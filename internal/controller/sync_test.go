package controller

import (
	"context"
	"strconv"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/derive"
	"uptime-kuma-operator/internal/kuma"
)

// TestPersistMonitorIDs_NoOpDoesNotWrite guards against the same
// self-triggering reconcile loop MonitorReconciler had (see its own
// TestMonitorReconciler_NoOpReconcileDoesNotBumpResourceVersion): if
// persistMonitorIDs writes on every call regardless of whether anything
// changed, Kubernetes bumps resourceVersion on every Update, which
// re-triggers a reconcile of the same Ingress/HTTPRoute, which calls
// kuma.Client.Upsert (editMonitor) again — restarting that monitor's check
// timer before it can ever complete more than one cycle. That's exactly the
// "monitor stuck at 1 check" symptom this test exists to prevent.
//
// persistMonitorIDs mutates its obj argument in place (it re-Gets into it,
// and a successful Update overwrites it with the server's response) — every
// test here fetches its own copy for the second call and captures
// ResourceVersion as a plain string before that call, so the comparison
// can't be aliased into passing or failing for the wrong reason.
func TestPersistMonitorIDs_NoOpDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	key := types.NamespacedName{Name: "noop-write", Namespace: "default"}

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Spec:       networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "app.example.com"}}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	defer func() {
		_ = k8sClient.Delete(ctx, ing)
	}()

	newIDs := map[string]string{"app.example.com": "1"}

	if err := persistMonitorIDs(ctx, k8sClient, ing, newIDs, true, "hash-a"); err != nil {
		t.Fatalf("first persistMonitorIDs: %v", err)
	}

	afterFirst := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, key, afterFirst); err != nil {
		t.Fatalf("get after first write: %v", err)
	}
	ids, err := annotations.ParseMonitorIDs(afterFirst.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if ids["app.example.com"] != "1" {
		t.Fatalf("monitor-ids = %v, want app.example.com=1", ids)
	}
	firstResourceVersion := afterFirst.ResourceVersion

	// Second call with the exact same newIDs and finalizer state, on a fresh
	// object of our own — nothing should change, so nothing should be written.
	callArg := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
	if err := persistMonitorIDs(ctx, k8sClient, callArg, newIDs, true, "hash-a"); err != nil {
		t.Fatalf("second persistMonitorIDs: %v", err)
	}

	afterSecond := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, key, afterSecond); err != nil {
		t.Fatalf("get after second write: %v", err)
	}

	if firstResourceVersion != afterSecond.ResourceVersion {
		t.Fatalf("resourceVersion changed on a no-op persistMonitorIDs call: %s -> %s (this re-triggers a reconcile, which calls Kuma's editMonitor again, restarting the monitor's check timer)",
			firstResourceVersion, afterSecond.ResourceVersion)
	}
}

// TestPersistMonitorIDs_WritesWhenIDsChange is the positive control: a real
// change to newIDs must still be persisted.
func TestPersistMonitorIDs_WritesWhenIDsChange(t *testing.T) {
	ctx := context.Background()
	key := types.NamespacedName{Name: "changed-write", Namespace: "default"}

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Spec:       networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "a.example.com"}}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	defer func() {
		_ = k8sClient.Delete(ctx, ing)
	}()

	if err := persistMonitorIDs(ctx, k8sClient, ing, map[string]string{"a.example.com": "1"}, true, "hash-a"); err != nil {
		t.Fatalf("first persistMonitorIDs: %v", err)
	}
	afterFirst := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, key, afterFirst); err != nil {
		t.Fatalf("get after first write: %v", err)
	}
	firstResourceVersion := afterFirst.ResourceVersion

	callArg := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
	if err := persistMonitorIDs(ctx, k8sClient, callArg, map[string]string{"a.example.com": "1", "b.example.com": "2"}, true, "hash-a"); err != nil {
		t.Fatalf("second persistMonitorIDs: %v", err)
	}
	afterSecond := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, key, afterSecond); err != nil {
		t.Fatalf("get after second write: %v", err)
	}

	if firstResourceVersion == afterSecond.ResourceVersion {
		t.Fatal("resourceVersion unchanged despite a real monitor-ids change — the write was wrongly skipped")
	}
	ids, err := annotations.ParseMonitorIDs(afterSecond.Annotations)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if ids["a.example.com"] != "1" || ids["b.example.com"] != "2" {
		t.Errorf("monitor-ids = %v, want a.example.com=1 b.example.com=2", ids)
	}
}

// TestPersistMonitorIDs_WritesWhenFinalizerChanges is the positive control
// for the other thing persistMonitorIDs mutates: adding/removing the
// finalizer must still be persisted even when the ID map itself is
// unchanged (the opt-out path calls this with identical empty newIDs but a
// finalizer that needs to come off).
func TestPersistMonitorIDs_WritesWhenFinalizerChanges(t *testing.T) {
	ctx := context.Background()
	key := types.NamespacedName{Name: "finalizer-write", Namespace: "default"}

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Spec:       networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "app.example.com"}}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	defer func() {
		_ = k8sClient.Delete(ctx, ing)
	}()

	if err := persistMonitorIDs(ctx, k8sClient, ing, nil, true, ""); err != nil {
		t.Fatalf("first persistMonitorIDs (add finalizer): %v", err)
	}
	afterFirst := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, key, afterFirst); err != nil {
		t.Fatalf("get after first write: %v", err)
	}
	firstResourceVersion := afterFirst.ResourceVersion

	callArg := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
	if err := persistMonitorIDs(ctx, k8sClient, callArg, nil, false, ""); err != nil {
		t.Fatalf("second persistMonitorIDs (remove finalizer): %v", err)
	}
	afterSecond := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, key, afterSecond); err != nil {
		t.Fatalf("get after second write: %v", err)
	}

	if firstResourceVersion == afterSecond.ResourceVersion {
		t.Fatal("resourceVersion unchanged despite a finalizer change — the write was wrongly skipped")
	}
}

