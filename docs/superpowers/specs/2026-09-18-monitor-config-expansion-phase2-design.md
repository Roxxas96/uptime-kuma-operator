# Monitor Configuration Expansion — Phase 2 (referenced fields + credentials)

Sub-project of `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`,
following on from `docs/superpowers/specs/2026-09-15-monitor-config-expansion-phase1-design.md`
("Phase 1: direct-value fields"). This doc covers Phase 2: fields that
require resolving a Kuma-side name to an ID, and HTTP/Gamedig credentials.
Phase 3 (tag inference from resource labels) is out of scope here.

## Goal

Add tags, notification channels, a proxy, and a parent group to every
monitor type, plus HTTP Basic/Bearer/OAuth2-Client-Credentials auth and
Gamedig's `token`, with credentials sourced from Kubernetes Secrets — never
plaintext.

## Key architectural insight

Of the four "referenced" categories, three fit the existing pipeline
unchanged once resolved to an ID:

- **Notifications** and **group** resolve a name to an ID *once*, before
  the monitor spec is built; the ID then flows through `ToBremlMonitor`/
  `FromBremlMonitor`/`Equivalent` exactly like `Description` or any other
  Phase 1 field, because `monitor.Base.NotificationIDs []int64` and
  `monitor.Base.Parent *int64` are already part of the normal
  create/update payload.
- **Proxy** needs no resolution at all — breml's `proxy.Proxy` has no
  `Name` field, so the user supplies the numeric Kuma proxy ID directly,
  and it flows straight through as `Base.ProxyID *int64`.
- **Tags** are the exception: Kuma manages monitor-tag associations via
  dedicated `AddMonitorTag`/`DeleteMonitorTagWithValue` calls, entirely
  outside a monitor's own create/update payload. `Equivalent` cannot cover
  them; they need their own post-`Upsert` reconciliation step and their
  own drift check.

Name resolution (notifications, group) happens in the reconciler layer,
which already holds a `kuma.Client` — never inside `ToBremlMonitor`/
`FromBremlMonitor`/`Equivalent`, which stay pure functions with no I/O.

## Scope decisions

- **HTTP auth methods**: this phase implements **Basic, Bearer, and OAuth2
  Client Credentials** only. breml also models NTLM and mTLS (`TLSCert`/
  `TLSKey`/`TLSCa`/`AuthDomain`/`AuthWorkstation`), which need certificate
  material handling beyond a single secret key — deferred to a later
  round, not designed here.
- **Credentials are per-field `corev1.SecretKeySelector`s**, not one
  shared Secret with well-known keys — confirmed with the user. Each
  credential-bearing field gets its own `{name, key}` reference, so
  different credentials can live in different Secrets if desired.
- **Missing notification name or missing group name is a hard error**
  (confirmed with the user for groups; applied consistently to
  notifications for the same reason: a silently-skipped reference is
  worse than a loud, retryable reconcile failure). Tags are the only
  by-name reference that auto-creates on miss — that was decided in
  Phase 1's brainstorming specifically for tags, not extended here to
  notifications/groups.
- **No cross-reconcile caching** of name→ID lookups. `GetTags`/
  `GetNotifications`/`GetMonitors` are list calls (not per-item), same
  cost profile as `ExistingSpecs`, which is already called uncached on
  every drift check. Keep this phase consistent with that.
- **RBAC**: the manager's cached client needs `get;list;watch` on
  `secrets` in every watched namespace — `get` alone isn't enough because
  the standard controller-runtime client caches every type it reads.
  Same per-namespace-`Role`-vs-`ClusterRole` split the chart already uses
  for everything else.

## New `kuma.Client` interface methods

Following the existing wrapping principle (the controller layer only
speaks in the operator's own types, never breml's), the "no name-based
lookup" complexity for tags/notifications/groups lives entirely inside
`internal/kuma`:

