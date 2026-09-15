package controller

import (
	"context"
	"strconv"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/config"
	"uptime-kuma-operator/internal/kuma"
)

func newHTTPRouteReconciler(optInByDefault bool) (*HTTPRouteReconciler, *kuma.FakeClient) {
	fake := kuma.NewFakeClient()
	return &HTTPRouteReconciler{
		Client:             k8sClient,
		Kuma:               fake,
		OptInByDefault:     optInByDefault,
		Recorder:           record.NewFakeRecorder(16),
		DriftCheckInterval: config.DefaultDriftCheckInterval,
	}, fake
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

func TestHTTPRouteReconciler_SecondReconcileReusesExistingMonitor(t *testing.T) {
	ctx := context.Background()
	r, fake := newHTTPRouteReconciler(false)

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: "idempotent", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"app.example.com"}},
	}
	if err := k8sClient.Create(ctx, route); err != nil {
		t.Fatalf("create HTTPRoute: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: route.Name, Namespace: route.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, route)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	first := &gatewayv1.HTTPRoute{}
	if err := k8sClient.Get(ctx, req.NamespacedName, first); err != nil {
		t.Fatalf("get HTTPRoute after first reconcile: %v", err)
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

	// See the identical assertion in TestIngressReconciler_SecondReconcileReusesExistingMonitor
	// for why this matters: kuma.Client.Upsert (editMonitor) restarts the
	// monitor's check timer even on an unchanged payload, so it must not be
	// called on a reconcile where nothing about the desired spec changed.
	if fake.UpsertCalls != 1 {
		t.Errorf("Kuma Upsert called %d times across two identical reconciles, want 1 — a no-op reconcile must not re-sync an unchanged monitor", fake.UpsertCalls)
	}

	second := &gatewayv1.HTTPRoute{}
	if err := k8sClient.Get(ctx, req.NamespacedName, second); err != nil {
		t.Fatalf("get HTTPRoute after second reconcile: %v", err)
	}
	secondIDs, err := annotations.ParseMonitorIDs(second.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if firstIDs["app.example.com"] == "" || firstIDs["app.example.com"] != secondIDs["app.example.com"] {
		t.Errorf("monitor id changed across reconciles: %v -> %v", firstIDs, secondIDs)
	}
}

// TestHTTPRouteReconciler_RecreatesMonitorDeletedOutOfBand mirrors
// TestIngressReconciler_RecreatesMonitorDeletedOutOfBand: skipping Kuma
// when the synced-hash is unchanged must not mean skipping it forever. If a
// monitor is deleted directly in Kuma, the next reconcile must notice via
// ExistingSpecs and recreate just that one, leaving healthy hosts untouched.
func TestHTTPRouteReconciler_RecreatesMonitorDeletedOutOfBand(t *testing.T) {
	ctx := context.Background()
	r, fake := newHTTPRouteReconciler(false)

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: "drifted", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"healthy.example.com", "drifted.example.com"}},
	}
	if err := k8sClient.Create(ctx, route); err != nil {
		t.Fatalf("create HTTPRoute: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: route.Name, Namespace: route.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, route)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	first := &gatewayv1.HTTPRoute{}
	if err := k8sClient.Get(ctx, req.NamespacedName, first); err != nil {
		t.Fatalf("get after first reconcile: %v", err)
	}
	firstIDs, err := annotations.ParseMonitorIDs(first.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if len(firstIDs) != 2 {
		t.Fatalf("monitor-ids = %v, want 2 entries", firstIDs)
	}

	drivenID, err := strconv.ParseInt(firstIDs["drifted.example.com"], 10, 64)
	if err != nil {
		t.Fatalf("parse drifted monitor id: %v", err)
	}
	if err := fake.Delete(ctx, drivenID); err != nil {
		t.Fatalf("simulate out-of-band delete: %v", err)
	}
	fake.DeleteCalls = 0
	fake.UpsertCalls = 0

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (should recreate the drifted host only): %v", err)
	}

	if fake.UpsertCalls != 1 {
		t.Errorf("Kuma Upsert called %d times, want exactly 1 (only the drifted host) — the healthy host must not be re-edited", fake.UpsertCalls)
	}
	if len(fake.Monitors) != 2 {
		t.Errorf("expected 2 monitors in Kuma after recreation, got %d", len(fake.Monitors))
	}

	second := &gatewayv1.HTTPRoute{}
	if err := k8sClient.Get(ctx, req.NamespacedName, second); err != nil {
		t.Fatalf("get after second reconcile: %v", err)
	}
	secondIDs, err := annotations.ParseMonitorIDs(second.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if secondIDs["healthy.example.com"] != firstIDs["healthy.example.com"] {
		t.Errorf("healthy.example.com id changed from %q to %q — it should have been left untouched",
			firstIDs["healthy.example.com"], secondIDs["healthy.example.com"])
	}
	if secondIDs["drifted.example.com"] == "" || secondIDs["drifted.example.com"] == firstIDs["drifted.example.com"] {
		t.Errorf("drifted.example.com id = %q, want a fresh id different from the deleted %q",
			secondIDs["drifted.example.com"], firstIDs["drifted.example.com"])
	}
}

// TestHTTPRouteReconciler_CorrectsMonitorConfigDriftedOutOfBand mirrors
// TestIngressReconciler_CorrectsMonitorConfigDriftedOutOfBand: skipping
// Kuma when the synced-hash is unchanged must not mean skipping it forever
// for content drift either. If a monitor's configuration is changed
// directly in Kuma, the next reconcile must notice via ExistingSpecs and
// correct just that one in place, leaving the healthy host untouched.
func TestHTTPRouteReconciler_CorrectsMonitorConfigDriftedOutOfBand(t *testing.T) {
	ctx := context.Background()
	r, fake := newHTTPRouteReconciler(false)

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: "config-drifted", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"healthy.example.com", "drifted.example.com"}},
	}
	if err := k8sClient.Create(ctx, route); err != nil {
		t.Fatalf("create HTTPRoute: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: route.Name, Namespace: route.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, route)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	first := &gatewayv1.HTTPRoute{}
	if err := k8sClient.Get(ctx, req.NamespacedName, first); err != nil {
		t.Fatalf("get after first reconcile: %v", err)
	}
	firstIDs, err := annotations.ParseMonitorIDs(first.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}

	drivenID, err := strconv.ParseInt(firstIDs["drifted.example.com"], 10, 64)
	if err != nil {
		t.Fatalf("parse drifted monitor id: %v", err)
	}
	fake.Monitors[drivenID] = kuma.MonitorSpec{
		Type: kuma.TypeHTTP, Name: fake.Monitors[drivenID].Name,
		HTTP: &kuma.HTTPSpec{URL: "https://changed-by-someone-else.example.com/"},
	}
	fake.UpsertCalls = 0

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (should correct the drift): %v", err)
	}

	if fake.UpsertCalls != 1 {
		t.Errorf("Kuma Upsert called %d times, want exactly 1 (only the drifted host) — the healthy host must not be re-edited", fake.UpsertCalls)
	}

	second := &gatewayv1.HTTPRoute{}
	if err := k8sClient.Get(ctx, req.NamespacedName, second); err != nil {
		t.Fatalf("get after second reconcile: %v", err)
	}
	secondIDs, err := annotations.ParseMonitorIDs(second.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if secondIDs["healthy.example.com"] != firstIDs["healthy.example.com"] {
		t.Errorf("healthy.example.com id changed from %q to %q — it should have been left untouched",
			firstIDs["healthy.example.com"], secondIDs["healthy.example.com"])
	}
	if secondIDs["drifted.example.com"] != firstIDs["drifted.example.com"] {
		t.Errorf("drifted.example.com id changed from %q to %q — a config correction must update in place, not recreate",
			firstIDs["drifted.example.com"], secondIDs["drifted.example.com"])
	}
	if got := fake.Monitors[drivenID].HTTP.URL; got != "https://drifted.example.com/" {
		t.Errorf("corrected URL = %q, want %q (derived from the HTTPRoute hostname)", got, "https://drifted.example.com/")
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
		t.Fatalf("expected 1 monitor after opt-in-by-default sync, got %d", len(fake.Monitors))
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

	// Opting out must also release our finalizer; otherwise the resource can
	// never be deleted once the operator is uninstalled.
	optedOut := &gatewayv1.HTTPRoute{}
	if err := k8sClient.Get(ctx, req.NamespacedName, optedOut); err != nil {
		t.Fatalf("get HTTPRoute after opt-out: %v", err)
	}
	if controllerutil.ContainsFinalizer(optedOut, annotations.Finalizer) {
		t.Errorf("finalizer %q still present after opt-out", annotations.Finalizer)
	}
	if _, ok := optedOut.Annotations[annotations.MonitorIDs]; ok {
		t.Errorf("monitor-ids annotation still present after opt-out: %q", optedOut.Annotations[annotations.MonitorIDs])
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
