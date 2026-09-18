package controller

import (
	"context"
	"errors"
	"strconv"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/config"
	"uptime-kuma-operator/internal/kuma"
)

func newIngressReconciler(optInByDefault bool) (*IngressReconciler, *kuma.FakeClient) {
	fake := kuma.NewFakeClient()
	return &IngressReconciler{
		Client:             k8sClient,
		Kuma:               fake,
		OptInByDefault:     optInByDefault,
		Recorder:           record.NewFakeRecorder(16),
		DriftCheckInterval: config.DefaultDriftCheckInterval,
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

	// The real bug this guards against: a Kuma monitor stuck showing only
	// ever having completed one check. kuma.Client.Upsert (editMonitor)
	// restarts the monitor's check timer on Kuma's side even when nothing
	// about its configuration changed — so calling it on every reconcile,
	// rather than only when the desired spec actually changed, means a
	// resource reconciled more often than its check interval (unrelated
	// annotation churn, an informer resync, anything) never lets the
	// monitor complete more than one cycle. len(fake.Monitors) alone can't
	// catch this: Upsert with a nonzero id overwrites the same map key
	// whether it's called once or a hundred times.
	if fake.UpsertCalls != 1 {
		t.Errorf("Kuma Upsert called %d times across two identical reconciles, want 1 — a no-op reconcile must not re-sync an unchanged monitor", fake.UpsertCalls)
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

// TestIngressReconciler_RecreatesMonitorDeletedOutOfBand is the other half
// of the no-op-reconcile fix above: skipping Kuma when the synced-hash is
// unchanged must not mean skipping it forever. If a monitor is deleted
// directly in Kuma (not through the operator), the next reconcile — even
// with an unchanged Ingress — must notice via ExistingSpecs and recreate it,
// since the source (this Ingress) is still present.
func TestIngressReconciler_RecreatesMonitorDeletedOutOfBand(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "drifted", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{
			{Host: "healthy.example.com"},
			{Host: "drifted.example.com"},
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
		t.Fatalf("first Reconcile: %v", err)
	}
	first := &networkingv1.Ingress{}
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

	// Simulate deleting only the "drifted" host's monitor directly in Kuma.
	drivenID, err := strconv.ParseInt(firstIDs["drifted.example.com"], 10, 64)
	if err != nil {
		t.Fatalf("parse drifted monitor id: %v", err)
	}
	if err := fake.Delete(ctx, drivenID); err != nil {
		t.Fatalf("simulate out-of-band delete: %v", err)
	}
	fake.DeleteCalls = 0
	fake.UpsertCalls = 0 // reset: count only what the second Reconcile does

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (should recreate the drifted host only): %v", err)
	}

	if fake.UpsertCalls != 1 {
		t.Errorf("Kuma Upsert called %d times, want exactly 1 (only the drifted host) — the healthy host must not be re-edited", fake.UpsertCalls)
	}
	if len(fake.Monitors) != 2 {
		t.Errorf("expected 2 monitors in Kuma after recreation, got %d", len(fake.Monitors))
	}

	second := &networkingv1.Ingress{}
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

// TestIngressReconciler_CorrectsMonitorConfigDriftedOutOfBand covers the
// other kind of drift ExistingSpecs exists to catch: the monitor still
// exists in Kuma, but someone changed its configuration directly (e.g. in
// the Kuma UI) so it no longer matches what this Ingress derives. The next
// reconcile — even with an unchanged Ingress — must notice and correct it
// in place, without touching its Kuma ID or the healthy host.
func TestIngressReconciler_CorrectsMonitorConfigDriftedOutOfBand(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "config-drifted", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{
			{Host: "healthy.example.com"},
			{Host: "drifted.example.com"},
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
		t.Fatalf("first Reconcile: %v", err)
	}
	first := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, req.NamespacedName, first); err != nil {
		t.Fatalf("get after first reconcile: %v", err)
	}
	firstIDs, err := annotations.ParseMonitorIDs(first.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}

	// Simulate someone editing the "drifted" host's monitor directly in
	// Kuma, out of band — the Ingress itself is untouched.
	drivenID, err := strconv.ParseInt(firstIDs["drifted.example.com"], 10, 64)
	if err != nil {
		t.Fatalf("parse drifted monitor id: %v", err)
	}
	fake.Monitors[drivenID] = kuma.MonitorSpec{
		Type: kuma.TypeHTTP, Name: fake.Monitors[drivenID].Name,
		HTTP: &kuma.HTTPSpec{URL: "https://changed-by-someone-else.example.com/"},
	}
	fake.UpsertCalls = 0 // reset: count only what the second Reconcile does

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (should correct the drift): %v", err)
	}

	if fake.UpsertCalls != 1 {
		t.Errorf("Kuma Upsert called %d times, want exactly 1 (only the drifted host) — the healthy host must not be re-edited", fake.UpsertCalls)
	}

	second := &networkingv1.Ingress{}
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
	if got := fake.Monitors[drivenID].HTTP.URL; got != "http://drifted.example.com/" {
		t.Errorf("corrected URL = %q, want %q (derived from the Ingress rule)", got, "http://drifted.example.com/")
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

func TestIngressReconciler_SyncsPhase1Overrides(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "phase1-overrides", Namespace: "default",
			Annotations: map[string]string{
				annotations.Enabled:     "true",
				annotations.Description: "a friendly description",
				annotations.IgnoreTLS:   "true",
				annotations.Path:        "/healthz",
			},
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
		t.Fatalf("Reconcile: %v", err)
	}

	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(fake.Monitors))
	}
	var spec kuma.MonitorSpec
	for _, s := range fake.Monitors {
		spec = s
	}
	if spec.Description != "a friendly description" {
		t.Errorf("Description = %q, want %q", spec.Description, "a friendly description")
	}
	if spec.HTTP == nil || !spec.HTTP.IgnoreTLS {
		t.Fatalf("HTTP.IgnoreTLS = %v, want true", spec.HTTP)
	}
	if spec.HTTP.URL != "http://app.example.com/healthz" {
		t.Errorf("URL = %q, want %q", spec.HTTP.URL, "http://app.example.com/healthz")
	}
}

