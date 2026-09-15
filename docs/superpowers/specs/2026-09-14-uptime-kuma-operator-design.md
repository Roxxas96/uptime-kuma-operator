# Uptime Kuma Operator — Design

Date: 2026-09-14
Status: Approved for implementation planning

## Purpose

A Kubernetes operator that keeps Uptime Kuma monitors in sync with cluster
state:

- Automatically create/update/delete HTTP(S) monitors from `Ingress` and
  Gateway API `HTTPRoute` resources.
- Allow manually declaring more complex monitor types (DNS, Gamedig, TCP,
  Ping) via a CRD, for cases Ingress/HTTPRoute can't express.
- Default to an opt-in annotation so it's safe to install in a shared
  cluster; support a "watch everything" mode with an opt-out escape hatch.
- Support watching a configured list of namespaces beyond its own.

## Non-goals

- Managing Uptime Kuma itself (notifications, status pages, users, proxies,
  maintenance windows) — only monitors.
- Supporting multiple Uptime Kuma instances from one operator deployment.
- TCP/UDP `Ingress`/`Service`-based monitor discovery (only `Ingress` and
  `HTTPRoute`, both HTTP(S)-oriented, are in scope for auto-discovery).

## Architecture

Single Go binary built on kubebuilder / controller-runtime, one `Deployment`.

Three controllers share one `internal/kuma.Client`, constructed once at
startup from operator-wide config:

- `IngressReconciler` — watches `networking.k8s.io/v1.Ingress`
- `HTTPRouteReconciler` — watches `gateway.networking.k8s.io.HTTPRoute`;
  only registered if the `HTTPRoute` CRD is present in the cluster (checked
  via the REST mapper at startup), so the operator doesn't crash-loop where
  Gateway API isn't installed.
- `MonitorReconciler` — watches the operator's own `Monitor` CRD

### Kuma client

