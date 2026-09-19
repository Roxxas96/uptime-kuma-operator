package controller

import (
	"context"
	"reflect"
	"regexp"
	"sort"
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

func TestSpecsMatch(t *testing.T) {
	httpSpec := kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "a", HTTP: &kuma.HTTPSpec{URL: "https://a.example.com/"}}
	otherSpec := kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "a", HTTP: &kuma.HTTPSpec{URL: "https://changed.example.com/"}}
	desired := []derive.DesiredMonitor{
		{Host: "a.example.com", Spec: httpSpec},
		{Host: "b.example.com", Spec: httpSpec},
	}

	cases := []struct {
		name        string
		existingIDs map[string]string
		liveSpecs   map[int64]kuma.MonitorSpec
		want        bool
	}{
		{"empty existingIDs", nil, map[int64]kuma.MonitorSpec{}, true},
		{"all present and matching", map[string]string{"a.example.com": "1", "b.example.com": "2"},
			map[int64]kuma.MonitorSpec{1: httpSpec, 2: httpSpec}, true},
		{"one missing", map[string]string{"a.example.com": "1", "b.example.com": "2"},
			map[int64]kuma.MonitorSpec{1: httpSpec}, false},
		{"one present but config drifted", map[string]string{"a.example.com": "1", "b.example.com": "2"},
			map[int64]kuma.MonitorSpec{1: httpSpec, 2: otherSpec}, false},
		{"unparseable id counts as drift", map[string]string{"a.example.com": "not-a-number"}, map[int64]kuma.MonitorSpec{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := specsMatch(desired, c.existingIDs, c.liveSpecs); got != c.want {
				t.Errorf("specsMatch(%v, %v) = %v, want %v", c.existingIDs, c.liveSpecs, got, c.want)
			}
		})
	}
}

func TestReconcileDrift_OnlyTouchesDriftedHosts(t *testing.T) {
	ctx := context.Background()
	fake := kuma.NewFakeClient()

	// Pre-populate Kuma directly so IDs 1 and 2 both "exist" initially —
	// 1 stays untouched, 2 is what we'll treat as deleted out-of-band.
	id1, _ := fake.Upsert(ctx, 0, kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "a", HTTP: &kuma.HTTPSpec{URL: "https://a.example.com/"}})
	id2, _ := fake.Upsert(ctx, 0, kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "b", HTTP: &kuma.HTTPSpec{URL: "https://b.example.com/"}})
	if err := fake.Delete(ctx, id2); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	fake.UpsertCalls = 0 // reset so we can assert on reconcileDrift's own calls only

	existingIDs := map[string]string{
		"a.example.com": strconv.FormatInt(id1, 10),
		"b.example.com": strconv.FormatInt(id2, 10),
	}
	desired := []derive.DesiredMonitor{
		{Host: "a.example.com", Spec: kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "a", HTTP: &kuma.HTTPSpec{URL: "https://a.example.com/"}}},
		{Host: "b.example.com", Spec: kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "b", HTTP: &kuma.HTTPSpec{URL: "https://b.example.com/"}}},
	}
	liveSpecs, err := fake.ExistingSpecs(ctx)
	if err != nil {
		t.Fatalf("ExistingSpecs: %v", err)
	}

	newIDs, err := reconcileDrift(ctx, fake, desired, existingIDs, liveSpecs)
	if err != nil {
		t.Fatalf("reconcileDrift: %v", err)
	}

	if fake.UpsertCalls != 1 {
		t.Errorf("Kuma Upsert called %d times, want exactly 1 (only the missing host) — reconcileDrift must not touch hosts that still match", fake.UpsertCalls)
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

func TestReconcileDrift_CorrectsConfigDriftInPlace(t *testing.T) {
	ctx := context.Background()
	fake := kuma.NewFakeClient()

	id, _ := fake.Upsert(ctx, 0, kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "a", HTTP: &kuma.HTTPSpec{URL: "https://a.example.com/"}})
	// Simulate an out-of-band edit directly in Kuma, bypassing our Upsert.
	fake.Monitors[id] = kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "a", HTTP: &kuma.HTTPSpec{URL: "https://changed-by-someone-else.example.com/"}}
	fake.UpsertCalls = 0

	existingIDs := map[string]string{"a.example.com": strconv.FormatInt(id, 10)}
	desired := []derive.DesiredMonitor{
		{Host: "a.example.com", Spec: kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "a", HTTP: &kuma.HTTPSpec{URL: "https://a.example.com/"}}},
	}
	liveSpecs, err := fake.ExistingSpecs(ctx)
	if err != nil {
		t.Fatalf("ExistingSpecs: %v", err)
	}

	newIDs, err := reconcileDrift(ctx, fake, desired, existingIDs, liveSpecs)
	if err != nil {
		t.Fatalf("reconcileDrift: %v", err)
	}

	if fake.UpsertCalls != 1 {
		t.Errorf("Kuma Upsert called %d times, want exactly 1 (correcting the drifted config)", fake.UpsertCalls)
	}
	if newIDs["a.example.com"] != strconv.FormatInt(id, 10) {
		t.Errorf("a.example.com id = %q, want unchanged %d — a config correction must update in place, not recreate", newIDs["a.example.com"], id)
	}
	if got := fake.Monitors[id].HTTP.URL; got != "https://a.example.com/" {
		t.Errorf("corrected URL = %q, want %q", got, "https://a.example.com/")
	}
}

