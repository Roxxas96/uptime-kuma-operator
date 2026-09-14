package controller

import (
	"context"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		if err := r.deleteAllMonitors(ctx, route.Annotations); err != nil {
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
		if len(existingIDs) == 0 {
			return ctrl.Result{}, nil
		}
		if err := r.deleteAllMonitors(ctx, route.Annotations); err != nil {
			return ctrl.Result{}, err
		}
		if route.Annotations != nil {
			annotations.SetMonitorIDs(route.Annotations, map[string]string{})
		}
		return ctrl.Result{}, r.Client.Update(ctx, route)
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
	newIDs, err := r.syncMonitors(ctx, desired, existingIDs)
	if err != nil {
		return ctrl.Result{}, err
	}

	if route.Annotations == nil {
		route.Annotations = map[string]string{}
	}
	annotations.SetMonitorIDs(route.Annotations, newIDs)
	if !controllerutil.ContainsFinalizer(route, annotations.Finalizer) {
		controllerutil.AddFinalizer(route, annotations.Finalizer)
	}
	return ctrl.Result{}, r.Client.Update(ctx, route)
}

func (r *HTTPRouteReconciler) syncMonitors(ctx context.Context, desired []derive.DesiredMonitor, existingIDs map[string]string) (map[string]string, error) {
	wanted := map[string]bool{}
	newIDs := map[string]string{}

	for _, dm := range desired {
		wanted[dm.Host] = true
		var id int64
		if idStr, ok := existingIDs[dm.Host]; ok {
			id, _ = strconv.ParseInt(idStr, 10, 64)
		}
		newID, err := r.Kuma.Upsert(ctx, id, dm.Spec)
		if err != nil {
			return nil, err
		}
		newIDs[dm.Host] = strconv.FormatInt(newID, 10)
	}

	for host, idStr := range existingIDs {
		if wanted[host] {
			continue
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		if err := r.Kuma.Delete(ctx, id); err != nil {
			return nil, err
		}
	}

	return newIDs, nil
}

func (r *HTTPRouteReconciler) deleteAllMonitors(ctx context.Context, ann map[string]string) error {
	ids, err := annotations.ParseMonitorIDs(ann)
	if err != nil {
		return err
	}
	for _, idStr := range ids {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		if err := r.Kuma.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *HTTPRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.HTTPRoute{}).
		Complete(r)
}
