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
own interface:

```go
package kuma

type Client interface {
    Upsert(ctx context.Context, id string, spec MonitorSpec) (newID string, err error)
    Delete(ctx context.Context, id string) error
}
```

Wrapping it means a future swap (if the library stalls or lacks a monitor
type we need, e.g. Gamedig edge cases) only touches this one seam, not every
controller. `Upsert` takes an existing ID (empty string = create).

### Operator-wide configuration

Env vars, credentials from a mounted `Secret`:

```
KUMA_URL=https://kuma.example.com
KUMA_USERNAME / KUMA_PASSWORD   (from Secret)
WATCH_NAMESPACES=prod,staging    # empty/unset = operator's own namespace only
WATCH_ALL=false                  # true = sync everything not explicitly opted out
```

Changing `WATCH_NAMESPACES` or `WATCH_ALL` requires a pod restart (no
hot-reload watcher) — acceptable since this changes rarely.

Startup fails fast (non-zero exit, no retry) if Kuma credentials are
invalid — there's no point running controllers that can never sync.

## Annotation contract (Ingress & HTTPRoute)

| Annotation | Purpose |
|---|---|
| `uptime-kuma.io/enabled` | `"true"` / `"false"`. In default mode: opt in. In `WATCH_ALL` mode: opt out. Absent: not synced (default mode) / synced (watch-all mode). |
| `uptime-kuma.io/name` | Friendly monitor name override. Default: derived from `<namespace>/<name>/<host>`. |
| `uptime-kuma.io/scheme` | `http` or `https` override. HTTPRoute only — Ingress infers scheme from `spec.tls`. |
| `uptime-kuma.io/interval` | Seconds between checks. Optional, Kuma default otherwise. |
| `uptime-kuma.io/retry-interval` | Seconds between retries. Optional. |
| `uptime-kuma.io/max-retries` | Optional. |
| `uptime-kuma.io/accepted-statuscodes` | Comma-separated list. Optional. |
| `uptime-kuma.io/monitor-ids` | **Operator-written.** JSON map `{"host": "kumaMonitorID"}`. One Ingress/HTTPRoute can expand into multiple monitors (one per host). |

## Reconciliation flow — Ingress & HTTPRoute

Both controllers share this logic:

1. Fetch the resource. Not found → nothing to do (cleanup already happened
   via the finalizer path on delete).
2. Check the namespace is in the watched set (controller-level predicate,
   not per-reconcile logic).
3. Compute `shouldSync`:
   - `WATCH_ALL=true`: sync unless `uptime-kuma.io/enabled: "false"`.
   - `WATCH_ALL=false` (default): sync only if `uptime-kuma.io/enabled: "true"`.
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
   - For each host: `kuma.Upsert(ctx, monitor-ids[host], spec)` — creates if
     no existing ID, updates in place otherwise.
   - For hosts previously in `monitor-ids` but no longer present in the
     spec: `kuma.Delete`.
   - Patch `monitor-ids` with the resulting map. Ensure finalizer
     `uptime-kuma.io/finalizer` is present.
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
- Finalizer `uptime-kuma.io/finalizer` ensures the Kuma monitor is deleted
  before the CR is removed from etcd.
- Starts with HTTP, TCP, Ping, DNS, Gamedig. Additional types are additive
  (new sub-struct + CEL branch), no breaking changes to existing types.

### Reconciliation flow — `Monitor` CRD

1. Fetch the CR. Not found → nothing to do.
2. On delete (finalizer path): if `status.monitorID` is set, `kuma.Delete`
   it, then remove the finalizer.
3. Otherwise: `kuma.Upsert(ctx, status.monitorID, spec)`, patch
   `status.monitorID` and the `Ready` condition, ensure finalizer present.

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
