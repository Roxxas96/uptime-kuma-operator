package controller

import (
	"context"
	"regexp"
	"strconv"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/derive"
	"uptime-kuma-operator/internal/kuma"
)

type IngressReconciler struct {
	Client client.Client
	Kuma   kuma.Client
	// OptInByDefault controls the annotation policy only — it is independent
	// of which namespaces the manager watches (config.Config.WatchAll).
	OptInByDefault bool
	Recorder       record.EventRecorder
	// DriftCheckInterval is how often a reconcile that found nothing to sync
	// re-checks that Kuma still has what it's supposed to. See sync.go's
	// specsMatch/reconcileDrift doc comments for why this exists.
	DriftCheckInterval time.Duration
	// DefaultTags lists tag names applied to every monitor this reconciler
	// manages, in addition to whatever the Ingress's own tags annotation
	// specifies. See sync.go's mergeTags doc comment.
	DefaultTags []string
	// LabelTagPatterns, when non-empty, turns on Phase 3's operator-wide
	// label-tag inference for every monitor this reconciler manages. See
	// sync.go's deriveLabelTags doc comment.
	LabelTagPatterns []*regexp.Regexp
}

func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	ing := &networkingv1.Ingress{}
	if err := r.Client.Get(ctx, req.NamespacedName, ing); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Ingress not found, assuming it was deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	log.V(1).Info("reconcile triggered", "resourceVersion", ing.ResourceVersion, "generation", ing.Generation)

	if !ing.DeletionTimestamp.IsZero() {
		log.V(1).Info("Ingress marked for deletion, deleting its Kuma monitors")
		if err := deleteAllMonitors(ctx, r.Kuma, ing.Annotations); err != nil {
			recordSyncFailure(r.Recorder, ing, err)
			return ctrl.Result{}, err
		}
		if controllerutil.ContainsFinalizer(ing, annotations.Finalizer) {
			controllerutil.RemoveFinalizer(ing, annotations.Finalizer)
			if err := r.Client.Update(ctx, ing); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !annotations.ShouldSync(r.OptInByDefault, ing.Annotations) {
		existingIDs, err := annotations.ParseMonitorIDs(ing.Annotations)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(existingIDs) == 0 && !controllerutil.ContainsFinalizer(ing, annotations.Finalizer) {
			log.V(1).Info("Ingress not opted in and has no tracked monitors, nothing to do", "optInByDefault", r.OptInByDefault)
			return ctrl.Result{}, nil
		}
		log.V(1).Info("Ingress opted out, deleting its Kuma monitors", "optInByDefault", r.OptInByDefault)
		if err := deleteAllMonitors(ctx, r.Kuma, ing.Annotations); err != nil {
			recordSyncFailure(r.Recorder, ing, err)
			return ctrl.Result{}, err
		}
		// Drop the monitor-ids annotation *and* the finalizer: an opted-out
		// resource is no longer ours, and a lingering finalizer would block
		// its deletion forever if the operator is uninstalled.
		return ctrl.Result{}, persistMonitorIDs(ctx, r.Client, ing, nil, false, "")
	}

	ov, err := annotations.ParseOverrides(ing.Annotations)
	if err != nil {
		return ctrl.Result{}, err
	}
	existingIDs, err := annotations.ParseMonitorIDs(ing.Annotations)
	if err != nil {
		return ctrl.Result{}, err
	}

	desired := derive.IngressMonitors(ing, ov)
	notificationIDs, groupID, err := resolveReferences(ctx, r.Kuma, ov.Notifications, ov.Group)
	if err != nil {
		recordSyncFailure(r.Recorder, ing, err)
		return ctrl.Result{}, err
	}
	for i := range desired {
		desired[i].Spec.NotificationIDs = notificationIDs
		desired[i].Spec.GroupID = groupID
	}
	hash, err := desiredHash(desired)
	if err != nil {
		return ctrl.Result{}, err
	}
	if hash == annotations.ParseSyncedHash(ing.Annotations) {
		// Nothing relevant (rules or override annotations) changed since the
		// last successful sync — skip the Kuma round-trip. See desiredHash's
		// doc comment for why this matters. But "skip" must not mean
		// "forever": verify Kuma still has what we think it has, and that
		// it's still configured the way we want, since a monitor deleted or
		// edited out-of-band (e.g. manually in the Kuma UI) would otherwise
		// never be noticed or corrected.
		log.V(1).Info("desired configuration unchanged since last sync, checking Kuma for drift", "hash", hash)
		liveSpecs, err := r.Kuma.ExistingSpecs(ctx)
		if err != nil {
			return ctrl.Result{}, err
		}
		if specsMatch(desired, existingIDs, liveSpecs) {
			for _, idStr := range existingIDs {
				id, err := strconv.ParseInt(idStr, 10, 64)
				if err != nil {
					continue // already logged/handled by the surrounding sync logic
				}
				if err := syncTags(ctx, r.Kuma, id, mergeTags(r.DefaultTags, ov.Tags, deriveLabelTags(ing.Labels, r.LabelTagPatterns))); err != nil {
					recordSyncFailure(r.Recorder, ing, err)
					return ctrl.Result{}, err
				}
			}
			log.V(1).Info("Kuma monitors match desired state, nothing to do", "hosts", len(desired))
			return ctrl.Result{RequeueAfter: r.DriftCheckInterval}, nil
		}
		log.V(1).Info("Kuma monitors drifted from desired state, correcting")
		newIDs, err := reconcileDrift(ctx, r.Kuma, desired, existingIDs, liveSpecs)
		if err != nil {
			recordSyncFailure(r.Recorder, ing, err)
			return ctrl.Result{}, err
		}
		// Persist the monitor IDs *before* syncing tags: the monitors already
		// exist in Kuma at this point, so a tag-sync failure that skipped this
		// write would leave the next reconcile with no record of them and
		// upsert duplicates with id=0.
		if err := persistMonitorIDs(ctx, r.Client, ing, newIDs, true, hash); err != nil {
			return ctrl.Result{}, err
		}
		for _, idStr := range newIDs {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				continue // already logged/handled by the surrounding sync logic
			}
			if err := syncTags(ctx, r.Kuma, id, mergeTags(r.DefaultTags, ov.Tags, deriveLabelTags(ing.Labels, r.LabelTagPatterns))); err != nil {
				recordSyncFailure(r.Recorder, ing, err)
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{RequeueAfter: r.DriftCheckInterval}, nil
	}

	log.V(1).Info("desired configuration changed, syncing", "oldHash", annotations.ParseSyncedHash(ing.Annotations), "newHash", hash, "hosts", len(desired))
	newIDs, err := syncMonitors(ctx, r.Kuma, desired, existingIDs)
	if err != nil {
		recordSyncFailure(r.Recorder, ing, err)
		return ctrl.Result{}, err
	}
	// Persist first, sync tags second — see the drift-correction branch above.
	if err := persistMonitorIDs(ctx, r.Client, ing, newIDs, true, hash); err != nil {
		return ctrl.Result{}, err
	}
	for _, idStr := range newIDs {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue // already logged/handled by the surrounding sync logic
		}
		if err := syncTags(ctx, r.Kuma, id, mergeTags(r.DefaultTags, ov.Tags, deriveLabelTags(ing.Labels, r.LabelTagPatterns))); err != nil {
			recordSyncFailure(r.Recorder, ing, err)
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: r.DriftCheckInterval}, nil
}

func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}
