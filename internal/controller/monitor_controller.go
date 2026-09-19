package controller

import (
	"context"
	"regexp"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	uptimekumaiov1alpha1 "uptime-kuma-operator/api/v1alpha1"
	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

type MonitorReconciler struct {
	Client   client.Client
	Kuma     kuma.Client
	Recorder record.EventRecorder
	// DriftCheckInterval is how often a reconcile that found nothing to sync
	// re-checks that Kuma still has the monitor it's supposed to, recreating
	// it if deleted out-of-band (e.g. manually in the Kuma UI).
	DriftCheckInterval time.Duration
	// DefaultTags lists tag names applied to every monitor this reconciler
	// manages, in addition to whatever the Monitor's own spec.tags
	// specifies. See sync.go's mergeTags doc comment.
	DefaultTags []string
	// LabelTagPatterns, when non-empty, turns on Phase 3's operator-wide
	// label-tag inference for every monitor this reconciler manages. See
	// sync.go's deriveLabelTags doc comment.
	LabelTagPatterns []*regexp.Regexp
}

func (r *MonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	mon := &uptimekumaiov1alpha1.Monitor{}
	if err := r.Client.Get(ctx, req.NamespacedName, mon); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Monitor not found, assuming it was deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	log.V(1).Info("reconcile triggered", "resourceVersion", mon.ResourceVersion, "generation", mon.Generation)

	if !mon.DeletionTimestamp.IsZero() {
		log.V(1).Info("Monitor marked for deletion, deleting its Kuma monitor")
		if mon.Status.MonitorID != "" {
			id, err := strconv.ParseInt(mon.Status.MonitorID, 10, 64)
			if err != nil {
				log.Error(err, "status.monitorID is not an integer, skipping Kuma delete",
					"monitorID", mon.Status.MonitorID)
			} else if err := r.Kuma.Delete(ctx, id); err != nil {
				return ctrl.Result{}, err
			} else {
				log.Info("deleted Kuma monitor", "monitorID", id)
			}
		}
		if controllerutil.ContainsFinalizer(mon, annotations.Finalizer) {
			controllerutil.RemoveFinalizer(mon, annotations.Finalizer)
			if err := r.Client.Update(ctx, mon); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(mon, annotations.Finalizer) {
		controllerutil.AddFinalizer(mon, annotations.Finalizer)
		if err := r.Client.Update(ctx, mon); err != nil {
			return ctrl.Result{}, err
		}
	}

	var existingID int64
	if mon.Status.MonitorID != "" {
		id, err := strconv.ParseInt(mon.Status.MonitorID, 10, 64)
		if err != nil {
			log.Error(err, "status.monitorID is not an integer, creating a new monitor",
				"monitorID", mon.Status.MonitorID)
		} else {
			existingID = id
		}
	}

	desiredSpec := toKumaSpec(mon.Spec)
	notificationIDs, groupID, err := resolveReferences(ctx, r.Kuma, mon.Spec.Notifications, mon.Spec.Group)
	if err != nil {
		recordSyncFailure(r.Recorder, mon, err)
		return ctrl.Result{}, err
	}
	desiredSpec.NotificationIDs = notificationIDs
	desiredSpec.GroupID = groupID

	if mon.Spec.HTTP != nil && desiredSpec.HTTP != nil {
		pass, err := resolveSecret(ctx, r.Client, mon.Namespace, mon.Spec.HTTP.BasicAuthPasswordSecretRef)
		if err != nil {
			recordSyncFailure(r.Recorder, mon, err)
			return ctrl.Result{}, err
		}
		desiredSpec.HTTP.BasicAuthPassword = pass

		token, err := resolveSecret(ctx, r.Client, mon.Namespace, mon.Spec.HTTP.BearerTokenSecretRef)
		if err != nil {
			recordSyncFailure(r.Recorder, mon, err)
			return ctrl.Result{}, err
		}
		desiredSpec.HTTP.BearerToken = token

		clientSecret, err := resolveSecret(ctx, r.Client, mon.Namespace, mon.Spec.HTTP.OAuthClientSecretRef)
		if err != nil {
			recordSyncFailure(r.Recorder, mon, err)
			return ctrl.Result{}, err
		}
		desiredSpec.HTTP.OAuthClientSecret = clientSecret
	}
	if mon.Spec.Gamedig != nil && desiredSpec.Gamedig != nil {
		token, err := resolveSecret(ctx, r.Client, mon.Namespace, mon.Spec.Gamedig.TokenSecretRef)
		if err != nil {
			recordSyncFailure(r.Recorder, mon, err)
			return ctrl.Result{}, err
		}
		desiredSpec.Gamedig.Token = token
	}

	if existingID != 0 && mon.Status.ObservedGeneration == mon.Generation {
		log.V(1).Info("desired configuration unchanged since last sync, checking Kuma for drift", "monitorID", existingID)
		liveSpecs, err := r.Kuma.ExistingSpecs(ctx)
		if err != nil {
			return ctrl.Result{}, err
		}
		if live, ok := liveSpecs[existingID]; ok {
			if kuma.Equivalent(desiredSpec, live) {
				// Nothing changed and Kuma still has it configured the way
				// we want — nothing to do, but check again later in case it's
				// deleted or edited out-of-band.
				if err := syncTags(ctx, r.Kuma, existingID, mergeTags(r.DefaultTags, mon.Spec.Tags, deriveLabelTags(mon.Labels, r.LabelTagPatterns))); err != nil {
					recordSyncFailure(r.Recorder, mon, err)
					return ctrl.Result{}, err
				}
				log.V(1).Info("Kuma monitor matches desired state, nothing to do", "monitorID", existingID)
				return ctrl.Result{RequeueAfter: r.DriftCheckInterval}, nil
			}
			log.Info("Kuma monitor configuration drifted from desired state, correcting", "monitorID", mon.Status.MonitorID)
		} else {
			log.Info("Kuma monitor no longer exists, recreating", "monitorID", mon.Status.MonitorID)
			existingID = 0 // force a create — the old id is gone, an update would fail
		}
	}

	generationToSync := mon.Generation
	creating := existingID == 0
	if creating {
		log.V(1).Info("no existing Kuma monitor, creating", "name", desiredSpec.Name, "type", desiredSpec.Type)
	} else {
		log.V(1).Info("spec changed or drift correction needed, syncing", "monitorID", existingID)
	}

	newID, err := r.Kuma.Upsert(ctx, existingID, desiredSpec)
	if err != nil {
		if uerr := r.updateStatus(ctx, mon, "", 0, metav1.ConditionFalse, "SyncFailed", err.Error()); uerr != nil {
			log.Error(uerr, "unable to record SyncFailed status on Monitor")
		}
		return ctrl.Result{}, err
	}
	if creating {
		log.Info("created Kuma monitor", "monitorID", newID, "name", desiredSpec.Name, "type", desiredSpec.Type)
	} else {
		log.Info("updated Kuma monitor", "monitorID", newID, "name", desiredSpec.Name, "type", desiredSpec.Type)
	}

	if err := r.updateStatus(ctx, mon, strconv.FormatInt(newID, 10), generationToSync, metav1.ConditionTrue, "Synced", "monitor synced to Kuma"); err != nil {
		return ctrl.Result{}, err
	}

	if err := syncTags(ctx, r.Kuma, newID, mergeTags(r.DefaultTags, mon.Spec.Tags, deriveLabelTags(mon.Labels, r.LabelTagPatterns))); err != nil {
		recordSyncFailure(r.Recorder, mon, err)
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: r.DriftCheckInterval}, nil
}

// updateStatus applies the Kuma monitor ID (when non-empty) and the Ready
// condition to mon's status, retrying on conflict.
//
// It writes only when something actually changed. Without that guard every
// reconcile would bump resourceVersion, which fires a fresh watch event, which
// reconciles again — an endless loop with a live Kuma round-trip per pass.
// meta.SetStatusCondition keeps LastTransitionTime stable while Status is
// unchanged, which is what makes "nothing changed" detectable at all.
func (r *MonitorReconciler) updateStatus(
	ctx context.Context,
	mon *uptimekumaiov1alpha1.Monitor,
	monitorID string,
	observedGeneration int64,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	key := client.ObjectKeyFromObject(mon)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Client.Get(ctx, key, mon); err != nil {
			return err
		}

		changed := false
		if monitorID != "" && mon.Status.MonitorID != monitorID {
			mon.Status.MonitorID = monitorID
			changed = true
		}
		if observedGeneration != 0 && mon.Status.ObservedGeneration != observedGeneration {
			mon.Status.ObservedGeneration = observedGeneration
			changed = true
		}
		if meta.SetStatusCondition(&mon.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  status,
			Reason:  reason,
			Message: message,
		}) {
			changed = true
		}
		if !changed {
			return nil
		}
		return r.Client.Status().Update(ctx, mon)
	})
}

func (r *MonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&uptimekumaiov1alpha1.Monitor{}).
		Complete(r)
}
