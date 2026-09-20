package controller

import (
	"context"
	"regexp"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	// DefaultTags lists tag names applied to every monitor this reconciler
	// manages, in addition to whatever the HTTPRoute's own tags annotation
	// specifies. See sync.go's mergeTags doc comment.
	DefaultTags []string
	// LabelTagPatterns, when non-empty, turns on Phase 3's operator-wide
	// label-tag inference for every monitor this reconciler manages. See
	// sync.go's deriveLabelTags doc comment.
	LabelTagPatterns []*regexp.Regexp
}

func (r *HTTPRouteReconciler) params() hostReconcilerParams {
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

	return reconcileHostBasedResource(ctx, r.params(), route, "HTTPRoute", func(ov annotations.Overrides) ([]derive.DesiredMonitor, error) {
		return derive.HTTPRouteMonitors(route, ov), nil
	})
}

func (r *HTTPRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.HTTPRoute{}).
		Complete(r)
}
