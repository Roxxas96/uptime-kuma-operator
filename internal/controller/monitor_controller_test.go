package controller

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	zaplog "sigs.k8s.io/controller-runtime/pkg/log/zap"

	uptimekumaiov1alpha1 "uptime-kuma-operator/api/v1alpha1"
	"uptime-kuma-operator/internal/config"
	"uptime-kuma-operator/internal/kuma"
)

func newMonitorReconciler(t *testing.T) (*MonitorReconciler, *kuma.FakeClient) {
	t.Helper()
	fake := kuma.NewFakeClient()
	return &MonitorReconciler{Client: k8sClient, Kuma: fake, DriftCheckInterval: config.DefaultDriftCheckInterval}, fake
}

func TestMonitorReconciler_CreatesMonitorAndSetsStatus(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "game-server", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type:    uptimekumaiov1alpha1.MonitorTypeGamedig,
			Gamedig: &uptimekumaiov1alpha1.GamedigMonitorSpec{Host: "game.example.com", Port: 27015, Game: "csgo"},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	// Reconcile adds our finalizer, so a plain Delete would only mark the
	// object terminating and leave it stuck (blocking later tests reusing this
	// name/namespace). Reconcile once more after Delete so the controller's own
	// deletion handling removes the finalizer, same as a real deletion event
	// would. (Same fix as Task 12's IngressReconciler tests.)
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	updated := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}, updated); err != nil {
		t.Fatalf("get Monitor: %v", err)
	}
	if updated.Status.MonitorID == "" {
		t.Fatal("status.monitorID not set after reconcile")
	}
	id, err := strconv.ParseInt(updated.Status.MonitorID, 10, 64)
	if err != nil {
		t.Fatalf("status.monitorID %q is not an integer: %v", updated.Status.MonitorID, err)
	}
	if _, ok := fake.Monitors[id]; !ok {
		t.Fatalf("fake Kuma client has no monitor with id %d", id)
	}
}

func TestMonitorReconciler_DeletionRemovesKumaMonitorAndFinalizer(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "to-delete", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type: uptimekumaiov1alpha1.MonitorTypePing,
			Ping: &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.5"},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile (create): %v", err)
	}

	if err := k8sClient.Get(ctx, req.NamespacedName, mon); err != nil {
		t.Fatalf("get after create: %v", err)
	}
	id := mon.Status.MonitorID

	if err := k8sClient.Delete(ctx, mon); err != nil {
		t.Fatalf("delete Monitor: %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (finalize): %v", err)
	}

	gone := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := k8sClient.Get(ctx, req.NamespacedName, &uptimekumaiov1alpha1.Monitor{})
		if err != nil {
			gone = true // as expected once the finalizer is removed
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !gone {
		t.Error("Monitor CR still present after finalizing reconcile; finalizer was not removed")
	}

	numID, _ := strconv.ParseInt(id, 10, 64)
	if _, ok := fake.Monitors[numID]; ok {
		t.Errorf("fake Kuma client still has monitor %d after Monitor CR deletion", numID)
	}
}

// TestMonitorReconciler_SecondReconcileDoesNotWriteStatus guards against the
// self-triggering reconcile loop: an unconditional Status().Update bumps
// resourceVersion, which fires a watch event, which reconciles again, which
// hits Kuma again — forever.
func TestMonitorReconciler_SecondReconcileDoesNotWriteStatus(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "stable", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type: uptimekumaiov1alpha1.MonitorTypePing,
			Ping: &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.9"},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	first := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, first); err != nil {
		t.Fatalf("get after first reconcile: %v", err)
	}
	if len(first.Status.Conditions) == 0 {
		t.Fatal("expected a Ready condition after the first reconcile")
	}

	// metav1.Time serializes at second precision, so two reconciles inside the
	// same wall-clock second would produce an identical object even with the
	// old always-stamp-Now() behaviour and the API server would no-op the
	// write. Cross a second boundary so a regression actually shows up here —
	// which is also what happens in production, where the Kuma round-trip
	// alone takes longer than a second.
	time.Sleep(1100 * time.Millisecond)

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	second := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, second); err != nil {
		t.Fatalf("get after second reconcile: %v", err)
	}

	if first.ResourceVersion != second.ResourceVersion {
		t.Errorf("resourceVersion changed on a no-op reconcile: %s -> %s (self-triggering loop)",
			first.ResourceVersion, second.ResourceVersion)
	}
	if !first.Status.Conditions[0].LastTransitionTime.Equal(&second.Status.Conditions[0].LastTransitionTime) {
		t.Errorf("Ready condition LastTransitionTime changed on a no-op reconcile: %v -> %v",
			first.Status.Conditions[0].LastTransitionTime, second.Status.Conditions[0].LastTransitionTime)
	}
	if len(fake.Monitors) != 1 {
		t.Errorf("expected the existing Kuma monitor to be reused, got %d", len(fake.Monitors))
	}
	// The real bug this guards against: a Kuma monitor stuck showing only
	// ever having completed one check. kuma.Client.Upsert (editMonitor)
	// restarts the monitor's check timer even on an unchanged payload, so a
	// no-op reconcile (nothing changed since the last successful sync) must
	// not call it at all — len(fake.Monitors) alone can't catch this, since
	// Upsert with a nonzero id overwrites the same map key whether it's
	// called once or a hundred times.
	if fake.UpsertCalls != 1 {
		t.Errorf("Kuma Upsert called %d times across two identical reconciles, want 1 — a no-op reconcile must not re-sync an unchanged monitor", fake.UpsertCalls)
	}
}

