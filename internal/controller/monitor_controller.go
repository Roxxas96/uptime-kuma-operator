package controller

import (
	"context"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	Client client.Client
	Kuma   kuma.Client
}

func (r *MonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	mon := &uptimekumaiov1alpha1.Monitor{}
	if err := r.Client.Get(ctx, req.NamespacedName, mon); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !mon.DeletionTimestamp.IsZero() {
		if mon.Status.MonitorID != "" {
			id, err := strconv.ParseInt(mon.Status.MonitorID, 10, 64)
			if err != nil {
				log.Error(err, "status.monitorID is not an integer, skipping Kuma delete",
					"monitorID", mon.Status.MonitorID)
			} else if err := r.Kuma.Delete(ctx, id); err != nil {
				return ctrl.Result{}, err
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

	if existingID != 0 && mon.Status.ObservedGeneration == mon.Generation {
		liveIDs, err := r.Kuma.ExistingIDs(ctx)
		if err != nil {
			return ctrl.Result{}, err
		}
		if liveIDs[existingID] {
			// Nothing changed and Kuma still has it — nothing to do, but
			// check again later in case it's deleted out-of-band.
			return ctrl.Result{RequeueAfter: driftCheckInterval}, nil
		}
		log.Info("Kuma monitor no longer exists, recreating", "monitorID", mon.Status.MonitorID)
		existingID = 0 // force a create — the old id is gone, an update would fail
	}

	generationToSync := mon.Generation

	newID, err := r.Kuma.Upsert(ctx, existingID, toKumaSpec(mon.Spec))
	if err != nil {
		if uerr := r.updateStatus(ctx, mon, "", 0, metav1.ConditionFalse, "SyncFailed", err.Error()); uerr != nil {
			log.Error(uerr, "unable to record SyncFailed status on Monitor")
		}
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, mon, strconv.FormatInt(newID, 10), generationToSync, metav1.ConditionTrue, "Synced", "monitor synced to Kuma"); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: driftCheckInterval}, nil
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
