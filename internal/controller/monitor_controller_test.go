package controller

import (
	"context"
	"strconv"
	"testing"
	"time"

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
	defer k8sClient.Delete(ctx, mon)

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}); err != nil {
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

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := k8sClient.Get(ctx, req.NamespacedName, &uptimekumaiov1alpha1.Monitor{})
		if err != nil {
			break // gone, as expected once the finalizer is removed
		}
		time.Sleep(50 * time.Millisecond)
	}

	numID, _ := strconv.ParseInt(id, 10, 64)
	if _, ok := fake.Monitors[numID]; ok {
		t.Errorf("fake Kuma client still has monitor %d after Monitor CR deletion", numID)
	}
}
