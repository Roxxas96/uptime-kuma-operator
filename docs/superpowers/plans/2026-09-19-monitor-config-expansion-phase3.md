# Monitor Configuration Expansion — Phase 3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the operator automatically derive Kuma tags from the Kubernetes labels already present on a reconciled Ingress, HTTPRoute, or Monitor CR, controlled by a single operator-wide regex-pattern config setting — no new per-resource annotation.

**Architecture:** One new `Config` field (`LabelTagPatterns`, a list of compiled, anchored regexes parsed from a comma-separated env var) flows into all three reconcilers exactly like `DefaultTags` already does. A pure helper, `deriveLabelTags`, turns a resource's `metadata.labels` into `"key=value"` tag-name strings for every label whose key fully matches at least one pattern. Those derived names join the existing `DefaultTags`/per-resource-tags union at each reconciler's `syncTags` call site via a generalized (variadic) `mergeTags`. No changes to `kuma.Client`, `kuma.MonitorSpec`, or any CRD/annotation surface.

**Tech Stack:** Go, controller-runtime, `regexp` (stdlib).

**Spec:** `docs/superpowers/specs/2026-09-19-monitor-config-expansion-phase3-design.md`

## Global Constraints

- Label-tag inference is entirely operator-wide: an empty/unset `LABEL_TAG_PATTERNS` means the feature is off for every monitor — no per-resource opt-in or opt-out exists in this phase.
- Every regex pattern is compiled anchored (`^(?:pattern)$`) at config-load time, in `internal/config.Load` — never compiled or re-compiled inside a reconcile loop, and never applied unanchored.
- An invalid regex pattern fails operator **startup** (`Load` returns an error), never a single reconcile — a bad pattern must not intermittently break monitor syncing for unrelated resources.
- A label becomes the Kuma tag literally named `"key=value"` — no per-tag-association *value* is used (`AddMonitorTag`'s value parameter stays hardcoded to `""`, unchanged from Phase 2). No changes to `kuma.Client`, `kuma.MonitorSpec`, `ToBremlMonitor`/`FromBremlMonitor`/`Equivalent`.
- Label-derived tags are read from the reconciled object's own `metadata.labels` only (the Ingress/HTTPRoute/Monitor itself) — never a backing Service/Pod's labels.
- Label-derived tags union with `DefaultTags` and the resource's own explicit tags (CRD field / `uptime-kuma.io/tags` annotation) — never override or replace them. The union is fully declarative: a label removed from the source object drops its derived tag association on the next reconcile, same as any other tag today.
- `LABEL_TAG_PATTERNS` is comma-separated with no escaping (same limitation `DEFAULT_TAGS` already has) — a pattern must not itself contain a literal comma. Document this, don't build escaping for it (YAGNI).
- No new RBAC — this reads `metadata.labels`, already part of objects the operator fully watches.
- Run `make test`, `gofmt -l .` (excluding `bin/`), and `go vet ./...` clean before considering any task done.

---

### Task 1: `internal/config` — `LabelTagPatterns` parsing

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `Config.LabelTagPatterns []*regexp.Regexp` — nil when unset, each entry already compiled and anchored.
- Consumed by: Task 3 (`cmd/main.go` threads it into each reconciler's new `LabelTagPatterns` field).

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
func TestLoad_LabelTagPatternsEmptyWhenUnset(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"POD_NAMESPACE": "default",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.LabelTagPatterns) != 0 {
		t.Errorf("LabelTagPatterns = %v, want empty", cfg.LabelTagPatterns)
	}
}

func TestLoad_LabelTagPatternsParsesAndAnchorsPatterns(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":           "https://kuma.example.com",
		"KUMA_USERNAME":      "admin",
		"KUMA_PASSWORD":      "secret",
		"POD_NAMESPACE":      "default",
		"LABEL_TAG_PATTERNS": "team, env-.*",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.LabelTagPatterns) != 2 {
		t.Fatalf("LabelTagPatterns = %v, want 2 compiled patterns", cfg.LabelTagPatterns)
	}
	if !cfg.LabelTagPatterns[0].MatchString("team") {
		t.Error(`LabelTagPatterns[0] ("team") should match "team"`)
	}
	if cfg.LabelTagPatterns[0].MatchString("my-team") {
		t.Error(`LabelTagPatterns[0] ("team") should NOT match "my-team" — patterns must be anchored`)
	}
	if !cfg.LabelTagPatterns[1].MatchString("env-prod") {
		t.Error(`LabelTagPatterns[1] ("env-.*") should match "env-prod"`)
	}
}