Uptime Kuma has no REST API for monitor management; everything goes through
its Socket.IO protocol (`addMonitor`, `editMonitor`, `deleteMonitor`,
`login`, etc. — see
[Internal API wiki](https://github.com/louislam/uptime-kuma/wiki/Internal-API)).

We depend on
[`breml/go-uptime-kuma-client`](https://github.com/breml/go-uptime-kuma-client)
(MIT, supports HTTP/TCP/Ping/DNS/gRPC/Real Browser/etc.), wrapped behind our
own interface. **This requires the target Uptime Kuma instance to run 2.x** —
the library's own README states 1.x is not supported (different login
protocol, and 1.x's schema is missing columns the client unconditionally
sends on every `addMonitor` call, e.g. `bearer_token`, so monitor creation
fails outright against a 1.x server). The operator has no way to detect or
warn about this at runtime; it's an operational precondition.

```go
package kuma

type Client interface {
    Upsert(ctx context.Context, id int64, spec MonitorSpec) (newID int64, err error)
    Delete(ctx context.Context, id int64) error
    // ExistingIDs supports drift detection (see "Recreate on Kuma-side
    // deletion" in the decisions log) — a cheap, non-mutating list call
    // reconcilers use to check whether a tracked monitor still exists,
    // instead of an Upsert/editMonitor round-trip per candidate.
    ExistingIDs(ctx context.Context) (map[int64]bool, error)
}
```

Wrapping it means a future swap (if the library stalls or lacks a monitor
type we need, e.g. Gamedig edge cases) only touches this one seam, not every
controller. `Upsert` takes an existing ID (0 = create) — the implementation
uses `int64` (breml's own monitor ID type) rather than `string`, an
improvement made during implementation.

### Operator-wide configuration

Env vars, credentials from a mounted `Secret`:

```
KUMA_URL=https://kuma.example.com
KUMA_USERNAME / KUMA_PASSWORD   (from Secret)
WATCH_NAMESPACES=prod,staging    # empty/unset = operator's own namespace only
WATCH_ALL=false                  # true = watch every namespace (namespace scope only)
OPT_IN_BY_DEFAULT=false          # true = sync everything not explicitly opted out
```

`WATCH_ALL` and `OPT_IN_BY_DEFAULT` are deliberately independent: `WATCH_ALL`
only decides which namespaces the operator watches (and, via the Helm chart,
whether it gets a `ClusterRole` or per-namespace `Role`s). It has no bearing
on the annotation contract below — watching every namespace does not exempt
a resource from needing `uptime-kuma.io/enabled=true`. Only
`OPT_IN_BY_DEFAULT` controls that.

Changing `WATCH_NAMESPACES`, `WATCH_ALL`, or `OPT_IN_BY_DEFAULT` requires a
pod restart (no hot-reload watcher) — acceptable since this changes rarely.

Startup fails fast (non-zero exit, no retry) if Kuma credentials are
invalid — there's no point running controllers that can never sync.

## Annotation contract (Ingress & HTTPRoute)

| Annotation | Purpose |
|---|---|
| `uptime-kuma.io/enabled` | `"true"` / `"false"`. Governed by `OPT_IN_BY_DEFAULT`, not `WATCH_ALL`: opt-in mode (default) requires it; opt-out mode requires it only to exclude a resource. Absent: not synced (opt-in mode) / synced (opt-out mode). |
| `uptime-kuma.io/name` | Friendly monitor name override. Default: derived from `<namespace>/<name>/<host>`. |
| `uptime-kuma.io/scheme` | `http` or `https` override. HTTPRoute only — Ingress infers scheme from `spec.tls`. |
| `uptime-kuma.io/interval` | Seconds between checks. Optional, Kuma default otherwise. |
| `uptime-kuma.io/retry-interval` | Seconds between retries. Optional. |
| `uptime-kuma.io/max-retries` | Optional. |
| `uptime-kuma.io/accepted-statuscodes` | Comma-separated list. Optional. |
| `uptime-kuma.io/monitor-ids` | **Operator-written.** JSON map `{"host": "kumaMonitorID"}`. One Ingress/HTTPRoute can expand into multiple monitors (one per host). |
| `uptime-kuma.io/synced-hash` | **Operator-written.** SHA-256 fingerprint of the derived monitor set as of the last successful sync — lets the reconciler skip the Kuma round-trip entirely when nothing relevant changed. |

## Reconciliation flow — Ingress & HTTPRoute

Both controllers share this logic:

1. Fetch the resource. Not found → nothing to do (cleanup already happened
   via the finalizer path on delete).
2. Check the namespace is in the watched set (controller-level predicate,
   not per-reconcile logic) — this is `WATCH_ALL`/`WATCH_NAMESPACES`'s only
   role in this flow.
3. Compute `shouldSync` from `OPT_IN_BY_DEFAULT` (not `WATCH_ALL`):
   - `OPT_IN_BY_DEFAULT=true`: sync unless `uptime-kuma.io/enabled: "false"`.
   - `OPT_IN_BY_DEFAULT=false` (default): sync only if `uptime-kuma.io/enabled: "true"`.
4. If `!shouldSync` and `monitor-ids` is non-empty (previously synced, now
   opted out): delete every listed Kuma monitor, clear the annotation,
   remove the finalizer. Return.
5. If `shouldSync`:
   - Derive one `(host, scheme, path)` per host:
     - Ingress: one per `spec.rules[].host`; scheme `https` if the host
       appears in `spec.tls[].hosts`, else `http`.
     - HTTPRoute: one per `spec.hostnames[]`; scheme `https` by default,
       overridable per-resource via `uptime-kuma.io/scheme` (no Gateway/
       Listener inspection — out of scope, see clarifying Q&A).
   - Hash the derived set and compare against the `uptime-kuma.io/synced-hash`
     annotation from the last successful sync.
     - **If they differ** (a real config change): for each host,
       `kuma.Upsert(ctx, monitor-ids[host], spec)` — creates if no existing
       ID, updates in place otherwise. For hosts previously in `monitor-ids`
       but no longer present in the spec: `kuma.Delete`. Patch `monitor-ids`
       and `synced-hash` with the resulting state.
     - **If they match**, do NOT blindly stop: `kuma.Upsert` (Kuma's
       `editMonitor`) restarts a monitor's check timer even when the payload
       is byte-for-byte unchanged, so calling it on every reconcile —
       including ones triggered by something unrelated to this monitor's
       configuration (annotation churn from other tooling, an informer
       resync, an error-triggered requeue) — would mean the monitor never
       completes more than one check cycle (a real, shipped bug — see the
       decisions log). So instead: call `kuma.ExistingIDs` (one cheap,
       non-mutating list call) and check every host's tracked ID is still
       present.
       - All present → nothing to do. Requeue after `driftCheckInterval`
         (5 minutes) to check again later.
       - Any missing (deleted out-of-band, e.g. manually in the Kuma UI) →
         recreate only the missing host(s) (`kuma.Upsert(ctx, 0, spec)`),
         leaving every host that's still present completely untouched — no
         edit call for them, so the redundant-editMonitor bug can't recur
         here either. Patch `monitor-ids` with the updated map (`synced-hash`
         is unchanged, since the desired config itself didn't change).
   - Ensure finalizer `uptime-kuma.io/finalizer` is present in either case.
   - Requeue after `driftCheckInterval` on every successful path above, so
     drift is caught even when nothing on the Kubernetes side ever changes
     again.
6. On delete (finalizer path): read `monitor-ids`, delete every listed Kuma
   monitor, remove the finalizer.

## `Monitor` CRD (manual complex monitors)

```yaml
apiVersion: uptime-kuma.io/v1alpha1
kind: Monitor
metadata:
  name: game-server
  namespace: prod
spec:
  type: Gamedig             # HTTP | TCP | Ping | DNS | Gamedig
  name: "Prod Game Server"  # friendly name in Kuma; defaults to metadata.name
  interval: 60               # seconds, optional
  retries: 3                 # optional
  retryInterval: 60           # optional
  gamedig:                   # exactly one of these must be set, matching `type`
    host: game.example.com
    port: 27015
    game: csgo
  dns:
    host: example.com
    resolverServer: "1.1.1.1"
    resolveType: A
  tcp:
    host: db.example.com
    port: 5432
  ping:
    host: 10.0.0.5
  http:
    url: https://example.com/health
    method: GET
    acceptedStatusCodes: ["200-299"]
status:
  monitorID: "42"
  observedGeneration: 3
  conditions:
    - type: Ready
      status: "True"
      reason: Synced
      lastTransitionTime: "..."
```

- `spec.type` is a discriminator; a `+kubebuilder:validation:XValidation` CEL
  rule enforces that the matching sub-struct (`spec.dns`, `spec.gamedig`,
  etc.) is set and others are absent.
- `status.monitorID` is this CRD's ownership-tracking mechanism (the CRD
  equivalent of the `monitor-ids` annotation on Ingress/HTTPRoute) — natural
  fit since we own the schema.
- `status.observedGeneration` is the `synced-hash` annotation's CRD
  equivalent: the reconciler skips the Kuma round-trip when it already
  matches `.metadata.generation` (which, with a status subresource, only
  changes on real `.spec` edits — annotation/metadata churn doesn't bump it,
  so unlike Ingress/HTTPRoute this CRD doesn't need a content hash).
- Finalizer `uptime-kuma.io/finalizer` ensures the Kuma monitor is deleted
  before the CR is removed from etcd.
- Starts with HTTP, TCP, Ping, DNS, Gamedig. Additional types are additive
  (new sub-struct + CEL branch), no breaking changes to existing types.

### Reconciliation flow — `Monitor` CRD

1. Fetch the CR. Not found → nothing to do.
2. On delete (finalizer path): if `status.monitorID` is set, `kuma.Delete`
   it, then remove the finalizer.
3. If `status.monitorID` is set and `status.observedGeneration ==
   .metadata.generation`: call `kuma.ExistingIDs` and check the ID is still
   present.
   - Present → nothing to do; requeue after `driftCheckInterval`.
   - Missing (deleted out-of-band) → recreate: `kuma.Upsert(ctx, 0, spec)`.
4. Otherwise (spec changed, or recreating after drift):
   `kuma.Upsert(ctx, status.monitorID, spec)`, patch `status.monitorID`,
   `status.observedGeneration`, and the `Ready` condition, ensure finalizer
   present, requeue after `driftCheckInterval`.

Unlike Ingress/HTTPRoute, there's no opt-in/opt-out annotation logic here —
declaring the CR *is* the opt-in.

## RBAC

Namespace scope is an explicit allow-list (`WATCH_NAMESPACES`), not
cluster-wide by default — chosen deliberately over the more common
cluster-wide-ClusterRole pattern, to keep the operator's default footprint
namespace-scoped in a shared cluster.

This mirrors the design External Secrets Operator's own maintainers
converged on for the equivalent gap in their project
([external-secrets#6490](https://github.com/external-secrets/external-secrets/issues/6490)):
today ESO only ships fully-cluster-wide or single-namespace-scoped modes,
and the accepted proposal for "a predefined list of namespaces" is exactly
a `Role`+`RoleBinding` generated per watched namespace with cluster-scoped
reconcilers disabled — the same shape used here.

- **`WATCH_NAMESPACES` set, `WATCH_ALL=false` (or unset):** generate one
  `Role` + `RoleBinding` per listed namespace (Helm/kustomize templated),
  granting `get/list/watch/update/patch` on `ingresses`, `httproutes`, and
  `monitors` in that namespace.
- **`WATCH_ALL=true`:** generate a single `ClusterRole` +
  `ClusterRoleBinding` instead — at that point the operator is already
  watching everything, so cluster scope is the honest representation
  rather than templating potentially hundreds of per-namespace `Role`s.
- The `Monitor` CRD is cluster-defined (as all CRDs are) but instances are
  namespaced, consistent with the allow-list model.
- No RBAC is requested for `Gateway` resources — HTTPRoute scheme detection
  deliberately does not inspect the parent Gateway (see clarifying Q&A).

## Error handling

- Kuma Socket.IO session drops mid-reconcile → client reconnects and
  re-authenticates transparently; the reconcile call itself just returns an
  error, and controller-runtime requeues with exponential backoff.
- Monitor create/update failure:
  - `Monitor` CR: sets `Ready=False` with the Kuma error message in
    `status.conditions`.
  - Ingress/HTTPRoute: emits a k8s `Event` on the resource (the operator
    doesn't own their status), requeues.
- Startup fails fast (non-zero exit) on invalid Kuma credentials.

## Testing strategy

- **Unit tests** for pure derivation logic — Ingress/HTTPRoute →
  desired monitor spec(s), annotation parsing/precedence, CRD
  type-discriminator validation. No cluster needed.
- **`envtest`** (controller-runtime's fake API server) for reconcile-loop
  behavior: annotation round-tripping, finalizer lifecycle, opt-in/opt-out
  transitions, multi-host expansion/contraction — against a fake/mock
  `kuma.Client`.
- **One integration test** against a real Uptime Kuma instance (Docker /
  docker-compose in CI) exercising `breml/go-uptime-kuma-client` end-to-end
  for one HTTP and one DNS monitor — catches drift if that library or
  Kuma's protocol changes.

## Key decisions log (from design Q&A)

- Go + kubebuilder over Python/kopf or manual client-go, for ecosystem fit.
- Socket.IO client over direct DB writes, since DB writes are fragile
  across Kuma schema changes.
- Single Kuma instance via operator-wide config, not a multi-instance CRD —
  YAGNI for the stated use case.
- Rich annotation set (interval/retries/status-codes) from day one, not
  deferred.
- Full lifecycle sync: monitors are deleted when the source resource is
  deleted or opts out, tracked via annotation (Ingress/HTTPRoute) or
  `.status` (CRD) plus finalizers — not lookup-by-naming-convention, to
  avoid listing all Kuma monitors on every reconcile.
- Typed CRD spec with a `type` discriminator over a generic params map, for
  validation and discoverability.
- Configurable namespace allow-list over default-cluster-wide, with RBAC
  generated per-namespace (falling back to `ClusterRole` only in
  `WATCH_ALL` mode) — validated against External Secrets Operator's
  accepted design for the same problem.
- HTTPRoute scheme: one monitor per hostname, assume HTTPS, override via
  annotation — deliberately not resolving the parent Gateway/Listener, to
  avoid extra RBAC and re-reconcile complexity for a rare case.
- **Correction (post-implementation):** the initial design conflated
  `WATCH_ALL` (namespace scope) with the annotation opt-in/opt-out policy —
  `WATCH_ALL=true` silently also meant "sync everything by default." Split
  into two independent settings: `WATCH_ALL` now governs namespace scope
  only, and a new `OPT_IN_BY_DEFAULT` governs the annotation policy. Watching
  every namespace no longer implies exempting resources from the opt-in
  annotation.
- **Bug fix (post-implementation): Kuma was called on every reconcile,
  regardless of relevance.** All three reconcilers called `kuma.Client.Upsert`
  (Kuma's `editMonitor`) unconditionally, with no check for whether the
  desired configuration had actually changed since the last successful sync.
  `editMonitor` restarts the monitor's check timer on Kuma's side even when
  the payload is byte-for-byte identical — so any reconcile trigger unrelated
  to the monitor's own config (annotation churn from other tooling, an
  informer resync, an error-triggered requeue) reset that monitor's check
  cycle, and a resource reconciled more often than its configured interval
  would never complete more than one check. Fixed by tracking "last
  successfully synced state" and skipping the Kuma call when it's unchanged:
  a content hash in a new `synced-hash` annotation for Ingress/HTTPRoute
  (their desired state depends on both `.spec` and override annotations), and
  the standard `.status.observedGeneration` vs `.metadata.generation` idiom
  for the `Monitor` CRD (whose `.spec` alone determines desired state).
  Caught from a user report of a monitor "stuck at 1 check"; reproduced
  directly against a real Kuma instance (repeated identical `editMonitor`
  calls force immediate rechecks at the edit frequency, not the configured
  interval) before fixing.
- **Bug fix (post-implementation): `kuma.NewClient`'s deadlock fix broke
  every call after the first.** An earlier fix for a startup deadlock (see
  below) wrapped the connection context in `context.WithTimeout` and
  deferred its cancellation. `bremlkuma.New` retains that same context for
  the connection's entire lifetime (its background goroutines treat
  `ctx.Done()` as a shutdown signal, not just a connect-attempt deadline),
  so the deferred cancel — firing the moment `NewClient` returned —
  silently killed the connection right after a successful first use. Every
  subsequent `Upsert`/`Delete` call failed with "context canceled". Fixed by
  relying solely on `bremlkuma.WithConnectTimeout` (which bounds the connect
  attempt independently of the passed context) and no longer wrapping or
  canceling the caller's context at all. Caught by the integration test
  against a real Kuma instance — the reconciler-level tests all use
  `FakeClient` and never exercised this path.
- **Bug fix (post-implementation): initial connection could hang forever,
  crashing the process.** `cmd/main.go` called `kuma.NewClient` with
  `context.Background()` — no deadline. A stalled connection (bad URL,
  network partition, a proxy that mangles Socket.IO's long-polling/WebSocket
  upgrade) blocked `bremlkuma.New` forever on a `select` whose only escape
  hatches (`ctx.Done()`, an optional connect-timeout) were never wired up.
  Since this runs before the manager creates any other goroutines, the whole
  process was just 3 goroutines, all permanently blocked — which Go's
  runtime reports as `fatal error: all goroutines are asleep - deadlock!`
  and crashes on. Fixed by always passing `bremlkuma.WithConnectTimeout`
  (30s) in `kuma.NewClient` (see the entry above for the regression this
  introduced, and its fix). Caught from a user-reported crash in a real
  cluster; reproduced locally with a TCP listener that accepts connections
  but never responds.
- **Feature (post-implementation, expected behavior): recreate a monitor
  deleted directly in Kuma, if its source resource is still present.**
  Skipping the Kuma round-trip when nothing changed (the fix above) has a
  side effect: the operator would never notice a monitor deleted
  out-of-band (e.g. manually in the Kuma UI), since nothing on the
  Kubernetes side changes to re-trigger a reconcile. Fixed by adding
  `kuma.Client.ExistingIDs` (one `GetMonitors` list call — cheap and
  non-mutating, unlike a per-ID `GetMonitor`, whose only "not found" signal
  turned out to be Kuma's raw, version-fragile JS error string rather than
  a typed error) and a periodic drift check: on the "nothing changed" path,
  every reconciler now verifies its tracked ID(s) still exist before doing
  nothing, and requeues after `driftCheckInterval` (5 minutes) so it checks
  again later even if the Kubernetes resource is never touched again. A
  missing monitor is recreated with a fresh Upsert(id=0, ...); for
  Ingress/HTTPRoute (which can track several monitors per resource), only
  the missing host(s) are recreated — hosts still present are left
  completely untouched, so this can't reintroduce the redundant-editMonitor
  bug it's built next to. Verified `ExistingIDs` against a real Kuma
  instance (not just `FakeClient`) before relying on it.
