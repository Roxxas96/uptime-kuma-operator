package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/derive"
	"uptime-kuma-operator/internal/kuma"
)

type HTTPRouteReconciler struct {
	Client   client.Client
	Kuma     kuma.Client
	WatchAll bool
	Recorder record.EventRecorder
}

func (r *HTTPRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	route := &gatewayv1.HTTPRoute{}
	if err := r.Client.Get(ctx, req.NamespacedName, route); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !route.DeletionTimestamp.IsZero() {
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

	if !annotations.ShouldSync(r.WatchAll, route.Annotations) {
		existingIDs, err := annotations.ParseMonitorIDs(route.Annotations)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(existingIDs) == 0 && !controllerutil.ContainsFinalizer(route, annotations.Finalizer) {
			return ctrl.Result{}, nil
		}
		if err := deleteAllMonitors(ctx, r.Kuma, route.Annotations); err != nil {
			recordSyncFailure(r.Recorder, route, err)
			return ctrl.Result{}, err
		}
		// Drop the monitor-ids annotation *and* the finalizer: an opted-out
		// resource is no longer ours, and a lingering finalizer would block
		// its deletion forever if the operator is uninstalled.
		return ctrl.Result{}, persistMonitorIDs(ctx, r.Client, route, nil, false)
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
	newIDs, err := syncMonitors(ctx, r.Kuma, desired, existingIDs)
	if err != nil {
		recordSyncFailure(r.Recorder, route, err)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, persistMonitorIDs(ctx, r.Client, route, newIDs, true)
}

func (r *HTTPRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.HTTPRoute{}).
		Complete(r)
}