func TestLoad_LabelTagPatternsInvalidRegexFails(t *testing.T) {
	_, err := Load(envMap(map[string]string{
		"KUMA_URL":           "https://kuma.example.com",
		"KUMA_USERNAME":      "admin",
		"KUMA_PASSWORD":      "secret",
		"POD_NAMESPACE":      "default",
		"LABEL_TAG_PATTERNS": "team-(",
	}))
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
}

func TestLoad_LabelTagPatternsSkipsBlankEntries(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":           "https://kuma.example.com",
		"KUMA_USERNAME":      "admin",
		"KUMA_PASSWORD":      "secret",
		"POD_NAMESPACE":      "default",
		"LABEL_TAG_PATTERNS": "team, , env",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.LabelTagPatterns) != 2 {
		t.Errorf("LabelTagPatterns = %v, want 2 patterns (blank entry skipped)", cfg.LabelTagPatterns)
	}
}
```

(`envMap` is the existing test helper already used by `TestLoad_DefaultTagsParsesAndTrimsList` and others in this file.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/... -run 'LabelTagPatterns' -v`
Expected: compile error (`Config` has no `LabelTagPatterns` field).

- [ ] **Step 3: Add the `regexp` import and `LabelTagPatterns` field**

In `internal/config/config.go`, add `"regexp"` to the import block, and add to `Config` (after `DefaultTags`):

```go
	// LabelTagPatterns, when non-empty, turns on operator-wide label-tag
	// inference: for every monitor the operator manages, a "key=value" Kuma
	// tag is added for each label on the label-owning resource
	// (Ingress/HTTPRoute/Monitor) whose key fully matches at least one of
	// these patterns. Each raw pattern from LABEL_TAG_PATTERNS is compiled
	// anchored (^(?:pattern)$), so a plain prefix filter is expressed as
	// e.g. "team-.*". Patterns must not themselves contain a literal comma
	// — LABEL_TAG_PATTERNS is comma-separated with no escaping, the same
	// limitation DEFAULT_TAGS already has. Empty by default: the feature is
	// entirely off unless at least one pattern is configured.
	LabelTagPatterns []*regexp.Regexp
```

- [ ] **Step 4: Parse `LABEL_TAG_PATTERNS` in `Load`**

Insert immediately after the existing `DEFAULT_TAGS` parsing block (after its closing `}`):

```go
	if raw := getenv("LABEL_TAG_PATTERNS"); raw != "" {
		for _, pattern := range strings.Split(raw, ",") {
			pattern = strings.TrimSpace(pattern)
			if pattern == "" {
				continue
			}
			re, err := regexp.Compile("^(?:" + pattern + ")$")
			if err != nil {
				return Config{}, fmt.Errorf("config: LABEL_TAG_PATTERNS pattern %q is not a valid regex: %w", pattern, err)
			}
			cfg.LabelTagPatterns = append(cfg.LabelTagPatterns, re)
		}
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/config/... -v`
Expected: PASS, all tests including pre-existing ones.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add LABEL_TAG_PATTERNS config for operator-wide label-tag inference"
```

---

### Task 2: `internal/controller/sync.go` — `deriveLabelTags` and a three-way `mergeTags`

**Files:**
- Modify: `internal/controller/sync.go`
- Test: `internal/controller/sync_test.go`

**Interfaces:**
- Produces: `deriveLabelTags(labels map[string]string, patterns []*regexp.Regexp) []string` — pure, no I/O. `mergeTags` changes signature from `mergeTags(defaults, specific []string) []string` to `mergeTags(sources ...[]string) []string`; every existing two-argument call site (`mergeTags(r.DefaultTags, mon.Spec.Tags)` etc.) keeps compiling unchanged, since Go accepts individual `[]string` arguments for a `...[]string` parameter.
- Consumed by: Task 3 (each reconciler calls `deriveLabelTags` on its object's `.Labels` and adds the result as a third argument to its existing `mergeTags` calls).

- [ ] **Step 1: Write the failing tests**

Append to `internal/controller/sync_test.go` (add `"regexp"` and `"sort"` to the import block if not already present — check the current import list first):

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/controller/... -run 'DeriveLabelTags|MergeTags_ThreeWay' -v`
Expected: compile error (`deriveLabelTags` doesn't exist; `mergeTags` called with 3 args against a 2-arg signature).

**Important empirical caveat to resolve before/while implementing this task**: neither breml's client nor Kuma's own API layer is known to enforce a length or charset limit on a tag's `Name` (checked `tag.Tag` in `go-uptime-kuma-client@v0.4.2/tag/tag.go` — plain `string`, no validation). A `"key=value"` string built from a long, prefixed Kubernetes label key (e.g. `app.kubernetes.io/name=some-very-long-service-name`) could exceed whatever limit Kuma's own schema has server-side. Verify against a real Kuma instance (same docker-compose approach used throughout this project) what happens with a long/unusual tag name — truncation, a 4xx error from `CreateTag`, or no limit at all — and note the finding in this task's commit message. This is informational/documentation-only for this plan (no test here depends on a specific outcome); if the finding turns out to be correctness-blocking (e.g. `CreateTag` errors and that error isn't handled gracefully), treat that as a new bug to fix in a follow-up task rather than blocking this one.

