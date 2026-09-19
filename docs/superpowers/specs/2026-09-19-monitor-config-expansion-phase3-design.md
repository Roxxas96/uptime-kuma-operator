# Monitor Configuration Expansion — Phase 3 (tag inference from resource labels)

Sub-project of `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`,
following on from `docs/superpowers/specs/2026-09-15-monitor-config-expansion-phase1-design.md`
("Phase 1: direct-value fields") and
`docs/superpowers/specs/2026-09-18-monitor-config-expansion-phase2-design.md`
("Phase 2: referenced and credential fields", which introduced Kuma tag
support via `SetMonitorTags`). This doc covers Phase 3: automatically
deriving Kuma tags from the Kubernetes labels already present on a
reconciled Ingress, HTTPRoute, or Monitor CR — no changes to how a user
requests an *explicit* tag (that stays exactly as Phase 2 left it).

This design revises the one-paragraph placeholder Phase 1 left for Phase 3
("opt-in via an annotation ... optional filter ... prefix or explicit key
allowlist") after discussion with the user: the opt-in and the filter both
move to operator-wide Helm/env configuration, and the filter is regex-based
rather than prefix-based.

## Goal

Let every monitor the operator manages automatically pick up a subset of
its source object's Kubernetes labels as Kuma tags, so ownership/team/
environment metadata that's already on the Ingress/HTTPRoute/Monitor
doesn't have to be re-declared via the `tags` annotation or CRD field.

## Key decisions (from brainstorming with the user)

- **Operator-wide, not per-resource.** There is no per-resource opt-in
  annotation. Label-tag inference is a single operator-wide switch:
  configuring at least one regex pattern turns it on for every monitor the
  operator manages (Ingress-, HTTPRoute-, and Monitor-CRD-derived alike).
  This is a deliberate departure from Phase 1/2's per-resource-annotation
  pattern, matching the user's explicit preference and `DEFAULT_TAGS`'s
  existing all-or-nothing model rather than introducing a new annotation
  surface.
- **Filter is regex, not prefix/allowlist**, and lives in Helm/env config
  (`internal/config`), not in an annotation — also an explicit user
  preference over the Phase 1 placeholder's original sketch. A label's key
  must fully match (anchored `^...$`) at least one configured pattern to be
  imported; a plain prefix filter remains trivially expressible as
  `team-.*`.
- **Off by default.** An empty/unset pattern list disables the feature
  entirely — zero behavior change for existing deployments. This reverses
  the Phase 1 placeholder's "without a filter, all labels import" default,
  because an always-on global feature that imports every label (including
  Kubernetes' own noisy/high-cardinality ones like `pod-template-hash`,
  `controller-revision-hash`, `helm.sh/chart`) the moment it ships is worse
  than requiring one explicit config step.
- **`key=value` becomes the Kuma tag name**, not a name/value pair. Kuma
  does support a per-monitor-tag *value* (`AddMonitorTag`'s third
  parameter), but the operator's existing tag machinery
  (`Client.SetMonitorTags`, `syncTags`) is name-only end-to-end and Phase 2
  already hardcodes that value to `""`. Encoding the label's value into the
  tag name keeps this phase additive — no changes to `Client.Tags`/
  `CreateTag`/`SetMonitorTags`'s signatures, no new drift-detection surface
  for a tag value. Label `team=platform` becomes a tag literally named
  `team=platform`.
- **No exclude/deny-list.** An allow-only regex list is precise enough on
  its own (a user who wants to exclude `pod-template-hash` simply never
  writes a pattern that matches it) — YAGNI for v1.
- **Union with existing tag sources, not override.** A monitor's final tag
  set is `DEFAULT_TAGS ∪ explicit tags (CRD/annotation) ∪ label-derived
  tags`, deduplicated by name — the same fully-declarative model Phase 2
  already established (a tag no longer in this union, including one whose
  source label was removed, is dropped from the monitor on the next
  reconcile).

## Configuration surface

New operator-wide setting, following `DEFAULT_TAGS`'s existing pattern in
`internal/config/config.go`:

| Env var | Helm value | Type | Behavior |
|---|---|---|---|
| `LABEL_TAG_PATTERNS` | `labelTagPatterns` | comma-separated list of regexes | Empty/unset (default): feature off. Non-empty: a source object's label is imported as a tag if its key fully matches at least one pattern. |

`internal/config.Load` (or equivalent) compiles every pattern at startup
via `regexp.Compile`, anchoring each with `^(?:pattern)$` before compiling.
**An invalid pattern fails operator startup** (same fail-loud posture as
any other misconfiguration) — never discovered mid-reconcile, so a bad
pattern can't intermittently break monitor syncing. `Config` gains
`LabelTagPatterns []*regexp.Regexp`.

Helm chart: add `labelTagPatterns: []` to `values.yaml`, template it into
`templates/deployment.yaml` as `LABEL_TAG_PATTERNS` (comma-joined) exactly
where `defaultTags`/`DEFAULT_TAGS` already is.

## Label → tag mapping

New pure helper, alongside the existing `mergeTags` in
`internal/controller/sync.go` (no Kubernetes or Kuma I/O, so it stays a
plain function like `mergeTags`, not a method on a client):

```go
// deriveLabelTags returns the Kuma tag names derived from labels: for each
// label whose key fully matches at least one pattern, "key=value" is
// included. Order is unspecified; callers that need determinism (e.g. for
// desiredHash) should sort the result — mergeTags already dedupes and its
// output feeds into the same hash-stability handling Phase 2 established
// for notification IDs.
func deriveLabelTags(labels map[string]string, patterns []*regexp.Regexp) []string
```

`mergeTags` (currently two-way: `defaults, specific`) generalizes to a
three-way union — either a variadic `mergeTags(sources ...[]string) []string`
or two chained calls (`mergeTags(mergeTags(defaults, labelTags), specific)`);
left to the implementation plan to pick based on what reads more clearly at
each call site. Dedup semantics are unchanged: same name from multiple
sources collapses to one entry.

## Reconciler wiring

Each of the three reconcilers (`monitor_controller.go`,
`ingress_controller.go`, `httproute_controller.go`) already computes the
tag set it passes to `syncTags` from `DefaultTags` + the resource's own
explicit tags. Phase 3 adds one more input at that same call site:

- **Ingress / HTTPRoute**: `deriveLabelTags(ing.Labels, cfg.LabelTagPatterns)`
  / `deriveLabelTags(route.Labels, cfg.LabelTagPatterns)`, read from the
  Ingress/HTTPRoute object's own `metadata.labels` — never the backing
  Service/Pod's labels. For a multi-host Ingress (one Ingress deriving
  several host monitors, existing Phase 1/2 behavior), the label set is
  read once from the Ingress and the same label-derived tags apply to every
  derived host — identical treatment to how `notifications`/`group`/`proxy`
  are resolved once per Ingress and applied to every derived monitor.