func TestIngressReconciler_ResolvesNotificationsAndGroup(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)
	fake.NotificationIDs = map[string]int64{"slack-prod": 1}
	fake.GroupIDs = map[string]int64{"prod-services": 5}

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-with-refs", Namespace: "default",
			Annotations: map[string]string{
				annotations.Enabled:       "true",
				annotations.Notifications: "slack-prod",
				annotations.Group:         "prod-services",
				annotations.Proxy:         "7",
			},
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
		t.Fatalf("Reconcile: %v", err)
	}

	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(fake.Monitors))
	}
	var spec kuma.MonitorSpec
	for _, s := range fake.Monitors {
		spec = s
	}
	if len(spec.NotificationIDs) != 1 || spec.NotificationIDs[0] != 1 {
		t.Errorf("NotificationIDs = %v, want [1]", spec.NotificationIDs)
	}
	if spec.GroupID == nil || *spec.GroupID != 5 {
		t.Errorf("GroupID = %v, want pointer to 5", spec.GroupID)
	}
	if spec.ProxyID == nil || *spec.ProxyID != 7 {
		t.Errorf("ProxyID = %v, want pointer to 7", spec.ProxyID)
	}
}

func TestIngressReconciler_AppliesDefaultTagsEvenWithoutOwnTags(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)
	r.DefaultTags = []string{"k8s"}

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-default-tags", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "default-tags.example.com"}}},
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
	idStr, ok := ids["default-tags.example.com"]
	if !ok {
		t.Fatalf("expected a tracked monitor ID for default-tags.example.com, got %v", ids)
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		t.Fatalf("monitor ID %q is not an integer: %v", idStr, err)
	}
	if _, ok := fake.TagIDs["k8s"]; !ok {
		t.Error("default tag k8s was not auto-created")
	}
	if len(fake.MonitorTags[id]) != 1 {
		t.Errorf("MonitorTags[id] = %v, want the 1 default tag applied even though the Ingress itself declares none", fake.MonitorTags[id])
	}
}

func TestIngressReconciler_SyncsTags(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-with-tags", Namespace: "default",
			Annotations: map[string]string{
				annotations.Enabled: "true",
				annotations.Tags:    "env-prod",
			},
		},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "tagged.example.com"}}},
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
	idStr, ok := ids["tagged.example.com"]
	if !ok {
		t.Fatalf("expected a tracked monitor ID for tagged.example.com, got %v", ids)
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		t.Fatalf("monitor ID %q is not an integer: %v", idStr, err)
	}
	if _, ok := fake.TagIDs["env-prod"]; !ok {
		t.Error("tag env-prod was not auto-created")
	}
	if len(fake.MonitorTags[id]) != 1 {
		t.Errorf("MonitorTags[id] = %v, want 1 entry", fake.MonitorTags[id])
	}

	// Reconcile again: the hash is unchanged, so this exercises the
	// specsMatch "nothing to do" drift-check path rather than syncMonitors —
	// tags must still be (re-)synced there since they can drift
	// independently of everything else.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(fake.MonitorTags[id]) != 1 {
		t.Errorf("after no-op reconcile, MonitorTags[id] = %v, want 1 entry", fake.MonitorTags[id])
	}
}

// A tag-sync failure must not cost us the record of the monitors that were
// just created in Kuma: without the monitor-ids annotation, the next
// reconcile upserts with id=0 and leaks a duplicate monitor on every retry.
func TestIngressReconciler_PersistsMonitorIDsDespiteTagSyncFailure(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(true)
	fake.SetMonitorTagsErr = errors.New("boom")

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web-tag-sync-failure", Namespace: "default"},
		Spec:       networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "tagfail.example.com"}}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ing.Name, Namespace: ing.Namespace}}
	defer func() {
		fake.SetMonitorTagsErr = nil
		_ = k8sClient.Delete(ctx, ing)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err == nil {
		t.Fatal("expected Reconcile to fail when tag sync fails")
	}
	if len(fake.Monitors) != 1 {
		t.Fatalf("expected exactly 1 monitor created despite the tag-sync failure, got %d", len(fake.Monitors))
	}

	updated := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("get Ingress: %v", err)
	}
	ids, err := annotations.ParseMonitorIDs(updated.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if _, ok := ids["tagfail.example.com"]; !ok {
		t.Fatalf("monitor-ids annotation missing host tagfail.example.com after tag-sync failure: %v", ids)
	}

	// A second reconcile, now that tag sync succeeds, must reuse the
	// existing monitor rather than creating a duplicate — proving the
	// persisted ID from the failed attempt above is what prevents the leak.
	fake.SetMonitorTagsErr = nil
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(fake.Monitors) != 1 {
		t.Errorf("expected still exactly 1 monitor after the retry succeeded, got %d (a duplicate was created)", len(fake.Monitors))
	}
}
