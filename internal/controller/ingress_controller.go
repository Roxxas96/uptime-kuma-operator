package controller

import (
	"context"
	"regexp"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (r *IngressReconciler) params() hostReconcilerParams {
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

	return reconcileHostBasedResource(ctx, r.params(), ing, "Ingress", func(ov annotations.Overrides) ([]derive.DesiredMonitor, error) {
		return derive.IngressMonitors(ing, ov), nil
	})
}

func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}