// TestMonitorReconciler_RecreatesMonitorDeletedOutOfBand is the other half
// of the no-op-reconcile fix above: skipping Kuma when nothing changed must
// not mean skipping it forever. If the monitor is deleted directly in Kuma
// (not through the operator), the next reconcile — even with an unchanged
// spec — must notice via ExistingSpecs and recreate it, since the source
// (this Monitor CR) is still present.
func TestMonitorReconciler_RecreatesMonitorDeletedOutOfBand(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "drifted", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type: uptimekumaiov1alpha1.MonitorTypePing,
			Ping: &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.11"},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	first := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, first); err != nil {
		t.Fatalf("get after first reconcile: %v", err)
	}
	originalID := first.Status.MonitorID
	if originalID == "" {
		t.Fatal("status.monitorID not set after first reconcile")
	}

	// Simulate an operator deleting the monitor directly in Kuma, out of
	// band — the Monitor CR itself is untouched, spec unchanged.
	numericID, err := strconv.ParseInt(originalID, 10, 64)
	if err != nil {
		t.Fatalf("parse monitor id: %v", err)
	}
	if err := fake.Delete(ctx, numericID); err != nil {
		t.Fatalf("simulate out-of-band delete: %v", err)
	}
	fake.DeleteCalls = 0 // reset: that was test setup, not something Reconcile did

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (should recreate): %v", err)
	}

	second := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, second); err != nil {
		t.Fatalf("get after second reconcile: %v", err)
	}
	if second.Status.MonitorID == "" {
		t.Fatal("status.monitorID empty after recreation")
	}
	if second.Status.MonitorID == originalID {
		t.Errorf("status.monitorID unchanged (%s) — the deleted monitor's id can't have been reused, it must be a fresh one", originalID)
	}
	if len(fake.Monitors) != 1 {
		t.Errorf("expected exactly 1 monitor in Kuma after recreation, got %d", len(fake.Monitors))
	}
	newID, _ := strconv.ParseInt(second.Status.MonitorID, 10, 64)
	if _, ok := fake.Monitors[newID]; !ok {
		t.Errorf("fake Kuma client has no monitor with the recorded id %d", newID)
	}
}

// TestMonitorReconciler_CorrectsConfigDriftedOutOfBand covers the other
// kind of drift ExistingSpecs exists to catch: the monitor still exists in
// Kuma, but someone changed its configuration directly (e.g. in the Kuma
// UI) so it no longer matches the Monitor CR's spec. The next reconcile —
// even with an unchanged CR — must notice and correct it in place, without
// touching its Kuma ID.
func TestMonitorReconciler_CorrectsConfigDriftedOutOfBand(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "config-drifted", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type: uptimekumaiov1alpha1.MonitorTypePing,
			Ping: &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.20"},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	first := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, first); err != nil {
		t.Fatalf("get after first reconcile: %v", err)
	}
	originalID, err := strconv.ParseInt(first.Status.MonitorID, 10, 64)
	if err != nil {
		t.Fatalf("parse monitor id: %v", err)
	}

	// Simulate someone editing the monitor directly in Kuma, out of band —
	// the Monitor CR itself is untouched, spec unchanged.
	fake.Monitors[originalID] = kuma.MonitorSpec{
		Type: kuma.TypePing, Name: fake.Monitors[originalID].Name,
		Ping: &kuma.PingSpec{Host: "10.0.0.99"},
	}
	fake.UpsertCalls = 0 // reset: count only what the second Reconcile does

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (should correct the drift): %v", err)
	}

	if fake.UpsertCalls != 1 {
		t.Errorf("Kuma Upsert called %d times, want exactly 1 (correcting the drifted config)", fake.UpsertCalls)
	}

	second := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, second); err != nil {
		t.Fatalf("get after second reconcile: %v", err)
	}
	if second.Status.MonitorID != first.Status.MonitorID {
		t.Errorf("status.monitorID changed from %q to %q — a config correction must update in place, not recreate",
			first.Status.MonitorID, second.Status.MonitorID)
	}
	if got := fake.Monitors[originalID].Ping.Host; got != "10.0.0.20" {
		t.Errorf("corrected Ping.Host = %q, want %q (the Monitor CR's desired value)", got, "10.0.0.20")
	}
}

