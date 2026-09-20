package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/config"
	"uptime-kuma-operator/internal/kuma"
)

func newServiceReconciler(optInByDefault bool) (*ServiceReconciler, *kuma.FakeClient) {
	fake := kuma.NewFakeClient()
	return &ServiceReconciler{
		Client:             k8sClient,
		Kuma:               fake,
		OptInByDefault:     optInByDefault,
		Recorder:           record.NewFakeRecorder(16),
		DriftCheckInterval: config.DefaultDriftCheckInterval,
	}, fake
}

func TestServiceReconciler_DefaultMode_SkipsWithoutAnnotation(t *testing.T) {
	ctx := context.Background()
	r, fake := newServiceReconciler(false)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("create Service: %v", err)
	}
	defer k8sClient.Delete(ctx, svc)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected no monitors created without opt-in annotation, got %d", len(fake.Monitors))
	}
}

func TestServiceReconciler_DefaultsToHTTPMonitor(t *testing.T) {
	ctx := context.Background()
	r, fake := newServiceReconciler(false)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("create Service: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, svc)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(fake.Monitors))
	}
	var spec kuma.MonitorSpec
	for _, s := range fake.Monitors {
		spec = s
	}
	if spec.Type != kuma.TypeHTTP {
		t.Errorf("Type = %q, want %q (default)", spec.Type, kuma.TypeHTTP)
	}
	wantURL := "http://web.default.svc.cluster.local:8080/"
	if spec.HTTP == nil || spec.HTTP.URL != wantURL {
		t.Errorf("URL = %v, want %q", spec.HTTP, wantURL)
	}
}

func TestServiceReconciler_TypeAnnotationSelectsTCPMonitor(t *testing.T) {
	ctx := context.Background()
	r, fake := newServiceReconciler(false)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "redis", Namespace: "default",
			Annotations: map[string]string{
				annotations.Enabled: "true",
				annotations.Type:    "TCP",
			},
		},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 6379}}},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("create Service: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, svc)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(fake.Monitors))
	}
	var spec kuma.MonitorSpec
	for _, s := range fake.Monitors {
		spec = s
	}
	if spec.Type != kuma.TypeTCP {
		t.Errorf("Type = %q, want %q", spec.Type, kuma.TypeTCP)
	}
	if spec.TCP == nil || spec.TCP.Host != "redis.default.svc.cluster.local" || spec.TCP.Port != 6379 {
		t.Errorf("TCP spec = %+v, want Host=redis.default.svc.cluster.local Port=6379", spec.TCP)
	}
}

func TestServiceReconciler_AmbiguousPortFailsReconcileWithoutCreatingAMonitor(t *testing.T) {
	ctx := context.Background()
	r, fake := newServiceReconciler(false)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "multi-port", Namespace: "default",
			Annotations: map[string]string{
				annotations.Enabled: "true",
				annotations.Type:    "TCP",
			},
		},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 80}, {Name: "https", Port: 443}}},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("create Service: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, svc)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err == nil {
		t.Fatal("expected Reconcile to fail for an ambiguous port, got nil")
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected no monitor created when port resolution fails, got %d", len(fake.Monitors))
	}
}

func TestServiceReconciler_DeletionRemovesMonitorAndFinalizer(t *testing.T) {
	ctx := context.Background()
	r, fake := newServiceReconciler(false)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("create Service: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile (create): %v", err)
	}
	if err := k8sClient.Delete(ctx, svc); err != nil {
		t.Fatalf("delete Service: %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (finalize): %v", err)
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected monitor deleted after Service deletion, got %d remaining", len(fake.Monitors))
	}
}
