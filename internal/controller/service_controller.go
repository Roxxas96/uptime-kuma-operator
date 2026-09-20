package controller

import (
	"context"
	"regexp"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/derive"
	"uptime-kuma-operator/internal/kuma"
)

type ServiceReconciler struct {
	Client client.Client
	Kuma   kuma.Client
	// OptInByDefault controls the annotation policy only — it is independent
	// of which namespaces the manager watches (config.Config.WatchAll).
	OptInByDefault bool
	Recorder       record.EventRecorder
	// DriftCheckInterval is how often a reconcile that found nothing to sync
	// re-checks that Kuma still has what it's supposed to.
	DriftCheckInterval time.Duration
	// DefaultTags lists tag names applied to every monitor this reconciler
	// manages, in addition to whatever the Service's own tags annotation
	// specifies. See sync.go's mergeTags doc comment.
	DefaultTags []string
	// LabelTagPatterns, when non-empty, turns on Phase 3's operator-wide
	// label-tag inference for every monitor this reconciler manages. See
	// sync.go's deriveLabelTags doc comment.
	LabelTagPatterns []*regexp.Regexp
}

func (r *ServiceReconciler) params() hostReconcilerParams {
	return hostReconcilerParams{
		Client:             r.Client,
		Kuma:               r.Kuma,
		OptInByDefault:     r.OptInByDefault,
		Recorder:           r.Recorder,
		DriftCheckInterval: r.DriftCheckInterval,
		DefaultTags:        r.DefaultTags,
		LabelTagPatterns:   r.LabelTagPatterns,
	}
}

func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	svc := &corev1.Service{}
	if err := r.Client.Get(ctx, req.NamespacedName, svc); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Service not found, assuming it was deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	log.V(1).Info("reconcile triggered", "resourceVersion", svc.ResourceVersion, "generation", svc.Generation)

	return reconcileHostBasedResource(ctx, r.params(), svc, "Service", func(ov annotations.Overrides) ([]derive.DesiredMonitor, error) {
		return derive.ServiceMonitors(svc, ov)
	})
}

func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Complete(r)
}
