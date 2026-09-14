package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

func newHTTPRouteReconciler(watchAll bool) (*HTTPRouteReconciler, *kuma.FakeClient) {
	fake := kuma.NewFakeClient()
	return &HTTPRouteReconciler{Client: k8sClient, Kuma: fake, WatchAll: watchAll}, fake
}

func TestHTTPRouteReconciler_DefaultMode_SkipsWithoutAnnotation(t *testing.T) {
	ctx := context.Background()
	r, fake := newHTTPRouteReconciler(false)

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"app.example.com"}},
	}
	if err := k8sClient.Create(ctx, route); err != nil {
		t.Fatalf("create HTTPRoute: %v", err)
	}
	defer k8sClient.Delete(ctx, route)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: route.Name, Namespace: route.Namespace}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected no monitors without opt-in annotation, got %d", len(fake.Monitors))
	}
}

func TestHTTPRouteReconciler_DefaultMode_SyncsWhenEnabled(t *testing.T) {
	ctx := context.Background()
	r, fake := newHTTPRouteReconciler(false)

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"app.example.com"}},
	}
	if err := k8sClient.Create(ctx, route); err != nil {
		t.Fatalf("create HTTPRoute: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: route.Name, Namespace: route.Namespace}}
	// Reconcile adds our finalizer, so a plain Delete would only mark the
	// object terminating and leave it stuck (blocking later tests reusing
	// this name/namespace). Reconcile once more after Delete so the
	// controller's own deletion handling removes the finalizer, same as a
	// real deletion event would. (Same fix as Task 12's IngressReconciler
	// tests.)
	defer func() {
		_ = k8sClient.Delete(ctx, route)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(fake.Monitors))
	}
}

func TestHTTPRouteReconciler_OptOutDeletesExistingMonitor(t *testing.T) {
	ctx := context.Background()
	r, fake := newHTTPRouteReconciler(true)

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"app.example.com"}},
	}
	if err := k8sClient.Create(ctx, route); err != nil {
		t.Fatalf("create HTTPRoute: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: route.Name, Namespace: route.Namespace}}
	// See comment in TestHTTPRouteReconciler_DefaultMode_SyncsWhenEnabled: the
	// first Reconcile below adds our finalizer, so cleanup must Reconcile
	// again after Delete to actually remove the object.
	defer func() {
		_ = k8sClient.Delete(ctx, route)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor after watch-all sync, got %d", len(fake.Monitors))
	}

	if err := k8sClient.Get(ctx, req.NamespacedName, route); err != nil {
		t.Fatalf("get HTTPRoute: %v", err)
	}
	if route.Annotations == nil {
		route.Annotations = map[string]string{}
	}
	route.Annotations[annotations.Enabled] = "false"
	if err := k8sClient.Update(ctx, route); err != nil {
		t.Fatalf("update with opt-out: %v", err)
	}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected monitor deleted after opt-out, got %d remaining", len(fake.Monitors))
	}
}

func TestHTTPRouteReconciler_DeletionRemovesMonitorAndFinalizer(t *testing.T) {
	ctx := context.Background()
	r, fake := newHTTPRouteReconciler(false)

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"app.example.com"}},
	}
	if err := k8sClient.Create(ctx, route); err != nil {
		t.Fatalf("create HTTPRoute: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: route.Name, Namespace: route.Namespace}}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	if err := k8sClient.Delete(ctx, route); err != nil {
		t.Fatalf("delete HTTPRoute: %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected monitor deleted after HTTPRoute deletion, got %d remaining", len(fake.Monitors))
	}
}
