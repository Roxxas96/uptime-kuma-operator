# Service-based monitor derivation with a type annotation

Sub-project of `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`,
sibling to the Phase 1–3 monitor-configuration-expansion docs (which only
ever touched `Ingress`/`HTTPRoute`, both permanently HTTP-only). This design
adds a fourth watched resource, `Service`, that can produce a monitor of
**any** Kuma type (HTTP/TCP/Ping/DNS/Gamedig), selected per-resource via a
new `uptime-kuma.io/type` annotation — closing the gap between the
annotation-driven workflow (previously HTTP-only) and the `Monitor` CRD
(all types, but requires writing a full custom resource).

## Goal

Let a user opt a plain `Service` into Kuma monitoring the same lightweight
way Ingress/HTTPRoute already work — annotations only, no CRD — while
supporting every monitor type the `Monitor` CRD supports. A `Service`
carries no routing/hostname information of its own, so (unlike Ingress/
HTTPRoute) the operator must synthesize a check target from the Service's
name/namespace/ports plus annotation overrides.

## Key decisions (from brainstorming with the user)

- **Default target is the in-cluster DNS name**
  (`<name>.<namespace>.svc.cluster.local`), overridable per type via a
  `.../host` annotation. This assumes Kuma can resolve in-cluster DNS
  (i.e. runs in- or reaches into the cluster's pod network) — reasonable
  for this operator's existing deployment model, and strictly more
  flexible than defaulting to `ClusterIP` (which breaks for headless
  Services).
- **Port selection**: auto-picked when the Service exposes exactly one
  port; a `.../port` annotation is *required* when it exposes more than
  one (by number or by `Service.spec.ports[].name`), and reconciliation
  fails with a clear error if it's missing or unresolvable. This applies
  to HTTP, TCP, and Gamedig, which all need a specific Service port; Ping
  has no port concept in Kuma, and DNS's `port` is the resolver's query
  port, unrelated to the Service's own ports (see below).
- **Missing `type` annotation defaults to HTTP.** Matches the Ingress/
  HTTPRoute precedent and is the common case for a plain Service; `type`
  only needs to be set for the four non-HTTP cases.
- **Ingress/HTTPRoute stay permanently HTTP** and simply never consult
  `type` or any non-`http/`-scoped annotation — no behavior change for
  either existing reconciler beyond the annotation rename below.
- **Secret-backed auth stays Monitor-CRD-only.** HTTP Basic/Bearer/OAuth2
  and the Gamedig token remain unavailable via annotation on any
  resource (Ingress, HTTPRoute, or Service) — same precedent Phase 2
  already established, since a `SecretKeySelector` has no sane
  single-annotation-value representation.
- **Breaking change, accepted.** The operator has no users yet, so this
  redesigns the annotation contract (see below) rather than growing it
  additively. Every existing Phase 1/2 annotation is renamed.

## Annotation contract: scoped by CRD sub-spec

The existing flat `uptime-kuma.io/<field>` annotations conflated "field
name" with "field meaning" — safe only because Ingress/HTTPRoute were
always HTTP. Introducing other types makes that ambiguous (`timeout` means
something different for HTTP vs. Ping; `domain-expiry-notification`
exists on all five types with independent values in the CRD). Annotations
now mirror `api/v1alpha1.MonitorSpec`'s own shape exactly: fields that live
at the top level of `MonitorSpec` stay unscoped; fields that live inside a
`Http`/`Tcp`/`Ping`/`Dns`/`Gamedig` sub-spec are scoped under
`<type>.uptime-kuma.io/...` — the type moves into the DNS-subdomain prefix,
not a second path segment, because a Kubernetes annotation key permits only
one `/` (separating the prefix from the name); `uptime-kuma.io/http/timeout`
is not a legal key. This is the same pattern well-known operators use for
their own annotation families (e.g. `cert-manager.io/*`).

### Top-level (unscoped) — mirrors `MonitorSpec`'s own fields

| Annotation | Meaning |
|---|---|
| `uptime-kuma.io/enabled` | opt in/out (unchanged) |
| `uptime-kuma.io/name` | monitor name override |
| `uptime-kuma.io/type` | `HTTP` (default) \| `TCP` \| `Ping` \| `DNS` \| `Gamedig` — **read only by the Service reconciler** |
| `uptime-kuma.io/interval` | check interval, seconds |
| `uptime-kuma.io/retry-interval` | retry interval, seconds |
| `uptime-kuma.io/max-retries` | max retries |
| `uptime-kuma.io/description` | shown in Kuma UI |
| `uptime-kuma.io/resend-interval` | failed-check resend cadence |
| `uptime-kuma.io/upside-down` | invert up/down |
| `uptime-kuma.io/notifications` | comma-separated notification channel names |
| `uptime-kuma.io/proxy` | Kuma proxy ID |
| `uptime-kuma.io/group` | parent group monitor name |
| `uptime-kuma.io/tags` | comma-separated tag names |

Plus the operator-internal `uptime-kuma.io/monitor-ids`,
`.../synced-hash`, `.../finalizer` (unchanged, not user-facing).

### `http.uptime-kuma.io/...` — mirrors `HTTPMonitorSpec`

`host`, `port` (both **new** — see below), `scheme`, `path`,
`accepted-statuscodes`, `timeout`, `max-redirects`, `ignore-tls`,
`cache-bust`, `expiry-notification`, `domain-expiry-notification`,
`headers`, `body`.

`host`/`port` have no `HTTPMonitorSpec` equivalent (the CRD takes a
literal `url` instead) — they're added here purely to synthesize the
derived URL (`scheme://host:port/path`) for a Service, which — unlike
Ingress/HTTPRoute — has no routing-rule-derived host of its own. Both
follow the same default/override/port-resolution rules as every other
type below.

### `tcp.uptime-kuma.io/...` — mirrors `TCPMonitorSpec`

`host`, `port`, `tls-mode`, `expected-ssl-alert`, `expiry-notification`,
`domain-expiry-notification`. `port` is resolved against
`Service.spec.ports` (auto-pick if one, required annotation if more than
one).

### `ping.uptime-kuma.io/...` — mirrors `PingMonitorSpec`

`host`, `timeout`, `packet-size`, `domain-expiry-notification`. No `port`
— ICMP has none.

### `dns.uptime-kuma.io/...` — mirrors `DNSMonitorSpec`

`host`, `port`, `resolver-server`, `resolve-type`,
`domain-expiry-notification`. **`port` here is a plain integer, parsed
like any other numeric annotation — it is never resolved against
`Service.spec.ports`.** It matches `DNSMonitorSpec.Port`'s own semantics:
the port used to query the resolver server, unrelated to whatever ports
the target Service happens to expose.

### `gamedig.uptime-kuma.io/...` — mirrors `GamedigMonitorSpec`

`host`, `port`, `game` (**required** when `type: Gamedig`),
`given-port-only`, `domain-expiry-notification`. `port` is resolved
against `Service.spec.ports`, same rule as HTTP/TCP.

## `Overrides` restructuring

`internal/annotations.Overrides` is restructured to mirror
`MonitorSpec`'s top-level-plus-sub-spec shape:

```go
type Overrides struct {
    Name, Type, Group string
    Interval, RetryInterval, MaxRetries, ResendInterval, Proxy int64
    Description string
    UpsideDown bool
    Notifications, Tags []string

    HTTP    HTTPOverrides
    TCP     TCPOverrides
    Ping    PingOverrides
    DNS     DNSOverrides
    Gamedig GamedigOverrides
}

type HTTPOverrides struct {
    Host, Port string // Port: literal number or Service port name; "" = auto
    Scheme, Path string
    AcceptedStatusCodes []string
    Timeout, MaxRedirects int64
    IgnoreTLS, CacheBust, ExpiryNotification, DomainExpiryNotification bool
    Headers, Body string
}

type TCPOverrides struct {
    Host, Port string
    TLSMode, ExpectedSSLAlert string
    ExpiryNotification, DomainExpiryNotification bool
}

type PingOverrides struct {
    Host string
    Timeout, PacketSize int64
    DomainExpiryNotification bool
}

type DNSOverrides struct {
    Host string
    Port int64 // plain integer; not resolved against Service ports
    ResolverServer, ResolveType string
    DomainExpiryNotification bool
}

type GamedigOverrides struct {
    Host, Port string
    Game string
    GivenPortOnly, DomainExpiryNotification bool
}
```

`ParseOverrides` parses all five sub-blocks unconditionally regardless of
resource kind — cheap (just annotation reads), and it means
`IngressMonitors`/`HTTPRouteMonitors` can keep reading `ov.HTTP.*`
directly without any resource-kind branching. `ov.Type` and every
non-`http/`-scoped annotation are simply never read by those two
reconcilers — inert, not an error, if present on an Ingress/HTTPRoute.

`httpMonitorSpec` (`internal/derive/httpspec.go`) is updated to read
`ov.HTTP.*` instead of the old flat `ov.*` fields; its signature
(`name, scheme, host string, ov annotations.Overrides`) is otherwise
unchanged. `IngressMonitors`/`HTTPRouteMonitors` call it exactly as
today.

## Reconciler wiring

New `internal/controller/service_controller.go`:

```go
type ServiceReconciler struct {
    Client             client.Client
    Kuma               kuma.Client
    OptInByDefault     bool
    Recorder           record.EventRecorder
    DriftCheckInterval time.Duration
    DefaultTags        []string
    LabelTagPatterns   []*regexp.Regexp
}
```

Structurally identical to `IngressReconciler`: `params()` builds a
`hostReconcilerParams`, `Reconcile` fetches a `corev1.Service` and calls
`reconcileHostBasedResource`, `SetupWithManager` watches `corev1.Service`.
No CRD-presence gate is needed (unlike `HTTPRouteReconciler`) — `Service`
is core API, always present.

**Shared-code change** in `internal/controller/host_reconciler.go`:
`reconcileHostBasedResource`'s `deriveMonitors` parameter changes from

```go
deriveMonitors func(annotations.Overrides) []derive.DesiredMonitor
```
to
```go
deriveMonitors func(annotations.Overrides) ([]derive.DesiredMonitor, error)
```

because `derive.ServiceMonitors` can fail (ambiguous port, missing
`gamedig/game`, unknown `type`) where `IngressMonitors`/`HTTPRouteMonitors`
never could. The error is routed through `recordSyncFailure` and returned,
exactly like the existing `resolveReferences` error handling a few lines
below the `deriveMonitors` call. `ingress_controller.go`/
`httproute_controller.go`'s closures are updated to return `nil` for the
error.

`cmd/main.go`: register `ServiceReconciler` unconditionally, same
construction pattern (and same config fields) as `IngressReconciler`.

RBAC (`charts/uptime-kuma-operator/templates/rbac.yaml`): add
`(dict "apiGroups" (list "") "resources" (list "services") "verbs" (list "get" "list" "watch" "update" "patch"))`
to the shared `$rules` list used by both the `ClusterRole` and
per-namespace `Role` branches.

No new CRD, no scheme registration (`corev1.Service` is already part of
`clientgoscheme.Scheme`), no config changes.

## `internal/derive/service.go`

```go
func ServiceMonitors(svc *corev1.Service, ov annotations.Overrides) ([]DesiredMonitor, error)
```

- `monType := ov.Type`, defaulting to `"HTTP"`; any other value is an
  error (`"service: unknown uptime-kuma.io/type %q"`).
- `resolveServicePort(svc *corev1.Service, override string) (int32, error)`
  — shared by HTTP/TCP/Gamedig: empty `override` + exactly one
  `svc.Spec.Ports` entry auto-picks it; empty `override` + more than one
  port is an error naming the required `<type>/port` annotation;
  non-empty `override` is matched first as a literal port number, then
  against `.Name`; no match is an error.
- Host per type defaults to `<name>.<namespace>.svc.cluster.local`,
  overridden by `ov.<Type>.Host`.
  - **HTTP**: port via `resolveServicePort(svc, ov.HTTP.Port)`; scheme
    defaults `"http"` (overridable via `ov.HTTP.Scheme`); path defaults
    `"/"`; calls `httpMonitorSpec(name, scheme, fmt.Sprintf("%s:%d", host, port), ov)`
    — passing `"host:port"` as the host argument needs no change to
    `httpMonitorSpec` itself, since it's interpolated directly into the
    URL string.
  - **TCP**: port via `resolveServicePort(svc, ov.TCP.Port)`; builds
    `kuma.TCPSpec` from `ov.TCP.*`.
  - **Ping**: no port; builds `kuma.PingSpec` from `ov.Ping.*`.
  - **DNS**: builds `kuma.DNSSpec` from `ov.DNS.*`, `Port` taken directly
    as a plain integer (no Service-port resolution).
  - **Gamedig**: errors immediately if `ov.Gamedig.Game == ""`; port via
    `resolveServicePort(svc, ov.Gamedig.Port)`; builds `kuma.GamedigSpec`
    from `ov.Gamedig.*`.
- Common fields set on the outer `kuma.MonitorSpec` regardless of type:
  `Name` (defaulting to `<namespace>/<name>`), `Interval`,
  `RetryInterval`, `MaxRetries`, `Description`, `ResendInterval`,
  `UpsideDown`, `ProxyID` — same set `toKumaSpec` already applies
  uniformly for the `Monitor` CRD. `NotificationIDs`/`GroupID` are left
  zero; `reconcileHostBasedResource` fills them in after calling
  `deriveMonitors`, identical to the existing Ingress/HTTPRoute flow.
- Always returns exactly one `DesiredMonitor` (one check per Service),
  `Host` set to the resolved target host — used as the map key in the
  `monitor-ids` annotation, same mechanism Ingress/HTTPRoute already use
  for their (there, potentially multiple) derived monitors.

## Touch points

- `internal/annotations/annotations.go` (+ `_test.go`) — restructure
  `Overrides` into the top-level-plus-sub-spec shape above; rename every
  existing annotation constant into its scoped form; add `type`,
  `http/host`, `http/port`, `tcp/*`, `ping/*`, `dns/*`, `gamedig/*`.
- `internal/derive/httpspec.go` — `httpMonitorSpec` reads `ov.HTTP.*`
  instead of flat `ov.*`.
- `internal/derive/service.go` (new, + `_test.go`) — `ServiceMonitors`,
  `resolveServicePort`.
- `internal/controller/host_reconciler.go` — `deriveMonitors` gains an
  error return.
- `internal/controller/ingress_controller.go`,
  `httproute_controller.go` (+ tests) — closures updated for the new
  `deriveMonitors` signature (return `nil` error); no behavior change.
- `internal/controller/service_controller.go` (new, + `_test.go`) —
  `ServiceReconciler`, mirroring `IngressReconciler`.
- `cmd/main.go` — construct and register `ServiceReconciler`.
- `charts/uptime-kuma-operator/templates/rbac.yaml` — add `services` to
  the shared RBAC rule list.
- `README.md`, `docs/README.md` — document `Service` support, the full
  scoped-annotation table, port-selection rules, and the breaking
  rename of every Phase 1/2 annotation.

## Testing

- `internal/annotations`: parsing tests for the restructured
  `Overrides` — each sub-block's fields, defaults, and the `type`
  enum validation.
- `internal/derive`: `ServiceMonitors` — one case per type; port
  resolution (single-port auto-pick, multi-port requires annotation,
  lookup by name vs. number, unresolvable override); unknown `type`
  error; missing `gamedig/game` error; `http`'s URL synthesis
  (`host:port` + scheme + path).
- `internal/controller`: `service_controller_test.go`, envtest-backed,
  mirroring `ingress_controller_test.go` — opt-in/out, drift correction,
  finalizer/deletion, each monitor type end-to-end against `kuma.Fake`.
- Update existing `ingress_controller_test.go`, `httproute_controller_test.go`,
  `annotations_test.go`, `convert_test.go` call sites for the renamed/
  restructured annotation constants.

## Out of scope

- Secret-backed HTTP auth / Gamedig token via annotation on any
  resource — stays `Monitor`-CRD-only (existing Phase 2 precedent).
- Multiple monitors per Service (e.g. one per exposed port) — one
  Service always yields exactly one monitor; a Service needing several
  checks should use multiple `Monitor` CRs or split into multiple
  Services.
- Scoping `enabled`/`monitor-ids`/`synced-hash`/`finalizer` — these are
  resource-level/operator-internal, not `MonitorSpec` fields, so they
  stay unscoped regardless of type.
