package controller

import (
	"context"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

func newIngressReconciler(optInByDefault bool) (*IngressReconciler, *kuma.FakeClient) {
	fake := kuma.NewFakeClient()
	return &IngressReconciler{
		Client:         k8sClient,
		Kuma:           fake,
		OptInByDefault: optInByDefault,
		Recorder:       record.NewFakeRecorder(16),
	}, fake
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
	r, fake := newIngressReconciler(true) // opt-in by default

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
		t.Fatalf("first Reconcile (sync via opt-in-by-default): %v", err)
	}
	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor after opt-in-by-default sync, got %d", len(fake.Monitors))
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

	// Opting out must also release our finalizer; otherwise the resource can
	// never be deleted once the operator is uninstalled.
	optedOut := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, req.NamespacedName, optedOut); err != nil {
		t.Fatalf("get Ingress after opt-out: %v", err)
	}
	if controllerutil.ContainsFinalizer(optedOut, annotations.Finalizer) {
		t.Errorf("finalizer %q still present after opt-out", annotations.Finalizer)
	}
	if _, ok := optedOut.Annotations[annotations.MonitorIDs]; ok {
		t.Errorf("monitor-ids annotation still present after opt-out: %q", optedOut.Annotations[annotations.MonitorIDs])
	}
}

func TestIngressReconciler_ContractionDeletesRemovedHost(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "shrinking", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{
			{Host: "a.example.com"},
			{Host: "b.example.com"},
		}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ing.Name, Namespace: ing.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, ing)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile (2 hosts): %v", err)
	}
	if len(fake.Monitors) != 2 {
		t.Fatalf("expected 2 monitors after first reconcile, got %d", len(fake.Monitors))
	}

	if err := k8sClient.Get(ctx, req.NamespacedName, ing); err != nil {
		t.Fatalf("get Ingress: %v", err)
	}
	ing.Spec.Rules = []networkingv1.IngressRule{{Host: "a.example.com"}}
	if err := k8sClient.Update(ctx, ing); err != nil {
		t.Fatalf("update Ingress to 1 host: %v", err)
	}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (1 host): %v", err)
	}
	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor after contraction, got %d", len(fake.Monitors))
	}

	updated := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("get Ingress after contraction: %v", err)
	}
	ids, err := annotations.ParseMonitorIDs(updated.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("monitor-ids = %v, want exactly one entry", ids)
	}
	if _, ok := ids["a.example.com"]; !ok {
		t.Errorf("monitor-ids = %v, want only the remaining host a.example.com", ids)
	}
}

func TestIngressReconciler_SecondReconcileReusesExistingMonitor(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "idempotent", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "app.example.com"}}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ing.Name, Namespace: ing.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, ing)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	first := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, req.NamespacedName, first); err != nil {
		t.Fatalf("get Ingress after first reconcile: %v", err)
	}
	firstIDs, err := annotations.ParseMonitorIDs(first.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(fake.Monitors) != 1 {
		t.Fatalf("expected the existing monitor to be reused, got %d monitors in Kuma", len(fake.Monitors))
	}

	second := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, req.NamespacedName, second); err != nil {
		t.Fatalf("get Ingress after second reconcile: %v", err)
	}
	secondIDs, err := annotations.ParseMonitorIDs(second.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if firstIDs["app.example.com"] == "" || firstIDs["app.example.com"] != secondIDs["app.example.com"] {
		t.Errorf("monitor id changed across reconciles: %v -> %v", firstIDs, secondIDs)
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
