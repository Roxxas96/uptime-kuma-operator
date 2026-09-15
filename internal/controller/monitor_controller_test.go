package controller

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	uptimekumaiov1alpha1 "uptime-kuma-operator/api/v1alpha1"
	"uptime-kuma-operator/internal/kuma"
)

func newMonitorReconciler(t *testing.T) (*MonitorReconciler, *kuma.FakeClient) {
	t.Helper()
	fake := kuma.NewFakeClient()
	return &MonitorReconciler{Client: k8sClient, Kuma: fake}, fake
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