func TestResolveReferences(t *testing.T) {
	fake := kuma.NewFakeClient()
	fake.NotificationIDs = map[string]int64{"slack-prod": 1, "email-oncall": 2}
	fake.GroupIDs = map[string]int64{"prod-services": 5}

	notificationIDs, groupID, err := resolveReferences(context.Background(), fake, []string{"slack-prod", "email-oncall"}, "prod-services")
	if err != nil {
		t.Fatalf("resolveReferences: %v", err)
	}
	if len(notificationIDs) != 2 {
		t.Fatalf("notificationIDs = %v, want 2 entries", notificationIDs)
	}
	if groupID == nil || *groupID != 5 {
		t.Errorf("groupID = %v, want pointer to 5", groupID)
	}
}

// Kuma stores notification associations as a set, so a duplicated name must
// not produce a duplicated ID: [1,1] would never compare equal to the [1]
// Kuma returns, re-Upserting (and restarting the check timer) forever. The
// sort keeps desiredHash stable when the same names are merely reordered.
func TestResolveReferences_DedupesAndSortsNotificationIDs(t *testing.T) {
	fake := kuma.NewFakeClient()
	fake.NotificationIDs = map[string]int64{"slack-prod": 7, "email-oncall": 2}

	notificationIDs, _, err := resolveReferences(context.Background(), fake,
		[]string{"slack-prod", "email-oncall", "slack-prod"}, "")
	if err != nil {
		t.Fatalf("resolveReferences: %v", err)
	}
	if !reflect.DeepEqual(notificationIDs, []int64{2, 7}) {
		t.Errorf("notificationIDs = %v, want [2 7] (deduplicated and sorted)", notificationIDs)
	}

	reordered, _, err := resolveReferences(context.Background(), fake,
		[]string{"slack-prod", "email-oncall"}, "")
	if err != nil {
		t.Fatalf("resolveReferences (reordered): %v", err)
	}
	if !reflect.DeepEqual(reordered, notificationIDs) {
		t.Errorf("reordered names resolved to %v, want the same %v — order must not affect the result", reordered, notificationIDs)
	}
}

func TestResolveReferences_UnknownNotificationErrors(t *testing.T) {
	fake := kuma.NewFakeClient()
	_, _, err := resolveReferences(context.Background(), fake, []string{"does-not-exist"}, "")
	if err == nil {
		t.Fatal("expected error for unresolvable notification name, got nil")
	}
}

func TestResolveReferences_UnknownGroupErrors(t *testing.T) {
	fake := kuma.NewFakeClient()
	_, _, err := resolveReferences(context.Background(), fake, nil, "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unresolvable group name, got nil")
	}
}

func TestResolveReferences_NoReferencesNoCall(t *testing.T) {
	fake := kuma.NewFakeClient()
	notificationIDs, groupID, err := resolveReferences(context.Background(), fake, nil, "")
	if err != nil {
		t.Fatalf("resolveReferences: %v", err)
	}
	if notificationIDs != nil || groupID != nil {
		t.Errorf("resolveReferences(nil, \"\") = (%v, %v), want (nil, nil)", notificationIDs, groupID)
	}
}

