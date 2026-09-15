# Monitor Configuration Expansion — Phase 1 (direct-value fields)

Sub-project of the main design: `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`.
This doc covers only Phase 1. Phases 2 and 3 are scoped but not designed in
detail here (see "Deferred phases" below) — they get their own
brainstorming pass when we get to them.

## Goal

Expose more of Kuma's per-monitor configuration through both the Monitor
CRD and Ingress/HTTPRoute annotations: everything that is a plain,
non-secret, non-referential value in the underlying `breml/go-uptime-kuma-client`
library. Fields requiring credentials, or a Kuma-side name→ID lookup
(tags, notification channels, proxy, monitor groups), are explicitly out
of scope for this phase.

## Fields confirmed NOT supported by breml v0.4.2 — excluded entirely

Checked directly against the library source (`monitor.Base`,
`HTTPDetails`, `TCPPortDetails`, `PingDetails`, `DNSDetails`,
`GameDigDetails`):

- **IP Family** (IPv4/IPv6/Any selector) — does not exist for any of our
  5 monitor types (only exists on the unrelated Globalping monitor type).
- **"Save HTTP error/success response for notification"** — does not
  exist anywhere in the library.
- **"Response max length"** (body truncation for notifications) — does
  not exist anywhere in the library.
- **Ping "Numeric output"** — does not exist.
- **Ping "Max packets"** — does not exist. `PingDetails.PacketSize` exists
  but is a packet *size in bytes*, not a packet *count*. This is what
  "Packets size" in the request maps to; "Max packets" itself has no
  library support.

None of these can be implemented against this library version. If the
library adds them later, revisit.

## Key decisions

- **Annotations only exist for HTTP-applicable fields.** Ingress and
  HTTPRoute always derive HTTP-type monitors — there is no way to get a
  TCP/Ping/DNS/Gamedig monitor from routing resources. So TCP/Ping/DNS/
  Gamedig's new fields are **Monitor-CRD-only**; only common fields and
  HTTP-specific fields get a `uptime-kuma.io/*` annotation.
- **`ExpiryNotification` (cert) and `DomainExpiryNotification` are not
  hoisted to the common `MonitorSpec`.** breml doesn't put them on `Base`
  either — they're duplicated per-type fields in the library itself
  (`HTTPDetails`, `TCPPortDetails`, `PingDetails`, `DNSDetails`,
  `GameDigDetails` each declare their own `DomainExpiryNotification`; only
  HTTP and TCP have the certificate-only `ExpiryNotification`). We mirror
  that shape exactly rather than inventing a false commonality.
- **Gamedig's "Guess Port" is implemented as `GivenPortOnly bool`, not
  `GuessPort bool`.** breml's field is `GameDigGivenPortOnly`, default
  `false` (Kuma guesses the port). Every other new boolean field in this
  design is a plain `bool` whose Go zero value (`false`) already equals
  Kuma's own default — that's *why* no field needs `*bool` tri-state here.
  Naming this one `GuessPort` would invert that: our zero value (`false`)
  would mean "don't guess," the opposite of Kuma's real default, which is
  exactly the class of bug this zero-value convention exists to avoid. In
  the Kuma UI, "Guess Port" checked corresponds to `givenPortOnly: false`.
- **Gamedig's `Token` is reclassified as a credential and deferred to
  Phase 2**, alongside HTTP auth (bearer/basic/OAuth). It wasn't part of
  the original HTTP-auth secrets question, but it's a per-server auth
  token by nature — treating it as plaintext-in-spec while HTTP auth gets
  `SecretRef` treatment would be an inconsistent security posture. Flagging
  this explicitly since it's a scope call, not something you asked for —
  say so in spec review if you'd rather keep it in Phase 1 as plaintext.
- **`path` is not a `kuma.HTTPSpec`/CRD field at all.** It only makes
  sense for Ingress/HTTPRoute (Monitor CRD users already provide a full
  URL). It's consumed entirely inside `derive.IngressMonitors`/
  `derive.HTTPRouteMonitors` to build the derived URL.
- **Every new field must flow through `FromBremlMonitor` and
  `Equivalent`, not just `ToBremlMonitor`/CRD conversion.** Skipping this
  for any field means drift detection silently never notices or corrects
  it — a correctness gap, not a nice-to-have. This is a global constraint
  on every task in the implementation plan.