- **Monitor CRD**: `deriveLabelTags(mon.Labels, cfg.LabelTagPatterns)`, read
  from the Monitor resource's own `metadata.labels`.

No new RBAC is required — `metadata.labels` is part of objects the
operator's cached client already watches in full (Ingress, HTTPRoute,
Monitor); this adds no new API surface.

## Risks / operational notes (to document, not solve in code)

- **Tag proliferation and no garbage collection.** A pattern that matches a
  label with many distinct values (a bad choice, e.g. a timestamp-like
  label) creates one Kuma tag object per distinct value ever seen, and
  Kuma tags are global/shared across monitors — the operator will not
  auto-delete a tag just because no monitor currently uses it (mirroring
  why `syncTags` already only ever adds/removes *associations*, never
  deletes tag objects). This is called out in the README/Helm value
  description as the operator's responsibility to configure carefully, the
  same spirit as Phase 2's "tags are fully declarative" warning.
- **Kuma-side tag name limits are unverified.** Neither breml's client nor
  the operator enforces a length/charset limit on `Tag.Name` — a `key=value`
  string from a long, prefixed Kubernetes label key
  (`app.kubernetes.io/name=some-very-long-service-name`) could exceed
  whatever limit Kuma's own schema has. Needs the same empirical
  verification against a real Kuma instance that Phase 1/2 already did for
  other server-side behaviors (default tag color, credential echo-back) —
  flagged for the implementation plan, not decided here.

## Touch points

- `internal/config/config.go` (+ `_test.go`) — `LabelTagPatterns
  []*regexp.Regexp`, parsing/compiling `LABEL_TAG_PATTERNS`, startup
  failure on an invalid pattern.
- `internal/controller/sync.go` (+ `_test.go`) — `deriveLabelTags`;
  generalize `mergeTags` to a three-way union.
- `internal/controller/monitor_controller.go`,
  `ingress_controller.go`, `httproute_controller.go` (+ tests) — wire
  `deriveLabelTags(<object>.Labels, cfg.LabelTagPatterns)` into the tag set
  passed to `syncTags`, alongside the existing `DefaultTags`/explicit-tags
  union.
- `cmd/main.go` — thread `cfg.LabelTagPatterns` into each reconciler
  struct, same as `cfg.DefaultTags` today.
- `charts/uptime-kuma-operator/values.yaml`,
  `charts/uptime-kuma-operator/templates/deployment.yaml` — new
  `labelTagPatterns` value / `LABEL_TAG_PATTERNS` env var.
- `README.md`, `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`
  — document the new operator-wide setting and its proliferation/no-GC
  caveat.

## Testing

Following Phase 1/2's conventions:

- `internal/config`: parsing tests for `LABEL_TAG_PATTERNS` (valid
  patterns, invalid pattern → startup error, empty/unset → nil/disabled).
- `internal/controller`: unit tests for `deriveLabelTags` (anchoring —
  `team` does not match `my-team`; multiple patterns; no patterns
  configured → empty result; `key=value` naming, including an empty
  value); unit tests for the generalized three-way `mergeTags` (dedup
  across all three sources).
- One reconciler-level test per resource type (Ingress, HTTPRoute, Monitor
  CRD) proving a matching label reaches `FakeClient`'s tag state via
  `syncTags`, and one proving a non-matching label does not.

## Out of scope

- An exclude/deny-list on top of the allow-patterns (YAGNI per the
  decisions above; straightforward to add later without touching this
  design's shape if it turns out to be needed).
- Any per-resource override or opt-out of the operator-wide setting.
- Using Kuma's native per-tag-association *value* field — deferred until
  there's a concrete need that `key=value`-as-name can't satisfy.