func TestMonitorReconciler_KumaFailureSetsReadyFalse(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)
	sentinel := errors.New("kuma: boom")
	fake.UpsertErr = sentinel

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "failing", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type: uptimekumaiov1alpha1.MonitorTypePing,
			Ping: &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.7"},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		fake.UpsertErr = nil
		fake.DeleteErr = nil
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	_, err := r.Reconcile(ctx, req)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Reconcile error = %v, want the Kuma error surfaced for requeue", err)
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected no monitors created on a failing Upsert, got %d", len(fake.Monitors))
	}

	updated := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("get Monitor: %v", err)
	}
	cond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
	if cond == nil {
		t.Fatal("no Ready condition set after a failed Kuma sync")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("Ready status = %q, want %q", cond.Status, metav1.ConditionFalse)
	}
	if cond.Reason != "SyncFailed" {
		t.Errorf("Ready reason = %q, want %q", cond.Reason, "SyncFailed")
	}
	if cond.Message != sentinel.Error() {
		t.Errorf("Ready message = %q, want %q", cond.Message, sentinel.Error())
	}
	if updated.Status.MonitorID != "" {
		t.Errorf("status.monitorID = %q, want empty after a failed Upsert", updated.Status.MonitorID)
	}
}

// TestMonitorSpec_RejectsSubStructForAnotherType covers the CEL discriminator
// rules, which only a real API server evaluates — hence envtest rather than a
// unit test.
func TestMonitorSpec_RejectsSubStructForAnotherType(t *testing.T) {
	ctx := context.Background()

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "mixed-types", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type:    uptimekumaiov1alpha1.MonitorTypeHTTP,
			HTTP:    &uptimekumaiov1alpha1.HTTPMonitorSpec{URL: "https://app.example.com/"},
			Gamedig: &uptimekumaiov1alpha1.GamedigMonitorSpec{Host: "game.example.com", Port: 27015, Game: "csgo"},
		},
	}

	err := k8sClient.Create(ctx, mon)
	if err == nil {
		_ = k8sClient.Delete(ctx, mon)
		t.Fatal("expected the API server to reject a HTTP Monitor that also sets spec.gamedig")
	}
	if !strings.Contains(err.Error(), "spec.gamedig must not be set unless type is Gamedig") {
		t.Errorf("rejection message = %q, want the spec.gamedig CEL rule message", err.Error())
	}
}

func TestMonitorReconciler_SyncsPhase1Fields(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "phase1-fields", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type:        uptimekumaiov1alpha1.MonitorTypePing,
			Description: "a friendly description",
			Ping:        &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.1", PacketSize: 64},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	updated := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("get Monitor: %v", err)
	}
	id, err := strconv.ParseInt(updated.Status.MonitorID, 10, 64)
	if err != nil {
		t.Fatalf("status.monitorID %q is not an integer: %v", updated.Status.MonitorID, err)
	}
	spec, ok := fake.Monitors[id]
	if !ok {
		t.Fatalf("fake Kuma client has no monitor with id %d", id)
	}
	if spec.Description != "a friendly description" {
		t.Errorf("Description = %q, want %q", spec.Description, "a friendly description")
	}
	if spec.Ping == nil || spec.Ping.PacketSize != 64 {
		t.Errorf("Ping = %+v, want PacketSize=64", spec.Ping)
	}
}

func TestMonitorReconciler_ResolvesNotificationsAndGroup(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)
	fake.NotificationIDs = map[string]int64{"slack-prod": 1}
	fake.GroupIDs = map[string]int64{"prod-services": 5}

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "web-with-refs", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type:          uptimekumaiov1alpha1.MonitorTypePing,
			Notifications: []string{"slack-prod"},
			Group:         "prod-services",
			Proxy:         7,
			Ping:          &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.1"},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	updated := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("get Monitor: %v", err)
	}
	id, err := strconv.ParseInt(updated.Status.MonitorID, 10, 64)
	if err != nil {
		t.Fatalf("status.monitorID %q is not an integer: %v", updated.Status.MonitorID, err)
	}
	spec, ok := fake.Monitors[id]
	if !ok {
		t.Fatalf("fake Kuma client has no monitor with id %d", id)
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

func TestMonitorReconciler_UnresolvableNotificationFailsReconcile(t *testing.T) {
	ctx := context.Background()
	r, _ := newMonitorReconciler(t)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "web-bad-notif", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type:          uptimekumaiov1alpha1.MonitorTypePing,
			Notifications: []string{"does-not-exist"},
			Ping:          &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.1"},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err == nil {
		t.Fatal("expected Reconcile to fail for an unresolvable notification name")
	}
}

