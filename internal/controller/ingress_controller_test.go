package controller

import (
	"context"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

func newIngressReconciler(watchAll bool) (*IngressReconciler, *kuma.FakeClient) {
	fake := kuma.NewFakeClient()
	return &IngressReconciler{Client: k8sClient, Kuma: fake, WatchAll: watchAll}, fake
}

func TestIngressReconciler_DefaultMode_SkipsWithoutAnnotation(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "app.example.com"}}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	defer k8sClient.Delete(ctx, ing)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ing.Name, Namespace: ing.Namespace}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected no monitors created without opt-in annotation, got %d", len(fake.Monitors))
	}
}

func TestIngressReconciler_DefaultMode_SyncsWhenEnabled(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "app.example.com"}}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ing.Name, Namespace: ing.Namespace}}
	// Reconcile adds our finalizer, so a plain Delete would only mark the
	// object terminating and leave it stuck (blocking later tests reusing
	// this name/namespace). Reconcile once more after Delete so the
	// controller's own deletion handling removes the finalizer, same as a
	// real deletion event would.
	defer func() {
		_ = k8sClient.Delete(ctx, ing)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(fake.Monitors))
	}

	updated := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("get Ingress: %v", err)
	}
	ids, err := annotations.ParseMonitorIDs(updated.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if _, ok := ids["app.example.com"]; !ok {
		t.Errorf("monitor-ids annotation missing host app.example.com: %v", ids)
	}
}

func TestIngressReconciler_OptOutDeletesExistingMonitor(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(true) // watch-all mode

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "app.example.com"}}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ing.Name, Namespace: ing.Namespace}}
	// See comment in TestIngressReconciler_DefaultMode_SyncsWhenEnabled: the
	// first Reconcile below adds our finalizer, so cleanup must Reconcile
	// again after Delete to actually remove the object.
	defer func() {
		_ = k8sClient.Delete(ctx, ing)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile (sync via watch-all): %v", err)
	}
	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor after watch-all sync, got %d", len(fake.Monitors))
	}

	if err := k8sClient.Get(ctx, req.NamespacedName, ing); err != nil {
		t.Fatalf("get Ingress: %v", err)
	}
	if ing.Annotations == nil {
		ing.Annotations = map[string]string{}
	}
	ing.Annotations[annotations.Enabled] = "false"
	if err := k8sClient.Update(ctx, ing); err != nil {
		t.Fatalf("update Ingress with opt-out: %v", err)
	}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (opt-out): %v", err)
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected monitor deleted after opt-out, got %d remaining", len(fake.Monitors))
	}
}

func TestIngressReconciler_DeletionRemovesMonitorAndFinalizer(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "app.example.com"}}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ing.Name, Namespace: ing.Namespace}}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile (create): %v", err)
	}
	if err := k8sClient.Delete(ctx, ing); err != nil {
		t.Fatalf("delete Ingress: %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (finalize): %v", err)
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected monitor deleted after Ingress deletion, got %d remaining", len(fake.Monitors))
	}
}