- [ ] **Step 3: Generalize `mergeTags` to variadic**

In `internal/controller/sync.go`, replace the existing `mergeTags` function body (keep its doc comment, updated for three-plus sources):

```go
// mergeTags unions any number of tag-name sources — operator-wide
// DEFAULT_TAGS, a resource's own explicit tags, and (Phase 3)
// label-derived tags — deduplicating by name so a tag listed in more than
// one source isn't resolved/created twice. Order is source-order, first
// occurrence wins — cosmetic only, since syncTags resolves names to IDs
// and Kuma tracks tag associations as a set.
func mergeTags(sources ...[]string) []string {
	seen := make(map[string]bool)
	var merged []string
	for _, src := range sources {
		for _, name := range src {
			if !seen[name] {
				seen[name] = true
				merged = append(merged, name)
			}
		}
	}
	return merged
}
```

This is a drop-in replacement: every existing call site (`mergeTags(r.DefaultTags, mon.Spec.Tags)` in `monitor_controller.go`, `mergeTags(r.DefaultTags, ov.Tags)` in `ingress_controller.go`/`httproute_controller.go`) keeps compiling and behaving identically, since Go passes each positional `[]string` argument into the variadic slice unchanged.

- [ ] **Step 4: Add `deriveLabelTags`**

Add near `mergeTags` in `internal/controller/sync.go` (add `"regexp"` to the import block):

