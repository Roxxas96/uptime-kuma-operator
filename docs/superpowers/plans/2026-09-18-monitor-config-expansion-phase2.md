# Monitor Configuration Expansion — Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add tags, notification channels, a proxy, and a parent group to every monitor type, plus HTTP Basic/Bearer/OAuth2-Client-Credentials auth and Gamedig's `token`, sourced from Kubernetes Secrets.

**Architecture:** Three waves. Wave 1 (notifications/proxy/group) and Wave 3 (HTTP auth/Gamedig token) both resolve a reference *once* per reconcile, before building the final `kuma.MonitorSpec`, so the resolved value flows through the existing `ToBremlMonitor`/`FromBremlMonitor`/`Equivalent` pipeline exactly like a Phase 1 field. Wave 2 (tags) is the exception: Kuma manages monitor-tag associations via separate calls outside a monitor's own payload, so it gets its own post-`Upsert` reconciliation step, entirely outside `Equivalent`.

**Tech Stack:** Go, controller-runtime, `github.com/breml/go-uptime-kuma-client` v0.4.2.

**Spec:** `docs/superpowers/specs/2026-09-18-monitor-config-expansion-phase2-design.md`

## Global Constraints

- Every field that flows through the normal `Upsert` payload (`NotificationIDs`, `GroupID`, `ProxyID`, and every Wave 3 HTTP-auth/Gamedig field) MUST be wired into `ToBremlMonitor`, `FromBremlMonitor`, AND `Equivalent` — a field in only some of these is a silent drift-detection gap.
- `kuma.MonitorSpec` holds only **resolved** values (IDs, or already-read secret values) — never raw names or `SecretKeySelector`s. Name/Secret resolution happens in the `internal/controller` reconcilers, which hold both a `kuma.Client` and a controller-runtime `client.Client`; `internal/kuma`'s `ToBremlMonitor`/`FromBremlMonitor`/`Equivalent` stay pure, no I/O.
- A missing notification name or missing group name is a hard reconcile error (via the existing `recordSyncFailure` Event pattern) — never silently skipped.
- Tags are the only by-name reference that auto-creates on miss.
- Annotations exist only for fields with a sane string representation: `tags`, `notifications`, `proxy`, `group` (all common fields, same treatment as Phase 1's `description`/`resendInterval`). HTTP auth and Gamedig `token` are CRD-only — a `SecretKeySelector` has no annotation form.
- CRD JSON tags use `lowerCamelCase` with `omitempty`; new int64 fields (`proxy`) need no `int32`/`int` cast anywhere since `Base.ProxyID` is `*int64` end-to-end.
- Run `make test`, `gofmt -l .` (excluding `bin/`), and `go vet ./...` clean before considering any task done. After any change to `api/v1alpha1/monitor_types.go`, run `make generate manifests`.
- Never log, annotate, or write a resolved secret value anywhere except directly into the `kuma.MonitorSpec` passed to `Upsert`. Task 9's tests must assert a secret value does NOT appear in captured log output.
- NTLM and mTLS HTTP auth methods are explicitly out of scope for this plan.

---

### Task 1: kuma package — Notifications, FindGroup, and the three new resolved-ID fields

**Files:**
- Modify: `internal/kuma/client.go`
- Modify: `internal/kuma/fake.go`
- Modify: `internal/kuma/spec.go`
- Modify: `internal/kuma/translate.go`
- Test: `internal/kuma/fake_test.go`, `internal/kuma/translate_test.go` (no `realClient`-specific test — same as every other `realClient` method, it's only exercised against a real Kuma instance, never unit-tested directly; `FakeClient`'s equivalent behavior is what reconciler tests exercise)

**Interfaces:**
- Produces: `Client` gains `Notifications(ctx) (map[string]int64, error)` and `FindGroup(ctx, name string) (id int64, found bool, err error)`. `MonitorSpec` gains `NotificationIDs []int64`, `GroupID *int64`, `ProxyID *int64`.
- Consumed by: Task 2 (CRD/annotations, no direct dependency), Task 3 (reconcilers call `Notifications`/`FindGroup` and set the three new `MonitorSpec` fields).

- [ ] **Step 1: Write the failing tests**

Append to `internal/kuma/fake_test.go`:

```go
func TestFakeClient_Notifications(t *testing.T) {
	c := NewFakeClient()
	c.NotificationIDs = map[string]int64{"slack-prod": 1, "email-oncall": 2}

	got, err := c.Notifications(context.Background())
	if err != nil {
		t.Fatalf("Notifications: %v", err)
	}
	if got["slack-prod"] != 1 || got["email-oncall"] != 2 {
		t.Errorf("Notifications = %v, want {slack-prod:1 email-oncall:2}", got)
	}
}

func TestFakeClient_FindGroup(t *testing.T) {
	c := NewFakeClient()
	c.GroupIDs = map[string]int64{"prod-services": 5}

	id, found, err := c.FindGroup(context.Background(), "prod-services")
	if err != nil || !found || id != 5 {
		t.Errorf("FindGroup(prod-services) = (%d, %v, %v), want (5, true, nil)", id, found, err)
	}

	_, found, err = c.FindGroup(context.Background(), "nonexistent")
	if err != nil || found {
		t.Errorf("FindGroup(nonexistent) = (_, %v, %v), want (false, nil)", found, err)
	}
}
```

Append to `internal/kuma/translate_test.go`:

```go
func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_ReferencedFields(t *testing.T) {
	groupID := int64(5)
	proxyID := int64(7)
	spec := MonitorSpec{
		Type: TypeHTTP, Name: "web",
		NotificationIDs: []int64{1, 2},
		GroupID:         &groupID,
		ProxyID:         &proxyID,
		HTTP:            &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}},
	}

	mon, err := ToBremlMonitor(9, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_ReferencedFields(t *testing.T) {
	base := MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}}

	changedNotifications := base
	changedNotifications.NotificationIDs = []int64{1}
	if Equivalent(base, changedNotifications) {
		t.Error("Equivalent(base, changedNotifications) = true, want false")
	}

	groupID := int64(5)
	changedGroup := base
	changedGroup.GroupID = &groupID
	if Equivalent(base, changedGroup) {
		t.Error("Equivalent(base, changedGroup) = true, want false")
	}

	proxyID := int64(7)
	changedProxy := base
	changedProxy.ProxyID = &proxyID
	if Equivalent(base, changedProxy) {
		t.Error("Equivalent(base, changedProxy) = true, want false")
	}
}

func TestEquivalent_NotificationIDsOrderIndependent(t *testing.T) {
	a := MonitorSpec{Type: TypeHTTP, Name: "web", NotificationIDs: []int64{1, 2}, HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}}
	b := MonitorSpec{Type: TypeHTTP, Name: "web", NotificationIDs: []int64{2, 1}, HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}}
	if !Equivalent(a, b) {
		t.Error("Equivalent(a, b) = false, want true — NotificationIDs order must not matter")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kuma/... -run 'Notifications|FindGroup|ReferencedFields|NotificationIDsOrderIndependent' -v`
Expected: compile errors (`FakeClient` has no `NotificationIDs`/`GroupIDs` fields or `Notifications`/`FindGroup` methods; `MonitorSpec` has no `NotificationIDs`/`GroupID`/`ProxyID` fields).

- [ ] **Step 3: Extend `Client` interface and `realClient`**

In `internal/kuma/client.go`, add to the `Client` interface (after `ExistingSpecs`):

```go
	// Notifications returns every existing notification channel's name -> ID.
	Notifications(ctx context.Context) (map[string]int64, error)
	// FindGroup returns the ID of the group-type monitor named name, or
	// found=false if no such group exists. Groups must be created directly
	// in Kuma first — the operator never creates one automatically.
	FindGroup(ctx context.Context, name string) (id int64, found bool, err error)
```

Add to `realClient` (after `ExistingSpecs`):

```go
func (r *realClient) Notifications(ctx context.Context) (map[string]int64, error) {
	notifs := r.inner.GetNotifications(ctx)
	out := make(map[string]int64, len(notifs))
	for _, n := range notifs {
		out[n.Name] = n.ID
	}
	return out, nil
}

func (r *realClient) FindGroup(ctx context.Context, name string) (int64, bool, error) {
	monitors, err := r.inner.GetMonitors(ctx)
	if err != nil {
		return 0, false, err
	}
	for _, m := range monitors {
		if m.Type() == "group" && m.Name == name {
			return m.GetID(), true, nil
		}
	}
	return 0, false, nil
}
```

- [ ] **Step 4: Extend `FakeClient`**

In `internal/kuma/fake.go`, add fields to the `FakeClient` struct:

```go
	// NotificationIDs and GroupIDs are pre-seeded by tests to simulate
	// existing Kuma-side notification channels and groups.
	NotificationIDs map[string]int64
	GroupIDs        map[string]int64

	NotificationsErr error
	FindGroupErr     error
```

Add methods:

```go
func (f *FakeClient) Notifications(_ context.Context) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.NotificationsErr != nil {
		return nil, f.NotificationsErr
	}
	out := make(map[string]int64, len(f.NotificationIDs))
	for k, v := range f.NotificationIDs {
		out[k] = v
	}
	return out, nil
}

func (f *FakeClient) FindGroup(_ context.Context, name string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FindGroupErr != nil {
		return 0, false, f.FindGroupErr
	}
	id, ok := f.GroupIDs[name]
	return id, ok, nil
}
```

- [ ] **Step 5: Extend `kuma.MonitorSpec` and wire `translate.go`**

In `internal/kuma/spec.go`, add to `MonitorSpec` (after `UpsideDown`):

```go
	// NotificationIDs is the set of Kuma notification channel IDs to alert.
	// Order does not matter; Equivalent compares it as a set.
	NotificationIDs []int64
	// GroupID is the parent group monitor's ID, or nil for no group.
	GroupID *int64
	// ProxyID is the Kuma proxy's ID to route checks through, or nil for none.
	ProxyID *int64
```

In `internal/kuma/translate.go`'s `ToBremlMonitor`, extend the `base := bremlmonitor.Base{...}` construction:

```go
	base := bremlmonitor.Base{
		ID:              id,
		Name:            spec.Name,
		Interval:        spec.Interval,
		RetryInterval:   spec.RetryInterval,
		MaxRetries:      spec.MaxRetries,
		ResendInterval:  spec.ResendInterval,
		UpsideDown:      spec.UpsideDown,
		NotificationIDs: spec.NotificationIDs,
		Parent:          spec.GroupID,
		ProxyID:         spec.ProxyID,
		IsActive:        true,
	}
```

In `FromBremlMonitor`, extend the initial `spec := MonitorSpec{...}`:

```go
	spec := MonitorSpec{
		Name:            base.Name,
		Interval:        base.Interval,
		RetryInterval:   base.RetryInterval,
		MaxRetries:      base.MaxRetries,
		ResendInterval:  base.ResendInterval,
		UpsideDown:      base.UpsideDown,
		NotificationIDs: base.NotificationIDs,
		GroupID:         base.Parent,
		ProxyID:         base.ProxyID,
	}
```

In `Equivalent`, extend the top-level comparison — `NotificationIDs` needs an order-independent set comparison, `GroupID`/`ProxyID` are plain `*int64` pointer-value comparisons (both nil, or both non-nil with the same value):

```go
	d := normalizeSpec(desired)
	live = normalizeSpec(live)
	if d.Type != live.Type || d.Name != live.Name || d.Interval != live.Interval ||
		d.RetryInterval != live.RetryInterval || d.MaxRetries != live.MaxRetries ||
		d.Description != live.Description || d.ResendInterval != live.ResendInterval ||
		d.UpsideDown != live.UpsideDown ||
		!equalInt64Sets(d.NotificationIDs, live.NotificationIDs) ||
		!equalInt64Ptr(d.GroupID, live.GroupID) ||
		!equalInt64Ptr(d.ProxyID, live.ProxyID) {
		return false
	}
```

Add two small helpers near `orDefault`:

```go
func equalInt64Ptr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalInt64Sets(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[int64]int, len(a))
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/kuma/... -v`
Expected: PASS, all tests including pre-existing ones.

- [ ] **Step 7: Commit**

```bash
git add internal/kuma/client.go internal/kuma/fake.go internal/kuma/spec.go internal/kuma/translate.go internal/kuma/fake_test.go internal/kuma/translate_test.go
git commit -m "feat: add Notifications/FindGroup to kuma.Client and resolved-ID fields to MonitorSpec"
```

---

### Task 2: CRD and annotation support for notifications, group, proxy

**Files:**
- Modify: `api/v1alpha1/monitor_types.go`
- Modify: `internal/annotations/annotations.go`
- Test: `internal/annotations/annotations_test.go`

**Interfaces:**
- Produces: CRD `MonitorSpec` gains `Notifications []string`, `Group string`, `Proxy int64` (raw names/ID, not yet resolved). `annotations.Overrides` gains the same three fields.
- Consumed by: Task 3.

- [ ] **Step 1: Write the failing annotation tests**

Append to `internal/annotations/annotations_test.go`:

```go
func TestParseOverrides_Phase2ReferenceFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		Notifications: "slack-prod, email-oncall",
		Group:         "prod-services",
		Proxy:         "7",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := Overrides{
		Notifications: []string{"slack-prod", "email-oncall"},
		Group:         "prod-services",
		Proxy:         7,
	}
	if !reflect.DeepEqual(ov, want) {
		t.Errorf("ParseOverrides = %+v, want %+v", ov, want)
	}
}

func TestParseOverrides_InvalidProxy(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{Proxy: "not-a-number"}); err == nil {
		t.Fatal("expected error for non-integer proxy, got nil")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/annotations/... -run 'Phase2ReferenceFields|InvalidProxy' -v`
Expected: compile error (`Notifications`/`Group`/`Proxy` constants and `Overrides` fields don't exist yet).

- [ ] **Step 3: Add annotation constants and `Overrides` fields**

In `internal/annotations/annotations.go`, extend the `const` block:

```go
	Tags          = "uptime-kuma.io/tags"
	Notifications = "uptime-kuma.io/notifications"
	Proxy         = "uptime-kuma.io/proxy"
	Group         = "uptime-kuma.io/group"
```

(`Tags` is declared here now — used starting in Task 5, not this task — so the constant block gains all four Phase 2 common-field annotations together; do not wire `Tags` parsing yet, that's Task 5.)

Extend `Overrides`:

```go
	Notifications []string
	Proxy         int64
	Group         string
```

- [ ] **Step 4: Extend `ParseOverrides`**

Insert before the final `return o, nil`:

```go
	if v := ann[Notifications]; v != "" {
		for _, name := range strings.Split(v, ",") {
			o.Notifications = append(o.Notifications, strings.TrimSpace(name))
		}
	}
	o.Group = ann[Group]
	if o.Proxy, err = parseIntAnnotation(ann, Proxy); err != nil {
		return Overrides{}, err
	}
```

- [ ] **Step 5: Extend the CRD `MonitorSpec`**

In `api/v1alpha1/monitor_types.go`, add to `MonitorSpec` (after `UpsideDown`):

```go
	// Notifications lists notification channel names to alert on this
	// monitor. Each must already exist in Kuma — an unresolvable name
	// fails the reconcile.
	Notifications []string `json:"notifications,omitempty"`
	// Group is the name of an existing parent group monitor. Must already
	// exist in Kuma — an unresolvable name fails the reconcile.
	Group string `json:"group,omitempty"`
	// Proxy is the numeric ID of an existing Kuma proxy to route checks
	// through. Kuma proxies have no name field, so this is ID-based.
	Proxy int64 `json:"proxy,omitempty"`
```

- [ ] **Step 6: Regenerate and verify**

Run: `make generate manifests` then `git diff --stat` to confirm `zz_generated.deepcopy.go` (the new `[]string` field needs a deepcopy statement this time, unlike Phase 1's scalar-only fields) and both CRD YAML files changed. Run `go build ./...`.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/annotations/... -v` and `go build ./...`.
Expected: PASS / clean.

- [ ] **Step 8: Commit**

```bash
git add api/v1alpha1/monitor_types.go api/v1alpha1/zz_generated.deepcopy.go config/crd/bases/uptime-kuma.io_monitors.yaml charts/uptime-kuma-operator/crds/uptime-kuma.io_monitors.yaml internal/annotations/annotations.go internal/annotations/annotations_test.go
git commit -m "feat: add notifications/group/proxy annotations and CRD fields"
```

---

### Task 3: Resolve and wire notifications/group/proxy in all three reconcilers

**Files:**
- Modify: `internal/controller/sync.go`
- Modify: `internal/controller/convert.go`
- Modify: `internal/controller/monitor_controller.go`
- Modify: `internal/derive/httpspec.go`
- Modify: `internal/controller/ingress_controller.go`
- Modify: `internal/controller/httproute_controller.go`
- Test: `internal/controller/sync_test.go`, `internal/controller/monitor_controller_test.go`, `internal/controller/ingress_controller_test.go`

**Interfaces:**
- Consumes: Task 1's `kuma.Client.Notifications`/`FindGroup` and `MonitorSpec.NotificationIDs`/`GroupID`/`ProxyID`; Task 2's CRD/annotation fields.
- Produces: `resolveReferences(ctx, kc kuma.Client, notificationNames []string, groupName string) (notificationIDs []int64, groupID *int64, err error)` in `sync.go`, called once per reconcile by each of the three reconcilers.

- [ ] **Step 1: Write the failing tests**

Append to `internal/controller/sync_test.go`:

```go
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
```

Append to `internal/controller/monitor_controller_test.go` a reconciler-level test:

```go
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
```

Append to `internal/controller/ingress_controller_test.go` a reconciler-level test proving the annotation path resolves too:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `KUBEBUILDER_ASSETS="$(bin/setup-envtest use -p path 1.31.x)" go test ./internal/controller/... -run 'ResolveReferences|ResolvesNotificationsAndGroup|UnresolvableNotificationFailsReconcile' -v`
Expected: compile error (`resolveReferences` doesn't exist) or, once it compiles, failures (fields not populated).

- [ ] **Step 3: Add `resolveReferences` to `sync.go`**

```go
// resolveReferences resolves notification channel names and a group name
// into their Kuma IDs. A name that doesn't exist in Kuma is a hard error —
// callers should treat it exactly like any other sync failure (record a
// SyncFailed Event, return the error for the usual requeue-with-backoff).
// (nil, nil, nil) is returned when there's nothing to resolve, so callers
// can skip the Kuma round-trip entirely for the common case of no
// notifications/group configured.
func resolveReferences(ctx context.Context, kc kuma.Client, notificationNames []string, groupName string) ([]int64, *int64, error) {
	var notificationIDs []int64
	if len(notificationNames) > 0 {
		known, err := kc.Notifications(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve notifications: %w", err)
		}
		notificationIDs = make([]int64, 0, len(notificationNames))
		for _, name := range notificationNames {
			id, ok := known[name]
			if !ok {
				return nil, nil, fmt.Errorf("notification channel %q not found in Kuma", name)
			}
			notificationIDs = append(notificationIDs, id)
		}
	}

	var groupID *int64
	if groupName != "" {
		id, found, err := kc.FindGroup(ctx, groupName)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve group %q: %w", groupName, err)
		}
		if !found {
			return nil, nil, fmt.Errorf("group %q not found in Kuma", groupName)
		}
		groupID = &id
	}

	return notificationIDs, groupID, nil
}
```

`sync.go` already imports `fmt`? Check the current imports — if not, add it.

- [ ] **Step 4: Wire into `toKumaSpec` (Monitor CRD path) and `monitor_controller.go`**

`toKumaSpec` (`internal/controller/convert.go`) cannot call `resolveReferences` itself — it's a pure CRD→kuma.MonitorSpec converter with no `kuma.Client`. Instead, add `ProxyID` directly in `toKumaSpec` (no resolution needed, exactly like `Description`):

```go
	out := kuma.MonitorSpec{
		Type:           kuma.MonitorType(spec.Type),
		Name:           spec.Name,
		Interval:       spec.Interval,
		RetryInterval:  spec.RetryInterval,
		MaxRetries:     spec.Retries,
		Description:    spec.Description,
		ResendInterval: spec.ResendInterval,
		UpsideDown:     spec.UpsideDown,
	}
	if spec.Proxy != 0 {
		out.ProxyID = &spec.Proxy
	}
```

(`&spec.Proxy` here is safe — `spec` is `toKumaSpec`'s own parameter, a fresh copy per call, not aliased with any caller-held struct, unlike the Phase-1 aliasing issue that only applied to `ToBremlMonitor`'s callee-side handling of the caller's `kuma.MonitorSpec`.)

In `internal/controller/monitor_controller.go`, after computing `desiredSpec := toKumaSpec(mon.Spec)`, resolve notifications/group and merge them in:

```go
	desiredSpec := toKumaSpec(mon.Spec)
	notificationIDs, groupID, err := resolveReferences(ctx, r.Kuma, mon.Spec.Notifications, mon.Spec.Group)
	if err != nil {
		recordSyncFailure(r.Recorder, mon, err)
		return ctrl.Result{}, err
	}
	desiredSpec.NotificationIDs = notificationIDs
	desiredSpec.GroupID = groupID
```

Place this immediately after the existing `desiredSpec := toKumaSpec(mon.Spec)` line, before the `if existingID != 0 && ...` drift-check block, so the resolved spec is what both the drift-check `Equivalent` call and the eventual `Upsert` use. Note `MonitorReconciler` doesn't currently have a `Recorder` field — check `internal/controller/monitor_controller.go`'s struct; if it lacks one, add `Recorder record.EventRecorder` to the `MonitorReconciler` struct (matching `IngressReconciler`/`HTTPRouteReconciler`'s existing field) and wire it in `cmd/main.go`'s `MonitorReconciler{...}` construction, since `recordSyncFailure` needs it and this is the first Monitor-CRD-path error that should surface as a Kubernetes Event rather than just a log line.

- [ ] **Step 5: Wire `ProxyID` into `derive/httpspec.go`, and resolve notifications/group in the Ingress/HTTPRoute reconcilers**

In `internal/derive/httpspec.go`, add `ProxyID` to the returned `kuma.MonitorSpec` (no resolution needed, same reasoning as Step 4):

```go
	spec := kuma.MonitorSpec{
		Type:           kuma.TypeHTTP,
		Name:           name,
		Interval:       ov.Interval,
		RetryInterval:  ov.RetryInterval,
		MaxRetries:     ov.MaxRetries,
		Description:    ov.Description,
		ResendInterval: ov.ResendInterval,
		UpsideDown:     ov.UpsideDown,
		HTTP: &kuma.HTTPSpec{
			URL:                      fmt.Sprintf("%s://%s%s", scheme, host, path),
			AcceptedStatusCodes:      ov.AcceptedStatusCodes,
			Timeout:                  ov.Timeout,
			MaxRedirects:             int(ov.MaxRedirects),
			IgnoreTLS:                ov.IgnoreTLS,
			CacheBust:                ov.CacheBust,
			ExpiryNotification:       ov.ExpiryNotification,
			DomainExpiryNotification: ov.DomainExpiryNotification,
			Headers:                  ov.Headers,
			Body:                     ov.Body,
		},
	}
	if ov.Proxy != 0 {
		spec.ProxyID = &ov.Proxy
	}
	return spec
```

(Same aliasing note as Step 4: `ov` is `httpMonitorSpec`'s own parameter, passed by value from each call site, so `&ov.Proxy` is safe.)

In `internal/controller/ingress_controller.go`, after `desired := derive.IngressMonitors(ing, ov)` and its hash computation but *before* the hash-match/hash-mismatch branches (both branches need the resolved IDs — one host's Ingress and a five-host Ingress both resolve notifications/group exactly once, then apply to every derived host):

```go
	desired := derive.IngressMonitors(ing, ov)
	notificationIDs, groupID, err := resolveReferences(ctx, r.Kuma, ov.Notifications, ov.Group)
	if err != nil {
		recordSyncFailure(r.Recorder, ing, err)
		return ctrl.Result{}, err
	}
	for i := range desired {
		desired[i].Spec.NotificationIDs = notificationIDs
		desired[i].Spec.GroupID = groupID
	}
	hash, err := desiredHash(desired)
```

(Insert this between the existing `desired := derive.IngressMonitors(ing, ov)` line and the existing `hash, err := desiredHash(desired)` line — resolving before hashing means a notification/group change is itself hash-visible, so it correctly triggers a full sync rather than being silently missed by the drift-check-only path.)

Apply the identical pattern to `internal/controller/httproute_controller.go` (same variable names: `desired`, `ov`, `r.Kuma`, `r.Recorder`).

- [ ] **Step 6: Run the tests to verify they pass**

Run: `make test`
Expected: PASS, full suite green.

- [ ] **Step 7: Commit**

```bash
git add internal/controller/sync.go internal/controller/convert.go internal/controller/monitor_controller.go internal/derive/httpspec.go internal/controller/ingress_controller.go internal/controller/httproute_controller.go internal/controller/sync_test.go internal/controller/monitor_controller_test.go internal/controller/ingress_controller_test.go cmd/main.go
git commit -m "feat: resolve and sync notifications/group/proxy in all three reconcilers"
```

---

### Task 4: kuma package — Tags client methods

**Files:**
- Modify: `internal/kuma/client.go`
- Modify: `internal/kuma/fake.go`
- Test: `internal/kuma/fake_test.go`

**Interfaces:**
- Produces: `Client` gains `Tags(ctx) (map[string]int64, error)`, `CreateTag(ctx, name string) (int64, error)`, `SetMonitorTags(ctx, monitorID int64, tagIDs []int64) error`.
- Consumed by: Task 7 (reconciler tag-reconciliation step).

- [ ] **Step 1: Write the failing tests**

Append to `internal/kuma/fake_test.go`:

```go
func TestFakeClient_TagsAndCreateTag(t *testing.T) {
	c := NewFakeClient()
	c.TagIDs = map[string]int64{"env-prod": 1}

	got, err := c.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if got["env-prod"] != 1 {
		t.Errorf("Tags = %v, want {env-prod:1}", got)
	}

	newID, err := c.CreateTag(context.Background(), "env-staging")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if newID == 0 {
		t.Fatal("CreateTag returned id 0")
	}
	got, err = c.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags after create: %v", err)
	}
	if got["env-staging"] != newID {
		t.Errorf("Tags after create = %v, want env-staging:%d", got, newID)
	}
}

func TestFakeClient_SetMonitorTags(t *testing.T) {
	c := NewFakeClient()
	ctx := context.Background()
	id, _ := c.Upsert(ctx, 0, MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{URL: "https://a"}})

	if err := c.SetMonitorTags(ctx, id, []int64{1, 2}); err != nil {
		t.Fatalf("SetMonitorTags: %v", err)
	}
	got := c.MonitorTags[id]
	if len(got) != 2 {
		t.Fatalf("MonitorTags[id] = %v, want 2 entries", got)
	}

	// Reconciling to a smaller set must remove the dropped tag, not just add.
	if err := c.SetMonitorTags(ctx, id, []int64{2}); err != nil {
		t.Fatalf("SetMonitorTags (shrink): %v", err)
	}
	got = c.MonitorTags[id]
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("MonitorTags[id] after shrink = %v, want [2]", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kuma/... -run 'TagsAndCreateTag|SetMonitorTags' -v`
Expected: compile error.

- [ ] **Step 3: Extend `Client` interface and `realClient`**

In `internal/kuma/client.go`, add to the `Client` interface:

```go
	// Tags returns every existing tag's name -> ID.
	Tags(ctx context.Context) (map[string]int64, error)
	// CreateTag creates a new tag with a sensible default color and
	// returns its ID.
	CreateTag(ctx context.Context, name string) (int64, error)
	// SetMonitorTags reconciles monitorID's tag associations to be exactly
	// tagIDs, adding missing ones and removing any not in the set. An
	// unchanged tag set costs one cache read and zero mutating calls.
	SetMonitorTags(ctx context.Context, monitorID int64, tagIDs []int64) error
```

Add to `realClient`. `"#3F51B5"` is used as the default tag color below (Kuma's own UI default at the time this plan was written). Before committing this task, verify it against a real Kuma instance if one is available (same docker-compose approach used throughout this project: create a tag via `CreateTag`, then check via the Kuma UI or `GetTags` what color a tag created with `""` gets by default in the *UI's* "add tag" flow, and use that value instead if it differs). Note whatever you find in the commit message; this is a cosmetic default only — getting it exactly right is not correctness-critical, unlike the Task 7 empirical check on credential echo-back:

```go
const defaultTagColor = "#3F51B5"

func (r *realClient) Tags(ctx context.Context) (map[string]int64, error) {
	tags, err := r.inner.GetTags(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(tags))
	for _, t := range tags {
		out[t.Name] = t.ID
	}
	return out, nil
}

func (r *realClient) CreateTag(ctx context.Context, name string) (int64, error) {
	return r.inner.CreateTag(ctx, tag.Tag{Name: name, Color: defaultTagColor})
}

func (r *realClient) SetMonitorTags(ctx context.Context, monitorID int64, tagIDs []int64) error {
	current, err := r.inner.GetMonitorTags(ctx, monitorID)
	if err != nil {
		return fmt.Errorf("set monitor tags: get current: %w", err)
	}
	currentIDs := make(map[int64]bool, len(current))
	for _, t := range current {
		currentIDs[t.TagID] = true
	}
	wantIDs := make(map[int64]bool, len(tagIDs))
	for _, id := range tagIDs {
		wantIDs[id] = true
	}

	for id := range wantIDs {
		if !currentIDs[id] {
			if _, err := r.inner.AddMonitorTag(ctx, id, monitorID, ""); err != nil {
				return fmt.Errorf("set monitor tags: add tag %d: %w", id, err)
			}
		}
	}
	for id := range currentIDs {
		if !wantIDs[id] {
			if err := r.inner.DeleteMonitorTag(ctx, id, monitorID); err != nil {
				return fmt.Errorf("set monitor tags: remove tag %d: %w", id, err)
			}
		}
	}
	return nil
}
```

Add the import `"github.com/breml/go-uptime-kuma-client/tag"` to `internal/kuma/client.go`.

- [ ] **Step 4: Extend `FakeClient`**

In `internal/kuma/fake.go`, add fields:

```go
	// TagIDs simulates existing Kuma-side tags (name -> ID), pre-seedable
	// by tests. MonitorTags simulates each monitor's current tag set.
	TagIDs      map[string]int64
	MonitorTags map[int64][]int64

	nextTagID int64

	TagsErr           error
	CreateTagErr      error
	SetMonitorTagsErr error
```

Update `NewFakeClient` to initialize the new maps:

```go
func NewFakeClient() *FakeClient {
	return &FakeClient{Monitors: map[int64]MonitorSpec{}, TagIDs: map[string]int64{}, MonitorTags: map[int64][]int64{}}
}
```

Add methods:

```go
func (f *FakeClient) Tags(_ context.Context) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.TagsErr != nil {
		return nil, f.TagsErr
	}
	out := make(map[string]int64, len(f.TagIDs))
	for k, v := range f.TagIDs {
		out[k] = v
	}
	return out, nil
}

func (f *FakeClient) CreateTag(_ context.Context, name string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateTagErr != nil {
		return 0, f.CreateTagErr
	}
	f.nextTagID++
	f.TagIDs[name] = f.nextTagID
	return f.nextTagID, nil
}

func (f *FakeClient) SetMonitorTags(_ context.Context, monitorID int64, tagIDs []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SetMonitorTagsErr != nil {
		return f.SetMonitorTagsErr
	}
	set := make([]int64, len(tagIDs))
	copy(set, tagIDs)
	f.MonitorTags[monitorID] = set
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/kuma/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/kuma/client.go internal/kuma/fake.go internal/kuma/fake_test.go
git commit -m "feat: add Tags/CreateTag/SetMonitorTags to kuma.Client"
```

---

### Task 5: CRD and annotation support for tags

**Files:**
- Modify: `api/v1alpha1/monitor_types.go`
- Modify: `internal/annotations/annotations.go`
- Test: `internal/annotations/annotations_test.go`

**Interfaces:**
- Produces: CRD `MonitorSpec.Tags []string`; `annotations.Overrides.Tags []string`. The `Tags` annotation constant already exists from Task 2's Step 3 — this task wires its parsing.

- [ ] **Step 1: Write the failing test**

Append to `internal/annotations/annotations_test.go`:

```go
func TestParseOverrides_Tags(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{Tags: "env-prod, team-platform"})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	if len(ov.Tags) != 2 || ov.Tags[0] != "env-prod" || ov.Tags[1] != "team-platform" {
		t.Errorf("Tags = %v, want [env-prod team-platform]", ov.Tags)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/annotations/... -run TestParseOverrides_Tags -v`
Expected: compile error (`Overrides.Tags` doesn't exist).

- [ ] **Step 3: Add `Overrides.Tags` and parse it**

In `internal/annotations/annotations.go`, add to `Overrides`:

```go
	Tags []string
```

In `ParseOverrides`, insert before the final `return o, nil`:

```go
	if v := ann[Tags]; v != "" {
		for _, name := range strings.Split(v, ",") {
			o.Tags = append(o.Tags, strings.TrimSpace(name))
		}
	}
```

- [ ] **Step 4: Add the CRD field**

In `api/v1alpha1/monitor_types.go`, add to `MonitorSpec` (alongside `Notifications`/`Group`/`Proxy`):

```go
	// Tags lists tag names to apply to this monitor. A tag that doesn't
	// already exist in Kuma is created automatically.
	Tags []string `json:"tags,omitempty"`
```

- [ ] **Step 5: Regenerate and verify**

Run: `make generate manifests`, confirm `zz_generated.deepcopy.go` and both CRD YAMLs changed, `go build ./...`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/annotations/... -v` and `go build ./...`.

- [ ] **Step 7: Commit**

```bash
git add api/v1alpha1/monitor_types.go api/v1alpha1/zz_generated.deepcopy.go config/crd/bases/uptime-kuma.io_monitors.yaml charts/uptime-kuma-operator/crds/uptime-kuma.io_monitors.yaml internal/annotations/annotations.go internal/annotations/annotations_test.go
git commit -m "feat: add tags annotation and CRD field"
```

---

### Task 6: Tag reconciliation in all three reconcilers

**Files:**
- Modify: `internal/controller/sync.go`
- Modify: `internal/controller/monitor_controller.go`
- Modify: `internal/controller/ingress_controller.go`
- Modify: `internal/controller/httproute_controller.go`
- Test: `internal/controller/sync_test.go`, `internal/controller/monitor_controller_test.go`, `internal/controller/ingress_controller_test.go`

**Interfaces:**
- Consumes: Task 4's `kuma.Client.Tags`/`CreateTag`/`SetMonitorTags`, Task 5's CRD/annotation `Tags` fields.
- Produces: `syncTags(ctx, kc kuma.Client, monitorID int64, tagNames []string) error` in `sync.go`, called after every successful `Upsert` (both the "just created/updated" path and the "nothing changed, only checking drift" path — tags can drift out-of-band on their own, independent of every other field).

- [ ] **Step 1: Write the failing tests**

Append to `internal/controller/sync_test.go`:

```go
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

func TestSyncTags_NoTagsIsNoop(t *testing.T) {
	ctx := context.Background()
	fake := kuma.NewFakeClient()
	id, _ := fake.Upsert(ctx, 0, kuma.MonitorSpec{Type: kuma.TypeHTTP, Name: "web", HTTP: &kuma.HTTPSpec{URL: "https://a"}})

	if err := syncTags(ctx, fake, id, nil); err != nil {
		t.Fatalf("syncTags: %v", err)
	}
	if len(fake.MonitorTags[id]) != 0 {
		t.Errorf("MonitorTags[id] = %v, want empty", fake.MonitorTags[id])
	}
}
```

Append to `internal/controller/monitor_controller_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `KUBEBUILDER_ASSETS="$(bin/setup-envtest use -p path 1.31.x)" go test ./internal/controller/... -run 'SyncTags|SyncsTags' -v`
Expected: compile error (`syncTags` doesn't exist).

- [ ] **Step 3: Add `syncTags` to `sync.go`**

```go
// syncTags reconciles monitorID's Kuma tag associations to match tagNames
// exactly, creating any tag that doesn't already exist. This runs entirely
// outside Equivalent/Upsert: Kuma manages monitor-tag associations via
// separate calls, not as part of a monitor's own create/update payload, so
// it needs its own step after every successful sync (including the
// "nothing else changed" drift-check path, since tags can drift out of
// band on their own).
func syncTags(ctx context.Context, kc kuma.Client, monitorID int64, tagNames []string) error {
	if len(tagNames) == 0 {
		return kc.SetMonitorTags(ctx, monitorID, nil)
	}
	known, err := kc.Tags(ctx)
	if err != nil {
		return fmt.Errorf("sync tags: list existing: %w", err)
	}
	ids := make([]int64, 0, len(tagNames))
	for _, name := range tagNames {
		id, ok := known[name]
		if !ok {
			id, err = kc.CreateTag(ctx, name)
			if err != nil {
				return fmt.Errorf("sync tags: create %q: %w", name, err)
			}
		}
		ids = append(ids, id)
	}
	return kc.SetMonitorTags(ctx, monitorID, ids)
}
```

- [ ] **Step 4: Call `syncTags` from `monitor_controller.go`**

After the successful `Upsert` and the "nothing changed" drift-check-passed return (both places a monitor's tags might need reconciling), call `syncTags`. Concretely: right before each `return ctrl.Result{RequeueAfter: r.DriftCheckInterval}, nil` success path in `Reconcile` — there are two: the "Kuma monitor matches desired state, nothing to do" early return inside the drift-check block, and the final return after `updateStatus`. Add the call before both, using `existingID` (drift-check path) or `newID` (post-Upsert path) as the monitor ID:

In the drift-check "matches" branch:
```go
			if kuma.Equivalent(desiredSpec, live) {
				if err := syncTags(ctx, r.Kuma, existingID, mon.Spec.Tags); err != nil {
					recordSyncFailure(r.Recorder, mon, err)
					return ctrl.Result{}, err
				}
				log.V(1).Info("Kuma monitor matches desired state, nothing to do", "monitorID", existingID)
				return ctrl.Result{RequeueAfter: r.DriftCheckInterval}, nil
			}
```

After the final `updateStatus` call, before the function's last `return`:
```go
	if err := syncTags(ctx, r.Kuma, newID, mon.Spec.Tags); err != nil {
		recordSyncFailure(r.Recorder, mon, err)
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: r.DriftCheckInterval}, nil
```

- [ ] **Step 5: Call `syncTags` from `ingress_controller.go` and `httproute_controller.go`**

Both reconcilers work in terms of a *set* of hosts, each with its own monitor ID, all sharing the *same* `ov.Tags` (an Ingress-level or HTTPRoute-level annotation applies to every host it derives). After the existing ID map is finalized on each success path — both the "matches, nothing to do" `specsMatch` branch and the post-`syncMonitors`/`reconcileDrift` branches — loop over the resulting host→ID map and call `syncTags` for each:

```go
	for _, idStr := range existingIDs { // or newIDs, depending on which branch
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue // already logged/handled by the surrounding sync logic
		}
		if err := syncTags(ctx, r.Kuma, id, ov.Tags); err != nil {
			recordSyncFailure(r.Recorder, ing, err) // or route, in httproute_controller.go
			return ctrl.Result{}, err
		}
	}
```

Place this immediately before each of `IngressReconciler.Reconcile`'s three success-path `return ctrl.Result{RequeueAfter: ...}` statements (the `specsMatch` early return, the `reconcileDrift` return, and the final `syncMonitors` return), using `existingIDs` for the first and `newIDs` for the other two. `strconv` is already imported in this file (used elsewhere for host→ID parsing) — verify and add if not. Apply the identical pattern to `httproute_controller.go`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `make test`
Expected: PASS, full suite green.

- [ ] **Step 7: Commit**

```bash
git add internal/controller/sync.go internal/controller/monitor_controller.go internal/controller/ingress_controller.go internal/controller/httproute_controller.go internal/controller/sync_test.go internal/controller/monitor_controller_test.go internal/controller/ingress_controller_test.go
git commit -m "feat: reconcile tags on every successful sync path"
```

---

### Task 7: kuma package — HTTP auth and Gamedig token fields

**Files:**
- Modify: `internal/kuma/spec.go`
- Modify: `internal/kuma/translate.go`
- Test: `internal/kuma/translate_test.go`

**Interfaces:**
- Produces: `HTTPSpec` gains `AuthMethod`, `BasicAuthUsername`, `BasicAuthPassword`, `BearerToken`, `OAuthClientID`, `OAuthClientSecret`, `OAuthTokenURL`, `OAuthScopes`, `OAuthAudience` (all plain, already-resolved strings — Kubernetes Secret resolution happens one layer up, in the reconciler, per the Global Constraints). `GamedigSpec` gains `Token string`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/kuma/translate_test.go`:

```go
func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_HTTPAuthFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeHTTP, Name: "web",
		HTTP: &HTTPSpec{
			URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"},
			AuthMethod: "basic", BasicAuthUsername: "svc-account", BasicAuthPassword: "hunter2",
		},
	}

	mon, err := ToBremlMonitor(9, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_GamedigToken(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeGamedig, Name: "game-server",
		Gamedig: &GamedigSpec{Host: "game.example.com", Port: 27015, Game: "csgo", Token: "s3cr3t"},
	}

	mon, err := ToBremlMonitor(9, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_HTTPAuthFields(t *testing.T) {
	base := MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}, AuthMethod: "bearer", BearerToken: "abc"}}
	changedToken := base
	changedToken.HTTP = &HTTPSpec{URL: base.HTTP.URL, Method: base.HTTP.Method, AcceptedStatusCodes: base.HTTP.AcceptedStatusCodes, AuthMethod: "bearer", BearerToken: "xyz"}
	if Equivalent(base, changedToken) {
		t.Error("Equivalent(base, changedToken) = true, want false")
	}
}
```

**Important empirical caveat to resolve before/while implementing this task**: verify against a real Kuma 2.x instance (same docker-compose approach used throughout this project) whether `GetMonitor`/`GetMonitorList` actually echoes back `bearer_token`/`basic_auth_pass`/`oauth_client_secret` in plaintext, or masks/omits them. If Kuma masks these fields server-side, `FromBremlMonitor` will never see the real value, `Equivalent` will see a permanent mismatch against any non-empty desired secret, and the reconciler will re-`Upsert` (reintroducing the redundant-`editMonitor` bug) on every drift check. If masking is confirmed, this task's `Equivalent` comparison for these three fields must be changed to skip them entirely (accept that credentials are write-only and can't be drift-detected) — do this only if the empirical check proves it necessary, and document the finding in the commit message either way.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kuma/... -run 'HTTPAuthFields|GamedigToken' -v`
Expected: compile error.

- [ ] **Step 3: Extend `kuma.HTTPSpec` and `kuma.GamedigSpec`**

In `internal/kuma/spec.go`, add to `HTTPSpec`:

```go
	// AuthMethod selects the HTTP auth scheme: "" (none), "basic", "bearer",
	// or "oauth2-cc" (OAuth2 Client Credentials). NTLM and mTLS are not
	// supported.
	AuthMethod string
	// BasicAuthUsername is not secret; only the password is.
	BasicAuthUsername string
	// BasicAuthPassword is an already-resolved secret value — see
	// api/v1alpha1's SecretKeySelector fields for where it's read from.
	BasicAuthPassword string
	// BearerToken is an already-resolved secret value.
	BearerToken string
	// OAuthClientID is not secret.
	OAuthClientID string
	// OAuthClientSecret is an already-resolved secret value.
	OAuthClientSecret string
	OAuthTokenURL     string
	OAuthScopes       string
	OAuthAudience     string
```

Add to `GamedigSpec`:

```go
	// Token is an already-resolved secret value, or "" for none.
	Token string
```

- [ ] **Step 4: Wire `translate.go`**

In `ToBremlMonitor`'s `TypeHTTP` case, extend the returned `HTTPDetails`:

```go
			HTTPDetails: bremlmonitor.HTTPDetails{
				URL:                      spec.HTTP.URL,
				Method:                   spec.HTTP.Method,
				AcceptedStatusCodes:      spec.HTTP.AcceptedStatusCodes,
				Timeout:                  spec.HTTP.Timeout,
				MaxRedirects:             spec.HTTP.MaxRedirects,
				IgnoreTLS:                spec.HTTP.IgnoreTLS,
				CacheBust:                spec.HTTP.CacheBust,
				ExpiryNotification:       spec.HTTP.ExpiryNotification,
				DomainExpiryNotification: spec.HTTP.DomainExpiryNotification,
				Headers:                  spec.HTTP.Headers,
				Body:                     spec.HTTP.Body,
				AuthMethod:               bremlmonitor.AuthMethod(spec.HTTP.AuthMethod),
				BasicAuthUser:            spec.HTTP.BasicAuthUsername,
				BasicAuthPass:            spec.HTTP.BasicAuthPassword,
				BearerToken:              spec.HTTP.BearerToken,
				OAuthClientID:            spec.HTTP.OAuthClientID,
				OAuthClientSecret:        spec.HTTP.OAuthClientSecret,
				OAuthTokenURL:            spec.HTTP.OAuthTokenURL,
				OAuthScopes:              spec.HTTP.OAuthScopes,
				OAuthAudience:            spec.HTTP.OAuthAudience,
			},
```

In the `TypeGamedig` case, set `GameDigToken` (a `*string` in breml — nil when empty, same pattern as `Description`):

```go
		details := bremlmonitor.GameDigDetails{
			Hostname:                 spec.Gamedig.Host,
			Port:                     spec.Gamedig.Port,
			Game:                     spec.Gamedig.Game,
			GameDigGivenPortOnly:     spec.Gamedig.GivenPortOnly,
			DomainExpiryNotification: spec.Gamedig.DomainExpiryNotification,
		}
		if spec.Gamedig.Token != "" {
			token := spec.Gamedig.Token
			details.GameDigToken = &token
		}
		return &bremlmonitor.GameDig{Base: base, GameDigDetails: details}, nil
```

(Replace the existing single-expression `return &bremlmonitor.GameDig{...}` with this two-step form.)

In `FromBremlMonitor`'s `"http"` case, extend the `HTTPSpec` literal:

```go
		spec.HTTP = &HTTPSpec{
			URL: d.URL, Method: d.Method, AcceptedStatusCodes: d.AcceptedStatusCodes,
			Timeout: d.Timeout, MaxRedirects: d.MaxRedirects, IgnoreTLS: d.IgnoreTLS,
			CacheBust: d.CacheBust, ExpiryNotification: d.ExpiryNotification,
			DomainExpiryNotification: d.DomainExpiryNotification, Headers: d.Headers, Body: d.Body,
			AuthMethod: string(d.AuthMethod), BasicAuthUsername: d.BasicAuthUser, BasicAuthPassword: d.BasicAuthPass,
			BearerToken: d.BearerToken, OAuthClientID: d.OAuthClientID, OAuthClientSecret: d.OAuthClientSecret,
			OAuthTokenURL: d.OAuthTokenURL, OAuthScopes: d.OAuthScopes, OAuthAudience: d.OAuthAudience,
		}
```

In the `"gamedig"` case:

```go
		gamedig := &GamedigSpec{
			Host: d.Hostname, Port: d.Port, Game: d.Game,
			GivenPortOnly: d.GameDigGivenPortOnly, DomainExpiryNotification: d.DomainExpiryNotification,
		}
		if d.GameDigToken != nil {
			gamedig.Token = *d.GameDigToken
		}
		spec.Gamedig = gamedig
```

`Equivalent`'s `TypeHTTP` case already uses `reflect.DeepEqual(*d.HTTP, *live.HTTP)` (Phase 1's fix) — it automatically covers every new `HTTPSpec` field with no further change needed. `TypeGamedig`'s case (`*d.Gamedig == *live.Gamedig`) needs no change either, **provided** `Token string` keeps `GamedigSpec` fully scalar (it does — confirm no slice/map/pointer was introduced).

Resolve the empirical caveat from Step 1 here: if masking is confirmed, adjust the `TypeHTTP` comparison to exclude the three secret fields before the `DeepEqual` (same "zero out the field on both sides first" pattern already used for `AcceptedStatusCodes` — but here on copies, not maps, since these are plain strings) rather than a broad rewrite.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/kuma/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/kuma/spec.go internal/kuma/translate.go internal/kuma/translate_test.go
git commit -m "feat: add HTTP auth and Gamedig token fields to kuma package"
```

---

### Task 8: CRD fields (SecretKeySelectors) and RBAC

**Files:**
- Modify: `api/v1alpha1/monitor_types.go`
- Modify: `charts/uptime-kuma-operator/templates/rbac.yaml`

**Interfaces:**
- Produces: `HTTPMonitorSpec` gains `AuthMethod`, `BasicAuthUsername`, `BasicAuthPasswordSecretRef *corev1.SecretKeySelector`, `BearerTokenSecretRef *corev1.SecretKeySelector`, `OAuthClientID`, `OAuthClientSecretRef *corev1.SecretKeySelector`, `OAuthTokenURL`, `OAuthScopes`, `OAuthAudience`. `GamedigMonitorSpec` gains `TokenSecretRef *corev1.SecretKeySelector`. RBAC templates grant `secrets: get,list,watch`.

- [ ] **Step 1: Add the CRD fields**

In `api/v1alpha1/monitor_types.go`, add the import `corev1 "k8s.io/api/core/v1"`. Extend `HTTPMonitorSpec`:

```go
	// AuthMethod selects the HTTP auth scheme: "" (none), "basic", "bearer",
	// or "oauth2-cc". NTLM and mTLS are not supported.
	// +kubebuilder:validation:Enum="";basic;bearer;oauth2-cc
	AuthMethod string `json:"authMethod,omitempty"`
	// BasicAuthUsername is used when authMethod is "basic". Not secret.
	BasicAuthUsername string `json:"basicAuthUsername,omitempty"`
	// BasicAuthPasswordSecretRef selects the password Secret key when
	// authMethod is "basic". The Secret must be in the same namespace as
	// this resource.
	BasicAuthPasswordSecretRef *corev1.SecretKeySelector `json:"basicAuthPasswordSecretRef,omitempty"`
	// BearerTokenSecretRef selects the token Secret key when authMethod is
	// "bearer". The Secret must be in the same namespace as this resource.
	BearerTokenSecretRef *corev1.SecretKeySelector `json:"bearerTokenSecretRef,omitempty"`
	// OAuthClientID is used when authMethod is "oauth2-cc". Not secret.
	OAuthClientID string `json:"oauthClientID,omitempty"`
	// OAuthClientSecretRef selects the client secret Secret key when
	// authMethod is "oauth2-cc". The Secret must be in the same namespace
	// as this resource.
	OAuthClientSecretRef *corev1.SecretKeySelector `json:"oauthClientSecretRef,omitempty"`
	OAuthTokenURL         string `json:"oauthTokenURL,omitempty"`
	OAuthScopes           string `json:"oauthScopes,omitempty"`
	OAuthAudience         string `json:"oauthAudience,omitempty"`
```

Extend `GamedigMonitorSpec`:

```go
	// TokenSecretRef selects an optional auth token Secret key. The Secret
	// must be in the same namespace as this resource.
	TokenSecretRef *corev1.SecretKeySelector `json:"tokenSecretRef,omitempty"`
```

- [ ] **Step 2: Regenerate and verify**

Run: `make generate manifests`. `corev1.SecretKeySelector` is a well-known Kubernetes type with its own existing OpenAPI schema — confirm controller-gen embeds a correct `secretKeyRef`-shaped schema (`name`, `key`, `optional`) in both generated CRD YAMLs, and that `zz_generated.deepcopy.go` gained correct deepcopy handling for the three new `*corev1.SecretKeySelector` pointer fields (controller-gen knows how to deepcopy a type it recognizes from `k8s.io/api/core/v1`, but confirm the generated code compiles). Run `go build ./...`.

- [ ] **Step 3: Add the RBAC rule**

In `charts/uptime-kuma-operator/templates/rbac.yaml`, add to the `$rules` list (after the `events` entry):

```yaml
  (dict "apiGroups" (list "") "resources" (list "secrets") "verbs" (list "get" "list" "watch"))
```

- [ ] **Step 4: Verify**

Run: `go build ./...` and `helm template test charts/uptime-kuma-operator --set kuma.url=https://x --set kuma.existingSecret=s | grep -A3 'resources:\s*$' ` (or simply `helm template ... | grep -B2 secrets` ) to confirm the rendered `Role`/`ClusterRole` includes the `secrets` rule. Run `helm lint charts/uptime-kuma-operator --set kuma.url=https://x --set kuma.existingSecret=s`.

- [ ] **Step 5: Commit**

```bash
git add api/v1alpha1/monitor_types.go api/v1alpha1/zz_generated.deepcopy.go config/crd/bases/uptime-kuma.io_monitors.yaml charts/uptime-kuma-operator/crds/uptime-kuma.io_monitors.yaml charts/uptime-kuma-operator/templates/rbac.yaml
git commit -m "feat: add HTTP auth/Gamedig token SecretKeySelector CRD fields and secrets RBAC"
```

---

### Task 9: Secret resolution and wiring in the Monitor CRD reconciler

**Files:**
- Modify: `internal/controller/convert.go`
- Modify: `internal/controller/monitor_controller.go`
- Test: `internal/controller/monitor_controller_test.go`

**Interfaces:**
- Consumes: Task 7's `kuma.HTTPSpec`/`kuma.GamedigSpec` credential fields, Task 8's CRD `SecretKeySelector` fields.
- Produces: a `resolveSecret(ctx, c client.Client, namespace string, ref *corev1.SecretKeySelector) (string, error)` helper in `internal/controller/sync.go`, called from `monitor_controller.go` (HTTP auth and Gamedig token are CRD-only per the design — no Ingress/HTTPRoute annotation surface, so this task touches only the Monitor CRD reconciler).

- [ ] **Step 1: Write the failing tests**

Append to `internal/controller/monitor_controller_test.go`:

```go
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
```

Add a test asserting the secret value never appears in log output — this needs a captured logger, following the exact pattern from the earlier `tmp_logcheck`-style verification used elsewhere in this project's history (a `zap.New(zap.WriteTo(&buf), ...)` logger injected via `logf.IntoContext`, then reconcile, then assert `!bytes.Contains(buf.Bytes(), []byte("hunter2"))`). Write this as `TestMonitorReconciler_SecretNeverLogged` in the same file, reusing `TestMonitorReconciler_ResolvesHTTPBasicAuthSecret`'s Monitor/Secret setup.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `KUBEBUILDER_ASSETS="$(bin/setup-envtest use -p path 1.31.x)" go test ./internal/controller/... -run 'ResolvesHTTPBasicAuthSecret|MissingSecretFailsReconcile|SecretNeverLogged' -v`
Expected: compile error or failures (secret values not yet wired through).

- [ ] **Step 3: Add `resolveSecret` to `sync.go`**

```go
// resolveSecret reads the value at ref's key from the Secret named ref.Name
// in namespace. Returns "" with no error if ref is nil (field not configured).
func resolveSecret(ctx context.Context, c client.Client, namespace string, ref *corev1.SecretKeySelector) (string, error) {
	if ref == nil {
		return "", nil
	}
	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, secret); err != nil {
		return "", fmt.Errorf("resolve secret %s/%s: %w", namespace, ref.Name, err)
	}
	value, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("resolve secret %s/%s: key %q not found", namespace, ref.Name, ref.Key)
	}
	return string(value), nil
}
```

Add the import `corev1 "k8s.io/api/core/v1"` to `internal/controller/sync.go` if not already present.

- [ ] **Step 4: Extend `toKumaSpec` and wire resolution in `monitor_controller.go`**

`toKumaSpec` (`internal/controller/convert.go`) copies the non-secret fields directly, same as every other field:

```go
	if spec.HTTP != nil {
		out.HTTP = &kuma.HTTPSpec{
			URL:                      spec.HTTP.URL,
			Method:                   spec.HTTP.Method,
			AcceptedStatusCodes:      spec.HTTP.AcceptedStatusCodes,
			Timeout:                  spec.HTTP.Timeout,
			MaxRedirects:             int(spec.HTTP.MaxRedirects),
			IgnoreTLS:                spec.HTTP.IgnoreTLS,
			CacheBust:                spec.HTTP.CacheBust,
			ExpiryNotification:       spec.HTTP.ExpiryNotification,
			DomainExpiryNotification: spec.HTTP.DomainExpiryNotification,
			Headers:                  spec.HTTP.Headers,
			Body:                     spec.HTTP.Body,
			AuthMethod:               spec.HTTP.AuthMethod,
			BasicAuthUsername:        spec.HTTP.BasicAuthUsername,
			OAuthClientID:            spec.HTTP.OAuthClientID,
			OAuthTokenURL:            spec.HTTP.OAuthTokenURL,
			OAuthScopes:              spec.HTTP.OAuthScopes,
			OAuthAudience:            spec.HTTP.OAuthAudience,
			// BasicAuthPassword, BearerToken, OAuthClientSecret are resolved
			// separately in monitor_controller.go, which holds the
			// client.Client toKumaSpec doesn't have.
		}
	}
```

(The three secret fields are deliberately left unset here — `toKumaSpec` stays a pure, `client.Client`-free converter.)

In `internal/controller/monitor_controller.go`, after `desiredSpec := toKumaSpec(mon.Spec)` and the notifications/group resolution added in Task 3, resolve the HTTP/Gamedig secrets:

```go
	if mon.Spec.HTTP != nil && desiredSpec.HTTP != nil {
		pass, err := resolveSecret(ctx, r.Client, mon.Namespace, mon.Spec.HTTP.BasicAuthPasswordSecretRef)
		if err != nil {
			recordSyncFailure(r.Recorder, mon, err)
			return ctrl.Result{}, err
		}
		desiredSpec.HTTP.BasicAuthPassword = pass

		token, err := resolveSecret(ctx, r.Client, mon.Namespace, mon.Spec.HTTP.BearerTokenSecretRef)
		if err != nil {
			recordSyncFailure(r.Recorder, mon, err)
			return ctrl.Result{}, err
		}
		desiredSpec.HTTP.BearerToken = token

		clientSecret, err := resolveSecret(ctx, r.Client, mon.Namespace, mon.Spec.HTTP.OAuthClientSecretRef)
		if err != nil {
			recordSyncFailure(r.Recorder, mon, err)
			return ctrl.Result{}, err
		}
		desiredSpec.HTTP.OAuthClientSecret = clientSecret
	}
	if mon.Spec.Gamedig != nil && desiredSpec.Gamedig != nil {
		token, err := resolveSecret(ctx, r.Client, mon.Namespace, mon.Spec.Gamedig.TokenSecretRef)
		if err != nil {
			recordSyncFailure(r.Recorder, mon, err)
			return ctrl.Result{}, err
		}
		desiredSpec.Gamedig.Token = token
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `make test`
Expected: PASS, full suite green.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/convert.go internal/controller/monitor_controller.go internal/controller/sync.go internal/controller/monitor_controller_test.go
git commit -m "feat: resolve HTTP auth/Gamedig token Secrets in the Monitor CRD reconciler"
```

---

### Task 10: Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`
- Modify: `config/samples/uptime-kuma_v1alpha1_monitor.yaml`

**Interfaces:**
- Consumes: nothing new — documents Tasks 1–9's finished surface.

- [ ] **Step 1: Extend the annotation contract table**

In `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`'s annotation table, add rows for `tags`, `notifications`, `proxy`, `group` (following the exact table-row format Phase 1 used), and a note that HTTP auth and Gamedig token are Monitor-CRD-only (no annotation), with a link to `docs/superpowers/specs/2026-09-18-monitor-config-expansion-phase2-design.md`.

- [ ] **Step 2: Add a Monitor CRD sample showing the new fields**

In `config/samples/uptime-kuma_v1alpha1_monitor.yaml`, add a third YAML document (`---` separator, alongside the existing Gamedig and HTTP samples) demonstrating `tags`, `notifications`, `group`, `proxy`, and HTTP `authMethod`/`basicAuthPasswordSecretRef` with a matching example `Secret` (as a fourth document in the same file, or note in a comment that the referenced Secret must be created separately — match whichever convention reads more naturally next to the existing samples).

- [ ] **Step 3: Update the README**

Add a short paragraph near the existing Phase 1 pointer (in the "Opt an Ingress in" section) noting that tags/notifications/proxy/group are also available via annotation, and that HTTP auth/Gamedig token require the Monitor CRD with a `SecretKeySelector` — pointing to the updated sample and the Phase 2 design doc.

- [ ] **Step 4: Verify**

Run: `make test` and `helm lint charts/uptime-kuma-operator --set kuma.url=https://x --set kuma.existingSecret=s`.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md config/samples/uptime-kuma_v1alpha1_monitor.yaml
git commit -m "docs: document Phase 2 monitor configuration fields"
```
