package controller

import (
	"context"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	uptimekumaiov1alpha1 "uptime-kuma-operator/api/v1alpha1"
	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

type MonitorReconciler struct {
	Client client.Client
	Kuma   kuma.Client
}

func (r *MonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
			if err == nil {
				if err := r.Kuma.Delete(ctx, id); err != nil {
					return ctrl.Result{}, err
				}
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
		existingID, _ = strconv.ParseInt(mon.Status.MonitorID, 10, 64)
	}

	newID, err := r.Kuma.Upsert(ctx, existingID, toKumaSpec(mon.Spec))
	if err != nil {
		mon.Status.Conditions = setReadyCondition(mon.Status.Conditions, metav1.ConditionFalse, "SyncFailed", err.Error())
		_ = r.Client.Status().Update(ctx, mon)
		return ctrl.Result{}, err
	}

	mon.Status.MonitorID = strconv.FormatInt(newID, 10)
	mon.Status.Conditions = setReadyCondition(mon.Status.Conditions, metav1.ConditionTrue, "Synced", "monitor synced to Kuma")
	if err := r.Client.Status().Update(ctx, mon); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func setReadyCondition(conditions []metav1.Condition, status metav1.ConditionStatus, reason, message string) []metav1.Condition {
	cond := metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
	for i, c := range conditions {
		if c.Type == "Ready" {
			conditions[i] = cond
			return conditions
		}
	}
	return append(conditions, cond)
}

func (r *MonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&uptimekumaiov1alpha1.Monitor{}).
		Complete(r)
}