```go
type Client interface {
	// ... existing methods unchanged ...

	// Tags returns every existing tag's name -> ID.
	Tags(ctx context.Context) (map[string]int64, error)
	// CreateTag creates a new tag with a default color and returns its ID.
	CreateTag(ctx context.Context, name string) (int64, error)
	// SetMonitorTags reconciles monitorID's tag associations to be exactly
	// tagIDs — adding missing ones and removing any not in the set. Callers
	// resolve names to IDs (via Tags/CreateTag) before calling this.
	SetMonitorTags(ctx context.Context, monitorID int64, tagIDs []int64) error

	// Notifications returns every existing notification channel's name -> ID.
	Notifications(ctx context.Context) (map[string]int64, error)

	// FindGroup returns the ID of the group-type monitor named name, or
	// found=false if no such group exists.
	FindGroup(ctx context.Context, name string) (id int64, found bool, err error)
}
```

`realClient` implements these via breml's `GetTags`/`CreateTag`/
`AddMonitorTag`/`GetMonitorTags`/`DeleteMonitorTagWithValue` (tags),
`GetNotifications` (notifications — no error return in breml, wrap
accordingly), and `GetMonitors` filtered to `Type() == "group"` (group).
`FakeClient` gets an in-memory equivalent of all four for reconciler
tests, following the same pattern as its existing `Monitors` map.

Default tag color for auto-created tags: verify against a real Kuma
instance during implementation (same empirical-verification approach used
in Phase 1) what a reasonable default is; a neutral color the Kuma UI
itself would offer is the target, not an arbitrary hex value invented
here.

## Field list

### Common (top-level `MonitorSpec`, every type) — has annotations, same as Phase 1's common fields

| Field | Annotation | CRD field | Kuma mapping |
|---|---|---|---|
| Tags | `tags` (comma-separated names) | `tags []string` | resolved via `Tags`/`CreateTag`, applied via `SetMonitorTags` after `Upsert` |
| Notifications | `notifications` (comma-separated names) | `notifications []string` | resolved via `Notifications` into `Base.NotificationIDs` before `Upsert` |
| Proxy | `proxy` (numeric ID) | `proxy int64` | `Base.ProxyID` directly, no resolution |
| Group | `group` (name) | `group string` | resolved via `FindGroup` into `Base.Parent` before `Upsert`; error if not found |

`kuma.MonitorSpec` holds only **resolved** values, never raw names — it
represents ready-to-translate desired state, and `ToBremlMonitor`/
`FromBremlMonitor`/`Equivalent` must not need to know about a name/ID
distinction. It gains `NotificationIDs []int64`, `GroupID *int64`, and
`ProxyID *int64`. Tag names never enter `kuma.MonitorSpec` at all (see
"Tag reconciliation" below — they're not part of the translate.go
pipeline).

Raw user input (names, not IDs) lives one layer up, in
`annotations.Overrides` (`Tags []string`, `Notifications []string`,
`Group string` — parsed from comma-separated annotation values) and in
the CRD's `MonitorSpec` (`tags []string`, `notifications []string`,
`group string`, `proxy int64`). The reconciler resolves
`Overrides.Notifications`/CRD `notifications` and `Overrides.Group`/CRD
`group` into `NotificationIDs`/`GroupID` via `kuma.Client.Notifications`/
`kuma.Client.FindGroup` *before* constructing the `kuma.MonitorSpec` it
passes into `syncMonitors`/`reconcileDrift`/`Upsert`. `proxy`'s numeric ID
needs no resolution and copies straight into `ProxyID`.

`Equivalent` gains `ProxyID` and `GroupID` (plain pointer comparison,
consistent with how other optional fields already compare) and
`NotificationIDs` (order-independent set comparison, e.g. via a
sorted-slice or map comparison — Kuma does not guarantee order) to its
top-level comparison. Tags are compared and reconciled entirely outside
`Equivalent`, in a dedicated step described below.

### HTTP-specific (CRD-only — a `SecretKeySelector` has no sane annotation representation)