func TestMonitorReconciler_SyncsTags(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "web-with-tags", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type: uptimekumaiov1alpha1.MonitorTypePing,
			Tags: []string{"env-prod"},
			Ping: &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.1"},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	updated := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("get Monitor: %v", err)
	}
	id, err := strconv.ParseInt(updated.Status.MonitorID, 10, 64)
	if err != nil {
		t.Fatalf("status.monitorID %q is not an integer: %v", updated.Status.MonitorID, err)
	}
	if _, ok := fake.TagIDs["env-prod"]; !ok {
		t.Error("tag env-prod was not auto-created")
	}
	if len(fake.MonitorTags[id]) != 1 {
		t.Errorf("MonitorTags[id] = %v, want 1 entry", fake.MonitorTags[id])
	}
}

func TestMonitorReconciler_ResolvesHTTPBasicAuthSecret(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "web-creds", Namespace: "default"},
		StringData: map[string]string{"password": "hunter2"},
	}
	if err := k8sClient.Create(ctx, secret); err != nil {
		t.Fatalf("create Secret: %v", err)
	}
	defer k8sClient.Delete(ctx, secret)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "web-with-auth", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type: uptimekumaiov1alpha1.MonitorTypeHTTP,
			HTTP: &uptimekumaiov1alpha1.HTTPMonitorSpec{
				URL: "https://example.com/", AuthMethod: "basic", BasicAuthUsername: "svc-account",
				BasicAuthPasswordSecretRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "web-creds"}, Key: "password",
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	updated := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("get Monitor: %v", err)
	}
	id, err := strconv.ParseInt(updated.Status.MonitorID, 10, 64)
	if err != nil {
		t.Fatalf("status.monitorID %q is not an integer: %v", updated.Status.MonitorID, err)
	}
	spec := fake.Monitors[id]
	if spec.HTTP == nil || spec.HTTP.BasicAuthPassword != "hunter2" {
		t.Errorf("HTTP.BasicAuthPassword = %q, want %q", spec.HTTP.BasicAuthPassword, "hunter2")
	}
}

func TestMonitorReconciler_MissingSecretFailsReconcile(t *testing.T) {
	ctx := context.Background()
	r, _ := newMonitorReconciler(t)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "web-bad-secret", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type: uptimekumaiov1alpha1.MonitorTypeHTTP,
			HTTP: &uptimekumaiov1alpha1.HTTPMonitorSpec{
				URL: "https://example.com/", AuthMethod: "bearer",
				BearerTokenSecretRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "does-not-exist"}, Key: "token",
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err == nil {
		t.Fatal("expected Reconcile to fail for a missing Secret")
	}
}

// TestMonitorReconciler_SecretNeverLogged guards against a resolved secret
// value ever reaching the controller's log output (e.g. via a stray
// "value", pass, or spec-dump log call). It captures the logger's output
// into a buffer via logf.IntoContext and asserts the plaintext password
// never appears in it, reusing
// TestMonitorReconciler_ResolvesHTTPBasicAuthSecret's Monitor/Secret setup.
func TestMonitorReconciler_SecretNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	log := zaplog.New(zaplog.WriteTo(&buf), zaplog.UseDevMode(true))
	ctx := logf.IntoContext(context.Background(), log)

	r, _ := newMonitorReconciler(t)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "web-creds-logcheck", Namespace: "default"},
		StringData: map[string]string{"password": "hunter2"},
	}
	if err := k8sClient.Create(ctx, secret); err != nil {
		t.Fatalf("create Secret: %v", err)
	}
	defer k8sClient.Delete(ctx, secret)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "web-with-auth-logcheck", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type: uptimekumaiov1alpha1.MonitorTypeHTTP,
			HTTP: &uptimekumaiov1alpha1.HTTPMonitorSpec{
				URL: "https://example.com/", AuthMethod: "basic", BasicAuthUsername: "svc-account",
				BasicAuthPasswordSecretRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "web-creds-logcheck"}, Key: "password",
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if bytes.Contains(buf.Bytes(), []byte("hunter2")) {
		t.Errorf("log output contains the plaintext secret value:\n%s", buf.String())
	}
}
