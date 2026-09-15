package controller

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/derive"
	"uptime-kuma-operator/internal/kuma"
)

type HTTPRouteReconciler struct {
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
}

func (r *HTTPRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	route := &gatewayv1.HTTPRoute{}
	if err := r.Client.Get(ctx, req.NamespacedName, route); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("HTTPRoute not found, assuming it was deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	log.V(1).Info("reconcile triggered", "resourceVersion", route.ResourceVersion, "generation", route.Generation)

	if !route.DeletionTimestamp.IsZero() {
		log.V(1).Info("HTTPRoute marked for deletion, deleting its Kuma monitors")
		if err := deleteAllMonitors(ctx, r.Kuma, route.Annotations); err != nil {
			recordSyncFailure(r.Recorder, route, err)
			return ctrl.Result{}, err
		}
		if controllerutil.ContainsFinalizer(route, annotations.Finalizer) {
			controllerutil.RemoveFinalizer(route, annotations.Finalizer)
			if err := r.Client.Update(ctx, route); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !annotations.ShouldSync(r.OptInByDefault, route.Annotations) {
		existingIDs, err := annotations.ParseMonitorIDs(route.Annotations)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(existingIDs) == 0 && !controllerutil.ContainsFinalizer(route, annotations.Finalizer) {
			log.V(1).Info("HTTPRoute not opted in and has no tracked monitors, nothing to do", "optInByDefault", r.OptInByDefault)
			return ctrl.Result{}, nil
		}
		log.V(1).Info("HTTPRoute opted out, deleting its Kuma monitors", "optInByDefault", r.OptInByDefault)
		if err := deleteAllMonitors(ctx, r.Kuma, route.Annotations); err != nil {
			recordSyncFailure(r.Recorder, route, err)
			return ctrl.Result{}, err
		}
		// Drop the monitor-ids annotation *and* the finalizer: an opted-out
		// resource is no longer ours, and a lingering finalizer would block
		// its deletion forever if the operator is uninstalled.
		return ctrl.Result{}, persistMonitorIDs(ctx, r.Client, route, nil, false, "")
	}

	ov, err := annotations.ParseOverrides(route.Annotations)
	if err != nil {
		return ctrl.Result{}, err
	}
	existingIDs, err := annotations.ParseMonitorIDs(route.Annotations)
	if err != nil {
		return ctrl.Result{}, err
	}

	desired := derive.HTTPRouteMonitors(route, ov)
	hash, err := desiredHash(desired)
	if err != nil {
		return ctrl.Result{}, err
	}
	if hash == annotations.ParseSyncedHash(route.Annotations) {
		// Nothing relevant (hostnames or override annotations) changed since
		// the last successful sync — skip the Kuma round-trip. See
		// desiredHash's doc comment for why this matters. But "skip" must not
		// mean "forever": verify Kuma still has what we think it has, and
		// that it's still configured the way we want, since a monitor
		// deleted or edited out-of-band (e.g. manually in the Kuma UI) would
		// otherwise never be noticed or corrected.
		log.V(1).Info("desired configuration unchanged since last sync, checking Kuma for drift", "hash", hash)
		liveSpecs, err := r.Kuma.ExistingSpecs(ctx)
		if err != nil {
			return ctrl.Result{}, err
		}
		if specsMatch(desired, existingIDs, liveSpecs) {
			log.V(1).Info("Kuma monitors match desired state, nothing to do", "hosts", len(desired))
			return ctrl.Result{RequeueAfter: r.DriftCheckInterval}, nil
		}
		log.V(1).Info("Kuma monitors drifted from desired state, correcting")
		newIDs, err := reconcileDrift(ctx, r.Kuma, desired, existingIDs, liveSpecs)
		if err != nil {
			recordSyncFailure(r.Recorder, route, err)
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: r.DriftCheckInterval}, persistMonitorIDs(ctx, r.Client, route, newIDs, true, hash)
	}

	log.V(1).Info("desired configuration changed, syncing", "oldHash", annotations.ParseSyncedHash(route.Annotations), "newHash", hash, "hosts", len(desired))
	newIDs, err := syncMonitors(ctx, r.Kuma, desired, existingIDs)
	if err != nil {
		recordSyncFailure(r.Recorder, route, err)
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: r.DriftCheckInterval}, persistMonitorIDs(ctx, r.Client, route, newIDs, true, hash)
}

func (r *HTTPRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.HTTPRoute{}).
		Complete(r)
}