| Field | CRD field | Kuma mapping |
|---|---|---|
| AuthMethod | `http.authMethod` (enum: `""`, `basic`, `bearer`, `oauth2-cc`) | `HTTPDetails.AuthMethod` |
| BasicAuthUsername | `http.basicAuthUsername` (plain string) | `HTTPDetails.BasicAuthUser` |
| BasicAuthPasswordSecretRef | `http.basicAuthPasswordSecretRef` (`corev1.SecretKeySelector`) | `HTTPDetails.BasicAuthPass` |
| BearerTokenSecretRef | `http.bearerTokenSecretRef` (`corev1.SecretKeySelector`) | `HTTPDetails.BearerToken` |
| OAuthClientID | `http.oauthClientID` (plain string) | `HTTPDetails.OAuthClientID` |
| OAuthClientSecretRef | `http.oauthClientSecretRef` (`corev1.SecretKeySelector`) | `HTTPDetails.OAuthClientSecret` |
| OAuthTokenURL | `http.oauthTokenURL` (plain string) | `HTTPDetails.OAuthTokenURL` |
| OAuthScopes | `http.oauthScopes` (plain string) | `HTTPDetails.OAuthScopes` |
| OAuthAudience | `http.oauthAudience` (plain string) | `HTTPDetails.OAuthAudience` |

Only the credential itself is a `SecretKeySelector`; usernames, client
IDs, token URLs, scopes, and audience are not secret and stay plain CRD
fields, matching how e.g. `kubernetes.io/basic-auth` Secrets treat
`username` as non-sensitive metadata alongside a sensitive `password`.

`kuma.HTTPSpec` gains the resolved secret VALUES (`BasicAuthPassword`,
`BearerToken`, `OAuthClientSecret` — plain strings, already-read secret
content), not the `SecretKeySelector`s themselves — the CRD layer resolves
the Secret via the Kubernetes API before building `kuma.MonitorSpec`,
mirroring how notification/group names are resolved via the Kuma API
before building it. `kuma.HTTPSpec` and `internal/kuma` package stay
free of any Kubernetes API dependency, consistent with today's design.

