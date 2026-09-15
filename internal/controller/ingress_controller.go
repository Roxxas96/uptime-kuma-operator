package controller

import (
	"context"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

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
			return ctrl.Result{}, nil
		}
		if err := deleteAllMonitors(ctx, r.Kuma, ing.Annotations); err != nil {
			recordSyncFailure(r.Recorder, ing, err)
			return ctrl.Result{}, err
		}
		// Drop the monitor-ids annotation *and* the finalizer: an opted-out
		// resource is no longer ours, and a lingering finalizer would block
		// its deletion forever if the operator is uninstalled.
		return ctrl.Result{}, persistMonitorIDs(ctx, r.Client, ing, nil, false)
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
	newIDs, err := syncMonitors(ctx, r.Kuma, desired, existingIDs)
	if err != nil {
		recordSyncFailure(r.Recorder, ing, err)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, persistMonitorIDs(ctx, r.Client, ing, newIDs, true)
}

func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}
