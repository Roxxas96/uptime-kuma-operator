package controller

import (
	"context"
	"strconv"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/derive"
	"uptime-kuma-operator/internal/kuma"
)

type IngressReconciler struct {
	Client   client.Client
	Kuma     kuma.Client
	WatchAll bool
}

func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ing := &networkingv1.Ingress{}
	if err := r.Client.Get(ctx, req.NamespacedName, ing); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !ing.DeletionTimestamp.IsZero() {
		if err := r.deleteAllMonitors(ctx, ing.Annotations); err != nil {
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

	if !annotations.ShouldSync(r.WatchAll, ing.Annotations) {
		existingIDs, err := annotations.ParseMonitorIDs(ing.Annotations)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(existingIDs) == 0 {
			return ctrl.Result{}, nil
		}
		if err := r.deleteAllMonitors(ctx, ing.Annotations); err != nil {
			return ctrl.Result{}, err
		}
		if ing.Annotations != nil {
			annotations.SetMonitorIDs(ing.Annotations, map[string]string{})
		}
		return ctrl.Result{}, r.Client.Update(ctx, ing)
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
	newIDs, err := r.syncMonitors(ctx, desired, existingIDs)
	if err != nil {
		return ctrl.Result{}, err
	}

	if ing.Annotations == nil {
		ing.Annotations = map[string]string{}
	}
	annotations.SetMonitorIDs(ing.Annotations, newIDs)
	if !controllerutil.ContainsFinalizer(ing, annotations.Finalizer) {
		controllerutil.AddFinalizer(ing, annotations.Finalizer)
	}
	return ctrl.Result{}, r.Client.Update(ctx, ing)
}

// syncMonitors upserts one monitor per desired host (reusing existingIDs[host]
// when present) and deletes any host present in existingIDs but absent from
// desired. It returns the new host -> id map.
func (r *IngressReconciler) syncMonitors(ctx context.Context, desired []derive.DesiredMonitor, existingIDs map[string]string) (map[string]string, error) {
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

func (r *IngressReconciler) deleteAllMonitors(ctx context.Context, ann map[string]string) error {
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

func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}