func TestSyncTags_CreatesMissingTagsAndApplies(t *testing.T) {
	ctx := context.Background()
	fake := kuma.NewFakeClient()
	id, _ := fake.Upsert(ctx, 0, kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "web", HTTP: &kuma.HTTPSpec{URL: "https://a"}})
	fake.TagIDs["existing-tag"] = 1

	if err := syncTags(ctx, fake, id, []string{"existing-tag", "brand-new-tag"}); err != nil {
		t.Fatalf("syncTags: %v", err)
	}

	if _, ok := fake.TagIDs["brand-new-tag"]; !ok {
		t.Error("brand-new-tag was not created")
	}
	if len(fake.MonitorTags[id]) != 2 {
		t.Errorf("MonitorTags[id] = %v, want 2 entries", fake.MonitorTags[id])
	}
}

func TestSyncTags_EmptyClearsExistingTags(t *testing.T) {
	ctx := context.Background()
	fake := kuma.NewFakeClient()
	id, _ := fake.Upsert(ctx, 0, kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "web", HTTP: &kuma.HTTPSpec{URL: "https://a"}})

	if err := syncTags(ctx, fake, id, []string{"prod"}); err != nil {
		t.Fatalf("syncTags (seed): %v", err)
	}
	if len(fake.MonitorTags[id]) == 0 {
		t.Fatalf("MonitorTags[id] = %v, want the seeded tag present before testing the empty-tags case", fake.MonitorTags[id])
	}

	if err := syncTags(ctx, fake, id, nil); err != nil {
		t.Fatalf("syncTags: %v", err)
	}
	if len(fake.MonitorTags[id]) != 0 {
		t.Errorf("MonitorTags[id] = %v, want empty — tags are fully declarative, so omitting them clears any existing ones", fake.MonitorTags[id])
	}
}

func TestMergeTags_UnionsAndDedupes(t *testing.T) {
	got := mergeTags([]string{"k8s", "managed"}, []string{"managed", "prod"})
	want := []string{"k8s", "managed", "prod"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeTags = %v, want %v", got, want)
	}
}

func TestMergeTags_NoDefaultsReturnsSpecificUnchanged(t *testing.T) {
	got := mergeTags(nil, []string{"prod"})
	want := []string{"prod"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeTags = %v, want %v", got, want)
	}
}

func TestMergeTags_NoSpecificReturnsDefaultsOnly(t *testing.T) {
	got := mergeTags([]string{"k8s"}, nil)
	want := []string{"k8s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeTags = %v, want %v", got, want)
	}
}

func TestMergeTags_BothEmptyReturnsEmpty(t *testing.T) {
	if got := mergeTags(nil, nil); len(got) != 0 {
		t.Errorf("mergeTags = %v, want empty", got)
	}
}

func TestDeriveLabelTags_MatchesAnchoredPatterns(t *testing.T) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^(?:team)$`),
		regexp.MustCompile(`^(?:env-.*)$`),
	}
	labels := map[string]string{
		"team":     "platform",
		"env-tier": "prod",
		"my-team":  "should-not-match", // "team" is anchored, must not match this
	}
	got := deriveLabelTags(labels, patterns)
	sort.Strings(got)
	want := []string{"env-tier=prod", "team=platform"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deriveLabelTags = %v, want %v", got, want)
	}
}

func TestDeriveLabelTags_NoPatternsIsEmpty(t *testing.T) {
	got := deriveLabelTags(map[string]string{"team": "platform"}, nil)
	if len(got) != 0 {
		t.Errorf("deriveLabelTags with no patterns = %v, want empty", got)
	}
}

func TestDeriveLabelTags_NoMatchingLabelsIsEmpty(t *testing.T) {
	patterns := []*regexp.Regexp{regexp.MustCompile(`^(?:team)$`)}
	got := deriveLabelTags(map[string]string{"other": "value"}, patterns)
	if len(got) != 0 {
		t.Errorf("deriveLabelTags with no matching labels = %v, want empty", got)
	}
}

func TestDeriveLabelTags_EmptyLabelValue(t *testing.T) {
	patterns := []*regexp.Regexp{regexp.MustCompile(`^(?:tier)$`)}
	got := deriveLabelTags(map[string]string{"tier": ""}, patterns)
	want := []string{"tier="}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deriveLabelTags = %v, want %v", got, want)
	}
}

func TestMergeTags_ThreeWayUnionAndDedup(t *testing.T) {
	got := mergeTags([]string{"a", "b"}, []string{"b", "c"}, []string{"c", "d"})
	want := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeTags = %v, want %v", got, want)
	}
}