```go
// deriveLabelTags returns the Kuma tag names derived from labels for
// Phase 3's operator-wide label-tag inference: for every label whose key
// fully matches at least one of patterns, "key=value" is included. patterns
// are assumed already anchored (config.Load compiles them as ^(?:...)$) —
// this function does no anchoring of its own. Order is unspecified; callers
// that need determinism should sort, same as any other tag-name slice fed
// into mergeTags/syncTags.
func deriveLabelTags(labels map[string]string, patterns []*regexp.Regexp) []string {
	if len(patterns) == 0 {
		return nil
	}
	var tags []string
	for key, value := range labels {
		for _, pattern := range patterns {
			if pattern.MatchString(key) {
				tags = append(tags, key+"="+value)
				break
			}
		}
	}
	return tags
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/controller/... -run 'DeriveLabelTags|MergeTags' -v`
Expected: PASS, all `TestMergeTags_*` tests (including the pre-existing `TestMergeTags_UnionsAndDedupes`, `TestMergeTags_NoDefaultsReturnsSpecificUnchanged`, `TestMergeTags_NoSpecificReturnsDefaultsOnly`, `TestMergeTags_BothEmptyReturnsEmpty`) still pass unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/sync.go internal/controller/sync_test.go
git commit -m "feat: add deriveLabelTags and generalize mergeTags to a three-way union"
```

---

### Task 3: Wire `LabelTagPatterns` into all three reconcilers

**Files:**
- Modify: `internal/controller/monitor_controller.go`
- Modify: `internal/controller/ingress_controller.go`
- Modify: `internal/controller/httproute_controller.go`
- Modify: `cmd/main.go`
- Test: `internal/controller/monitor_controller_test.go`, `internal/controller/ingress_controller_test.go`, `internal/controller/httproute_controller_test.go`

**Interfaces:**
- Consumes: Task 1's `config.Config.LabelTagPatterns`, Task 2's `deriveLabelTags`/variadic `mergeTags`.
- Produces: each reconciler struct gains `LabelTagPatterns []*regexp.Regexp`, threaded from `cfg.LabelTagPatterns` in `cmd/main.go`, exactly parallel to the existing `DefaultTags` field.

- [ ] **Step 1: Write the failing tests**

Append to `internal/controller/monitor_controller_test.go` (add `"regexp"` to the import block):

```go
func TestMonitorReconciler_AppliesLabelDerivedTags(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)
	r.LabelTagPatterns = []*regexp.Regexp{regexp.MustCompile(`^(?:team)$`)}

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-with-label-tag", Namespace: "default",
			Labels: map[string]string{"team": "platform", "other": "ignored"},
		},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type: uptimekumaiov1alpha1.MonitorTypePing,
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
	tagIDs := fake.MonitorTags[id]
	if len(tagIDs) != 1 {
		t.Fatalf("MonitorTags[id] = %v, want 1 entry (only \"team\" matches the pattern)", tagIDs)
	}
	name := ""
	for tagName, tagID := range fake.TagIDs {
		if tagID == tagIDs[0] {
			name = tagName
		}
	}
	if name != "team=platform" {
		t.Errorf("inferred tag name = %q, want %q", name, "team=platform")
	}
}
```

Append to `internal/controller/ingress_controller_test.go` (add `"regexp"` to the import block):

```go
func TestIngressReconciler_AppliesLabelDerivedTags(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)
	r.LabelTagPatterns = []*regexp.Regexp{regexp.MustCompile(`^(?:team)$`)}

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-with-label-tag", Namespace: "default",
			Labels:      map[string]string{"team": "platform"},
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
		t.Fatalf("Reconcile: %v", err)
	}

	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(fake.Monitors))
	}
	var id int64
	for monID := range fake.Monitors {
		id = monID
	}
	tagIDs := fake.MonitorTags[id]
	if len(tagIDs) != 1 {
		t.Errorf("MonitorTags[id] = %v, want 1 entry", tagIDs)
	}
}
```

Append to `internal/controller/httproute_controller_test.go` (add `"regexp"` to the import block):

```go
func TestHTTPRouteReconciler_AppliesLabelDerivedTags(t *testing.T) {
	ctx := context.Background()
	r, fake := newHTTPRouteReconciler(false)
	r.LabelTagPatterns = []*regexp.Regexp{regexp.MustCompile(`^(?:team)$`)}

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-with-label-tag", Namespace: "default",
			Labels:      map[string]string{"team": "platform"},
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
		t.Fatalf("Reconcile: %v", err)
	}

	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(fake.Monitors))
	}
	var id int64
	for monID := range fake.Monitors {
		id = monID
	}
	tagIDs := fake.MonitorTags[id]
	if len(tagIDs) != 1 {
		t.Errorf("MonitorTags[id] = %v, want 1 entry", tagIDs)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `KUBEBUILDER_ASSETS="$(bin/setup-envtest use -p path 1.31.x)" go test ./internal/controller/... -run 'AppliesLabelDerivedTags' -v`
Expected: compile error (`LabelTagPatterns` field doesn't exist on any of the three reconciler structs).

- [ ] **Step 3: Add `LabelTagPatterns` to `MonitorReconciler` and wire it in**

In `internal/controller/monitor_controller.go`, add `"regexp"` to the import block. Add to the `MonitorReconciler` struct (after `DefaultTags`):

```go
	// LabelTagPatterns, when non-empty, turns on Phase 3's operator-wide
	// label-tag inference for every monitor this reconciler manages. See
	// sync.go's deriveLabelTags doc comment.
	LabelTagPatterns []*regexp.Regexp
```

Change both existing `syncTags` call sites from:

```go
if err := syncTags(ctx, r.Kuma, existingID, mergeTags(r.DefaultTags, mon.Spec.Tags)); err != nil {
```

and

```go
if err := syncTags(ctx, r.Kuma, newID, mergeTags(r.DefaultTags, mon.Spec.Tags)); err != nil {
```

to:

```go
if err := syncTags(ctx, r.Kuma, existingID, mergeTags(r.DefaultTags, mon.Spec.Tags, deriveLabelTags(mon.Labels, r.LabelTagPatterns))); err != nil {
```

and

```go
if err := syncTags(ctx, r.Kuma, newID, mergeTags(r.DefaultTags, mon.Spec.Tags, deriveLabelTags(mon.Labels, r.LabelTagPatterns))); err != nil {
```

(`mon.Labels` is `mon.ObjectMeta.Labels`, promoted through the embedded `metav1.ObjectMeta`.)

- [ ] **Step 4: Same pattern for `IngressReconciler`**

In `internal/controller/ingress_controller.go`, add `"regexp"` to the import block. Add to the `IngressReconciler` struct (after `DefaultTags`):

```go
	// LabelTagPatterns, when non-empty, turns on Phase 3's operator-wide
	// label-tag inference for every monitor this reconciler manages. See
	// sync.go's deriveLabelTags doc comment.
	LabelTagPatterns []*regexp.Regexp
```

Change all three existing `mergeTags(r.DefaultTags, ov.Tags)` call sites (inside the `specsMatch` branch, the `reconcileDrift` branch, and the final `syncMonitors` branch — grep the file for `mergeTags` to find all three) to:

```go
mergeTags(r.DefaultTags, ov.Tags, deriveLabelTags(ing.Labels, r.LabelTagPatterns))
```

- [ ] **Step 5: Same pattern for `HTTPRouteReconciler`**

In `internal/controller/httproute_controller.go`, add `"regexp"` to the import block. Add to the `HTTPRouteReconciler` struct (after `DefaultTags`):

```go
	// LabelTagPatterns, when non-empty, turns on Phase 3's operator-wide
	// label-tag inference for every monitor this reconciler manages. See
	// sync.go's deriveLabelTags doc comment.
	LabelTagPatterns []*regexp.Regexp
```

Change all three existing `mergeTags(r.DefaultTags, ov.Tags)` call sites to:

```go
mergeTags(r.DefaultTags, ov.Tags, deriveLabelTags(route.Labels, r.LabelTagPatterns))
```

- [ ] **Step 6: Wire `cfg.LabelTagPatterns` in `cmd/main.go`**

In `cmd/main.go`, extend all three reconciler struct literals:

```go
	if err := (&controller.IngressReconciler{
		Client: mgr.GetClient(), Kuma: kumaClient, OptInByDefault: cfg.OptInByDefault,
		Recorder: recorder, DriftCheckInterval: cfg.DriftCheckInterval, DefaultTags: cfg.DefaultTags,
		LabelTagPatterns: cfg.LabelTagPatterns,
	}).SetupWithManager(mgr); err != nil {
```

```go
	if err := (&controller.MonitorReconciler{
		Client: mgr.GetClient(), Kuma: kumaClient, Recorder: recorder, DriftCheckInterval: cfg.DriftCheckInterval,
		DefaultTags: cfg.DefaultTags, LabelTagPatterns: cfg.LabelTagPatterns,
	}).SetupWithManager(mgr); err != nil {
```

```go
		if err := (&controller.HTTPRouteReconciler{
			Client: mgr.GetClient(), Kuma: kumaClient, OptInByDefault: cfg.OptInByDefault,
			Recorder: recorder, DriftCheckInterval: cfg.DriftCheckInterval, DefaultTags: cfg.DefaultTags,
			LabelTagPatterns: cfg.LabelTagPatterns,
		}).SetupWithManager(mgr); err != nil {
```

Also extend the startup log line for visibility:

```go
	log.Info("starting manager", "watchNamespaces", cfg.WatchNamespaces, "watchAll", cfg.WatchAll,
		"optInByDefault", cfg.OptInByDefault, "driftCheckInterval", cfg.DriftCheckInterval, "logLevel", cfg.LogLevel,
		"defaultTags", cfg.DefaultTags, "labelTagPatternCount", len(cfg.LabelTagPatterns))
```

(Logging the pattern *count*, not the compiled `regexp.Regexp` values themselves, keeps the log line readable — the patterns' source strings aren't retained on `Config` after compiling, consistent with `DefaultTags`/other settings being logged by value while this one is logged by size.)

- [ ] **Step 7: Run the tests to verify they pass**

Run: `make test`
Expected: PASS, full suite green.

- [ ] **Step 8: Commit**

```bash
git add internal/controller/monitor_controller.go internal/controller/ingress_controller.go internal/controller/httproute_controller.go internal/controller/monitor_controller_test.go internal/controller/ingress_controller_test.go internal/controller/httproute_controller_test.go cmd/main.go
git commit -m "feat: apply operator-wide label-derived tags in all three reconcilers"
```

---

### Task 4: Helm chart — `labelTagPatterns` value and env var

**Files:**
- Modify: `charts/uptime-kuma-operator/values.yaml`
- Modify: `charts/uptime-kuma-operator/templates/deployment.yaml`

**Interfaces:**
- Consumes: nothing new — templates the env var Task 1's `config.Load` reads.

- [ ] **Step 1: Add the Helm value**

In `charts/uptime-kuma-operator/values.yaml`, add near `defaultTags` (same section):

```yaml
# labelTagPatterns lists regex patterns (see internal/config.Config's
# LabelTagPatterns doc comment for exact matching semantics — each pattern
# is anchored, so "team-.*" is a prefix filter). A label on a managed
# Ingress/HTTPRoute/Monitor is imported as a "key=value" Kuma tag when its
# key fully matches at least one pattern. Empty by default — label-tag
# inference is entirely off until at least one pattern is configured. A
# pattern must not contain a literal comma (patterns are comma-joined with
# no escaping, same as defaultTags). e.g. ["team", "env-.*"]
labelTagPatterns: []
```

- [ ] **Step 2: Template the env var**

In `charts/uptime-kuma-operator/templates/deployment.yaml`, add immediately after the existing `DEFAULT_TAGS` block:

```yaml
            - name: LABEL_TAG_PATTERNS
              value: {{ join "," .Values.labelTagPatterns | quote }}
```

- [ ] **Step 3: Verify**

Run: `helm lint charts/uptime-kuma-operator --set kuma.url=https://x --set kuma.existingSecret=s` and `helm template test charts/uptime-kuma-operator --set kuma.url=https://x --set kuma.existingSecret=s --set 'labelTagPatterns={team,env-.*}' | grep -A1 LABEL_TAG_PATTERNS` to confirm the rendered env var is `"team,env-.*"`.

- [ ] **Step 4: Commit**

```bash
git add charts/uptime-kuma-operator/values.yaml charts/uptime-kuma-operator/templates/deployment.yaml
git commit -m "feat: add labelTagPatterns Helm value for label-tag inference"
```

---

### Task 5: Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`

**Interfaces:**
- Consumes: nothing new — documents Tasks 1–4's finished surface.

- [ ] **Step 1: Extend the operator-wide configuration section**

In `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`'s "Operator-wide configuration" env var block, add a line after `DEFAULT_TAGS=`:

```
LABEL_TAG_PATTERNS=              # comma-separated regexes (anchored); labels matching are imported as "key=value" tags
```

And extend the sentence listing settings that require a pod restart to include `LABEL_TAG_PATTERNS`:

```
Changing `WATCH_NAMESPACES`, `WATCH_ALL`, `OPT_IN_BY_DEFAULT`,
`DRIFT_CHECK_INTERVAL`, `LOG_LEVEL`, `DEFAULT_TAGS`, or `LABEL_TAG_PATTERNS`
requires a pod restart (no hot-reload watcher) — acceptable since these
change rarely.
```

Add a short subsection (near the `DEFAULT_TAGS` explanation, or as its own paragraph) pointing to the Phase 3 design doc and noting the operational caveat from that spec:

```markdown
### Label-derived tags (Phase 3)

When `LABEL_TAG_PATTERNS` is configured, every monitor the operator
manages also gets a `"key=value"` Kuma tag for each label on its source
Ingress/HTTPRoute/Monitor whose key fully matches at least one pattern —
in addition to `DEFAULT_TAGS` and that resource's own explicit tags. This
is entirely operator-wide (no per-resource annotation); see
`docs/superpowers/specs/2026-09-19-monitor-config-expansion-phase3-design.md`
for the full design, including the tag-proliferation caveat: a pattern
that matches a high-cardinality label creates one Kuma tag object per
distinct value ever seen, and the operator never deletes an unused tag
object (tags are global and shared across monitors) — choose patterns
narrowly.
```

- [ ] **Step 2: Update the README**

Add a short paragraph near the existing `defaultTags` paragraph (in the same numbered walkthrough section):

```markdown
   Set `--set labelTagPatterns={team,env-.*}` to automatically add a
   `"key=value"` tag for every label on a managed Ingress/HTTPRoute/Monitor
   whose key fully matches one of these regex patterns (each pattern is
   anchored, so a plain prefix filter reads as e.g. `"team-.*"`). Empty by
   default — off until configured. See
   `docs/superpowers/specs/2026-09-19-monitor-config-expansion-phase3-design.md`
   for the full design and its tag-proliferation caveat.
```

- [ ] **Step 3: Verify**

Run: `make test` and `helm lint charts/uptime-kuma-operator --set kuma.url=https://x --set kuma.existingSecret=s`.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md
git commit -m "docs: document Phase 3 operator-wide label-tag inference"
```