- **All new boolean fields are plain `bool`** (not `*bool`). breml's own
  `MarshalJSON` for every monitor type unconditionally sends every
  boolean field regardless of whether the caller touched it — there's no
  wire-level difference between "not set" and "set to false" to preserve.
  Zero value `false` already matches Kuma's off/default state for every
  boolean added here (verified per-field against breml's defaults).
- **`Timeout` on `kuma.PingSpec` is `int64` (zero = unset)**, translated
  to breml's `*int64` by passing `nil` when zero — same zero-as-sentinel
  convention already used for `Interval`/`RetryInterval`/`MaxRetries`
  everywhere else in this codebase, just crossing a pointer boundary for
  this one breml field.
- **No new field needs `normalizeSpec` default-substitution.** Every
  field below already has its Go zero value equal to what breml/Kuma
  itself defaults to when the field is omitted (confirmed per-field,
  not assumed) — unlike `AcceptedStatusCodes`, whose empty-slice zero
  value does *not* equal Kuma's real default (`["200-299"]"`) and
  therefore needs substitution. `MaxRedirects: 0` is not a new risk here
  either: it's what this operator has already been silently sending on
  every HTTP monitor since day one (Go zero-initializes `HTTPDetails`
  fields we never touched), so making it configurable doesn't change
  existing behavior for anyone who hasn't set it.

## Field list

Annotation keys are `uptime-kuma.io/<key>`. CRD fields use
`lowerCamelCase` JSON tags, following existing convention.

### Common (top-level `MonitorSpec`, every type)

| Field | Go type | Annotation | CRD field | Kuma mapping |
|---|---|---|---|---|
| Description | `string` | `description` | `description` | `Base.Description` (`""` → `nil`) |
| ResendInterval | `int64` | `resend-interval` | `resendInterval` | `Base.ResendInterval` |
| UpsideDown | `bool` | `upside-down` | `upsideDown` | `Base.UpsideDown` |

### HTTP (`kuma.HTTPSpec` / CRD `HTTPMonitorSpec`) — has annotations

| Field | Go type | Annotation | CRD field | Kuma mapping |
|---|---|---|---|---|
| Timeout | `int64` | `timeout` | `http.timeout` | `HTTPDetails.Timeout` |
| MaxRedirects | `int` | `max-redirects` | `http.maxRedirects` | `HTTPDetails.MaxRedirects` |
| IgnoreTLS | `bool` | `ignore-tls` | `http.ignoreTLS` | `HTTPDetails.IgnoreTLS` |
| CacheBust | `bool` | `cache-bust` | `http.cacheBust` | `HTTPDetails.CacheBust` |
| ExpiryNotification | `bool` | `expiry-notification` | `http.expiryNotification` | `HTTPDetails.ExpiryNotification` |
| DomainExpiryNotification | `bool` | `domain-expiry-notification` | `http.domainExpiryNotification` | `HTTPDetails.DomainExpiryNotification` |
| Headers | `string` | `headers` | `http.headers` | `HTTPDetails.Headers` (opaque passthrough, unvalidated — same as breml itself) |
| Body | `string` | `body` | `http.body` | `HTTPDetails.Body` (opaque passthrough) |
| *(path)* | — | `path` (Ingress/HTTPRoute only) | *n/a — CRD users set the full URL* | appended to the derived URL |

Method and AcceptedStatusCodes already exist; unchanged.

### TCP (`kuma.TCPSpec` / CRD `TCPMonitorSpec`) — CRD-only, no annotations

| Field | Go type | CRD field | Kuma mapping |
|---|---|---|---|
| TLSMode | `string` (`""` \| `nostarttls` \| `secure` \| `starttls`) | `tcp.tlsMode` | `TCPPortDetails.SMTPSecurity` (`""` → `nil`) |
| ExpectedSSLAlert | `string` | `tcp.expectedSSLAlert` | `TCPPortDetails.ExpectedTLSAlert` (`""` → `nil`) |
| ExpiryNotification | `bool` | `tcp.expiryNotification` | `TCPPortDetails.ExpiryNotification` |
| DomainExpiryNotification | `bool` | `tcp.domainExpiryNotification` | `TCPPortDetails.DomainExpiryNotification` |

### Ping (`kuma.PingSpec` / CRD `PingMonitorSpec`) — CRD-only

| Field | Go type | CRD field | Kuma mapping |
|---|---|---|---|
| Timeout | `int64` | `ping.timeout` | `PingDetails.Timeout` (`0` → `nil`) |
| PacketSize | `int` | `ping.packetSize` | `PingDetails.PacketSize` |
| DomainExpiryNotification | `bool` | `ping.domainExpiryNotification` | `PingDetails.DomainExpiryNotification` |

### DNS (`kuma.DNSSpec` / CRD `DNSMonitorSpec`) — CRD-only

| Field | Go type | CRD field | Kuma mapping |
|---|---|---|---|
| DomainExpiryNotification | `bool` | `dns.domainExpiryNotification` | `DNSDetails.DomainExpiryNotification` |

(Nameserver/port/record type already exist as `ResolverServer`/`Port`/`ResolveType`.)

### Gamedig (`kuma.GamedigSpec` / CRD `GamedigMonitorSpec`) — CRD-only

| Field | Go type | CRD field | Kuma mapping |
|---|---|---|---|
| GivenPortOnly | `bool` | `gamedig.givenPortOnly` | `GameDigDetails.GameDigGivenPortOnly` |
| DomainExpiryNotification | `bool` | `gamedig.domainExpiryNotification` | `GameDigDetails.DomainExpiryNotification` |

(`Token` deferred to Phase 2 — see "Key decisions" above.)

## Touch points

- `api/v1alpha1/monitor_types.go` — new fields on `MonitorSpec` and each
  `*MonitorSpec`; regenerate deepcopy + CRD YAML (`make generate manifests`,
  which also runs `sync-chart-crds`).
- `internal/kuma/spec.go` — mirror the same new fields on `kuma.MonitorSpec`
  and its sub-specs.
- `internal/kuma/translate.go` — extend `ToBremlMonitor`, `FromBremlMonitor`,
  and `Equivalent` for every new field. `normalizeSpec` needs no changes
  per the "no default-substitution needed" decision above.
- `internal/controller/convert.go` — extend `toKumaSpec` (CRD → `kuma.MonitorSpec`).
- `internal/annotations/annotations.go` — new annotation constants, extend
  `Overrides`, extend `ParseOverrides`, add a `parseBoolAnnotation` helper
  (mirroring the existing `parseIntAnnotation`).
- `internal/derive/ingress.go` and `internal/derive/httproute.go` — apply
  the new `Overrides` fields (common + HTTP-specific + `path`) when
  building the derived `kuma.MonitorSpec`.
- CRD sample (`config/samples/uptime-kuma_v1alpha1_monitor.yaml`) and
  README annotation table — document the new fields.

## Testing

Following the pattern already established in this codebase:

- `internal/annotations`: parse tests per new annotation (valid, invalid,
  absent → zero value), including the new `parseBoolAnnotation` helper.
- `internal/kuma`: extend `TestFromBremlMonitor_RoundTripsThroughToBremlMonitor`
  and `TestEquivalent_DetectsDrift`-style tests to cover every new field —
  a field that round-trips through `ToBremlMonitor`→(JSON)→`FromBremlMonitor`
  and isn't `Equivalent`-compared is a silent drift-detection gap.
- `internal/controller`: extend `toKumaSpec` conversion tests; at least one
  reconciler-level test asserting a new field actually reaches the
  `FakeClient`'s stored `MonitorSpec` (not just that reconcile succeeds).
- `internal/derive`: extend tests for the new HTTP-applicable annotations
  and `path`.

## Deferred phases (not designed here)

- **Phase 2 — referenced and credential fields.** Tags (name-based,
  auto-create missing), notification channels (name-based), proxy
  (ID-based — breml's `proxy.Proxy` has no `Name` field), monitor groups
  (name-based via `GetMonitors` + `Type()=="group"` filter, no library
  helper). HTTP auth (method already in Phase 1; `AuthMethod` +
  `BearerToken`/`BasicAuthUser`/`BasicAuthPassword`/OAuth fields via a
  Kubernetes `Secret` reference, Monitor-CRD-only). Gamedig `Token`, same
  `SecretRef` treatment.
- **Phase 3 — tag inference from resource labels.** Opt-in via an
  annotation (exact name TBD), with an optional filter the user can
  configure (prefix or explicit key allowlist) to avoid importing noisy
  labels like `pod-template-hash`; without a filter, all labels import.
  Depends on Phase 2's tag support existing first.