// TestPersistMonitorIDs_WritesWhenHashChanges is the positive control for
// the hash field specifically: identical IDs and finalizer state, but a
// different hash (e.g. an override annotation changed, so derive produced a
// different desired spec even though the resulting host set is the same)
// must still be persisted.
func TestPersistMonitorIDs_WritesWhenHashChanges(t *testing.T) {
	ctx := context.Background()
	key := types.NamespacedName{Name: "hash-write", Namespace: "default"}

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Spec:       networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "app.example.com"}}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	defer func() {
		_ = k8sClient.Delete(ctx, ing)
	}()

	newIDs := map[string]string{"app.example.com": "1"}

	if err := persistMonitorIDs(ctx, k8sClient, ing, newIDs, true, "hash-a"); err != nil {
		t.Fatalf("first persistMonitorIDs: %v", err)
	}
	afterFirst := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, key, afterFirst); err != nil {
		t.Fatalf("get after first write: %v", err)
	}
	firstResourceVersion := afterFirst.ResourceVersion

	callArg := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
	if err := persistMonitorIDs(ctx, k8sClient, callArg, newIDs, true, "hash-b"); err != nil {
		t.Fatalf("second persistMonitorIDs: %v", err)
	}
	afterSecond := &networkingv1.Ingress{}
	if err := k8sClient.Get(ctx, key, afterSecond); err != nil {
		t.Fatalf("get after second write: %v", err)
	}

	if firstResourceVersion == afterSecond.ResourceVersion {
		t.Fatal("resourceVersion unchanged despite a hash change — the write was wrongly skipped")
	}
	if got := annotations.ParseSyncedHash(afterSecond.Annotations); got != "hash-b" {
		t.Errorf("SyncedHash = %q, want %q", got, "hash-b")
	}
}

func TestAllExist(t *testing.T) {
	cases := []struct {
		name        string
		existingIDs map[string]string
		liveIDs     map[int64]bool
		want        bool
	}{
		{"empty existingIDs", nil, map[int64]bool{}, true},
		{"all present", map[string]string{"a.example.com": "1", "b.example.com": "2"}, map[int64]bool{1: true, 2: true}, true},
		{"one missing", map[string]string{"a.example.com": "1", "b.example.com": "2"}, map[int64]bool{1: true}, false},
		{"unparseable id counts as missing", map[string]string{"a.example.com": "not-a-number"}, map[int64]bool{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := allExist(c.existingIDs, c.liveIDs); got != c.want {
				t.Errorf("allExist(%v, %v) = %v, want %v", c.existingIDs, c.liveIDs, got, c.want)
			}
		})
	}
}

func TestRecreateMissing_OnlyTouchesMissingHosts(t *testing.T) {
	ctx := context.Background()
	fake := kuma.NewFakeClient()

	// Pre-populate Kuma directly so IDs 1 and 2 both "exist" initially —
	// 1 stays, 2 is what we'll treat as deleted out-of-band.
	id1, _ := fake.Upsert(ctx, 0, kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "a", HTTP: &kuma.HTTPSpec{URL: "https://a.example.com/"}})
	id2, _ := fake.Upsert(ctx, 0, kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "b", HTTP: &kuma.HTTPSpec{URL: "https://b.example.com/"}})
	if err := fake.Delete(ctx, id2); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	fake.UpsertCalls = 0 // reset so we can assert on recreateMissing's own calls only

	existingIDs := map[string]string{
		"a.example.com": strconv.FormatInt(id1, 10),
		"b.example.com": strconv.FormatInt(id2, 10),
	}
	desired := []derive.DesiredMonitor{
		{Host: "a.example.com", Spec: kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "a", HTTP: &kuma.HTTPSpec{URL: "https://a.example.com/"}}},
		{Host: "b.example.com", Spec: kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "b", HTTP: &kuma.HTTPSpec{URL: "https://b.example.com/"}}},
	}
	liveIDs, err := fake.ExistingIDs(ctx)
	if err != nil {
		t.Fatalf("ExistingIDs: %v", err)
	}

	newIDs, err := recreateMissing(ctx, fake, desired, existingIDs, liveIDs)
	if err != nil {
		t.Fatalf("recreateMissing: %v", err)
	}

	if fake.UpsertCalls != 1 {
		t.Errorf("Kuma Upsert called %d times, want exactly 1 (only the missing host) — recreateMissing must not touch hosts that still exist", fake.UpsertCalls)
	}
	if newIDs["a.example.com"] != strconv.FormatInt(id1, 10) {
		t.Errorf("a.example.com id changed to %q, want unchanged %d", newIDs["a.example.com"], id1)
	}
	if newIDs["b.example.com"] == strconv.FormatInt(id2, 10) || newIDs["b.example.com"] == "" {
		t.Errorf("b.example.com id = %q, want a fresh id (was %d, now deleted)", newIDs["b.example.com"], id2)
	}
	if len(fake.Monitors) != 2 {
		t.Errorf("expected 2 monitors in Kuma after recreation, got %d", len(fake.Monitors))
	}
}