**Drift detection caveat**: once resolved, secret values ARE plain
strings in `kuma.HTTPSpec` and DO flow through `Equivalent`'s existing
`reflect.DeepEqual` comparison (Phase 1's fix) like any other HTTP field —
this means the secret's live value is held in memory during reconcile
(never logged, never written to an annotation or status field) and
compared byte-for-byte against what Kuma reports back. This is standard
practice for controllers that resolve Secrets (e.g. cert-manager) but is
called out explicitly here since it's new to this operator.

### Gamedig-specific (CRD-only)

| Field | CRD field | Kuma mapping |
|---|---|---|
| TokenSecretRef | `gamedig.tokenSecretRef` (`corev1.SecretKeySelector`) | `GameDigDetails.GameDigToken` (`*string`, nil when unset) |

## RBAC

Extend the Helm chart's per-namespace `Role` and cluster-wide
`ClusterRole` templates (`charts/uptime-kuma-operator/templates/rbac.yaml`)
with:

```yaml
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
```

Scoped identically to how the existing rules are scoped (per-namespace
`Role`s when `watchAll=false`, a single `ClusterRole` when `watchAll=true`).
A Secret referenced by a Monitor CR or via an Ingress/HTTPRoute annotation
must live in the **same namespace** as the referencing resource — no
cross-namespace secret access, matching standard Kubernetes security
practice and avoiding a namespace-boundary bypass.

## Tag reconciliation (the one genuinely new mechanism)

On every successful path through a reconciler (after `Upsert` creates or
updates the monitor, and on the "nothing changed" drift-check path too —
tags can drift out-of-band exactly like any other field):

1. Resolve `TagNames` to IDs: call `kc.Tags(ctx)`, and for any name not
   present, `kc.CreateTag(ctx, name)`.
2. Call `kc.SetMonitorTags(ctx, monitorID, resolvedIDs)` — the `kuma`
   package's `realClient` implementation diffs against
   `GetMonitorTags(ctx, monitorID)` and calls `AddMonitorTag`/
   `DeleteMonitorTagWithValue` only for the delta, so an unchanged tag set
   costs one cheap cache read and zero mutating calls — preserving the
   same "don't call Kuma when nothing changed" discipline the whole
   drift-detection design already depends on.

This runs as an explicit extra step in each reconciler (Ingress,
HTTPRoute, Monitor CRD) after the existing sync logic, not inside
`syncMonitors`/`reconcileDrift`, since tag reconciliation needs the
monitor's ID (available only after `Upsert`) and operates per-monitor
rather than per-desired-spec.

## Touch points

- `internal/kuma/client.go` — new `Client` interface methods (`Tags`,
  `CreateTag`, `SetMonitorTags`, `Notifications`, `FindGroup`) and
  `realClient` implementations.
- `internal/kuma/fake.go` — in-memory equivalents for reconciler tests.
- `internal/kuma/spec.go` — `MonitorSpec` gains `NotificationIDs []int64`,
  `GroupID *int64`, `ProxyID *int64` (resolved values only, per "Key
  architectural insight" above). `HTTPSpec` gains `AuthMethod`,
  `BasicAuthUsername`, `BasicAuthPassword`, `BearerToken`, `OAuthClientID`,
  `OAuthClientSecret`, `OAuthTokenURL`, `OAuthScopes`, `OAuthAudience`
  (all resolved plain values). `GamedigSpec` gains `Token`.
- `internal/kuma/translate.go` — extend `ToBremlMonitor`/
  `FromBremlMonitor`/`Equivalent` for `ProxyID`, resolved
  `NotificationIDs`, resolved `Parent`, and the new HTTP auth fields.
  `FromBremlMonitor` reading secret fields back from Kuma needs care:
  Kuma's API does return the stored `bearer_token`/`basic_auth_pass`/
  `oauth_client_secret` values in `getMonitor`/`getMonitorList` responses
  (confirm this empirically against a real instance before relying on it
  for drift detection — if Kuma masks these server-side, `Equivalent`
  would see a permanent mismatch and re-`Upsert` every drift check,
  reintroducing the redundant-`editMonitor` bug; this needs the same kind
  of empirical check Phase 1 did for zero-valued fields).
- `api/v1alpha1/monitor_types.go` — new common fields, new HTTP auth
  fields (including `corev1.SecretKeySelector` import), new Gamedig field.
- `internal/annotations/annotations.go` — `tags`, `notifications`,
  `proxy`, `group` annotations and `Overrides` fields (no annotation
  surface for HTTP auth or Gamedig token — CRD-only, per the "no sane
  annotation representation for a SecretKeySelector" decision above).
- `internal/controller/convert.go` — `toKumaSpec` populates the
  CRD-only fields' resolved forms once resolution is added at the
  reconciler layer (this file itself likely stays as a pure struct-copy
  function; the actual Kuma-API and Kubernetes-API resolution calls
  belong in the reconcilers, which already hold both a `kuma.Client` and
  a controller-runtime `client.Client` for reading Secrets).
- `internal/controller/ingress_controller.go`, `httproute_controller.go`,
  `monitor_controller.go` — add the name/Secret resolution step before
  building the final spec, and the post-`Upsert` tag reconciliation step.
- `charts/uptime-kuma-operator/templates/rbac.yaml` — `secrets` RBAC rule.
- `config/samples/`, README, main design spec's annotation table — document
  the new fields.

## Testing

Same conventions as Phase 1: `FakeClient` extended with the new methods
for reconciler-level tests; `internal/kuma` round-trip tests for every new
translate.go field; a reconciler test proving tag add/remove reconciles
correctly against `FakeClient`'s tag state; a reconciler test proving a
referenced Secret's value reaches the (fake) Kuma client without ever
appearing in a log line, annotation, or status field (grep the test's
captured log output for the secret value, asserting absence). Real-Kuma
empirical verification (per the two callouts above: default tag color,
and whether Kuma echoes back credential fields) before trusting either in
the implementation plan.

## Suggested execution waves (one plan, sequential)

1. **Notifications + proxy + group** — all three fit the existing
   resolve-then-flow-through-Equivalent pattern; lowest risk, ships the
   "easy" 3 of 4 referenced categories.
2. **Tags** — the new `SetMonitorTags` mechanism and its own
   reconciliation step.
3. **HTTP auth + Gamedig token** — Secret resolution, RBAC, and the two
   empirical Kuma checks (tag default color already covered in wave 2;
   credential echo-back specific to this wave).
