# Uptime Kuma Operator — User Guide

The Uptime Kuma Operator watches your cluster's `Ingress`, `HTTPRoute`,
and `Service` resources — and a `Monitor` custom resource for full manual
control — and keeps matching [Uptime Kuma](https://github.com/louislam/uptime-kuma)
monitors in sync automatically. Point it at your Kuma instance, annotate
what you want monitored, and stop clicking "Add Monitor" by hand.

This guide covers day-to-day use. For the internal design and
implementation history, see [`docs/superpowers/`](superpowers/).

## Contents

- [How it works](#how-it-works)
- [Prerequisites](#prerequisites)
- [Installing](#installing)
- [Choosing what gets watched](#choosing-what-gets-watched)
- [Monitoring Ingress and HTTPRoute](#monitoring-ingress-and-httproute)
- [Watching Services](#watching-services)
- [The Monitor CRD](#the-monitor-crd)
- [Tags, notifications, groups, and proxies](#tags-notifications-groups-and-proxies)
- [Configuration reference](#configuration-reference)
- [Operations](#operations)
- [Uninstalling](#uninstalling)
- [Troubleshooting](#troubleshooting)

## How it works

The operator runs as a single Deployment inside your cluster and watches
three kinds of resources:

- **`Ingress`** — one Kuma HTTP(S) monitor per distinct host in the
  Ingress's rules.
- **`HTTPRoute`** (Gateway API) — one Kuma HTTP(S) monitor per distinct
  hostname, if the HTTPRoute CRD is installed in your cluster. If it
  isn't, the operator simply skips this watcher — no error, no crash loop.
- **`Monitor`** — a CRD this chart installs, for monitor types routing
  can't express: DNS, TCP, Ping, and Gamedig, plus HTTP monitors that need
  more control (auth, notifications, groups, proxies) than an annotation
  can carry.

Every reconcile is fully declarative: whatever a resource's spec and
annotations describe is what ends up in Kuma, including removing monitors
for hosts you've deleted and clearing tags you've removed. Nothing you do
directly in the Kuma UI to a managed monitor survives the next reconcile
of its source resource.

Two things happen automatically that are worth knowing about upfront:

- **Drift correction.** If a managed monitor is deleted or hand-edited
  directly in Kuma, the operator notices and corrects it — at the latest
  `driftCheckInterval` (default `30s`) after the *next* reconcile of its
  source resource, even if nothing else changed.
- **Cleanup on delete.** Deleting an Ingress, HTTPRoute, or Monitor
  deletes every Kuma monitor it owns. This is enforced with a Kubernetes
  finalizer, so the delete won't fully complete until the operator has
  confirmed the Kuma side is cleaned up.

## Prerequisites

- A Kubernetes cluster (1.23+) and Helm 3.
- A running **Uptime Kuma 2.x** instance the operator can reach over the
  network. (1.x is not supported — different login/API schema.)
- Kuma credentials for an account the operator can use.

## Installing

1. Store your Kuma credentials in a Secret — the chart never takes them
   as plain values:

   ```bash
   kubectl create secret generic kuma-credentials \
     --from-literal=username=admin --from-literal=password=<password>
   ```

2. Install the chart. The `Monitor` CRD ships in the chart itself, so
   Helm installs it automatically. Published releases are available as an
   OCI artifact — no clone needed:

   ```bash
   helm install uptime-kuma-operator oci://ghcr.io/roxxas96/uptime-kuma-operator/charts/uptime-kuma-operator \
     --version 0.1.0 \
     --set kuma.url=https://kuma.example.com \
     --set kuma.existingSecret=kuma-credentials \
     --set watchNamespaces={default}
   ```

   (Or `charts/uptime-kuma-operator` in place of the `oci://...` URL if
   you're installing from a checkout of this repo instead.)

That's a working install. Everything else below is configuration you can
layer on as needed — see the [chart README](../charts/uptime-kuma-operator/README.md)
for the full list of Helm values (resource limits, security context,
scheduling, optional NetworkPolicy, etc).

## Choosing what gets watched

Two independent settings control what the operator acts on — mixing them
up is the single most common source of "why isn't my monitor showing up":

| Setting | Controls | Values |
|---|---|---|
| `watchAll` / `watchNamespaces` | *Which namespaces* the operator even looks at | `watchAll=true` watches the whole cluster (uses a `ClusterRole`); otherwise only `watchNamespaces` (a per-namespace `Role`), defaulting to just the release namespace |
| `optInByDefault` + the `uptime-kuma.io/enabled` annotation | *Which resources*, within watched namespaces, actually get synced | Default (`optInByDefault=false`): a resource needs `uptime-kuma.io/enabled=true`. With `optInByDefault=true`: every resource is synced unless annotated `uptime-kuma.io/enabled=false` |

These are fully independent — `watchAll=true` does **not** imply every
Ingress everywhere gets monitored; it only widens which namespaces are
in scope. You still need the annotation (or `optInByDefault=true`) on top.

## Monitoring Ingress and HTTPRoute

Opt a resource in:

```bash
kubectl annotate ingress my-app uptime-kuma.io/enabled=true
```

The operator creates one HTTP(S) monitor per distinct host:

- **Ingress**: one monitor per distinct `spec.rules[].host`. Scheme is
  `https` if the host is listed under `spec.tls`, otherwise `http`.
- **HTTPRoute**: one monitor per distinct `spec.hostnames[]`. Scheme
  defaults to `https` (HTTPRoute carries no TLS info of its own).

Remove a host from the resource and its monitor is deleted on the next
reconcile — you never end up with stale monitors for hosts you've
retired, as long as the parent Ingress/HTTPRoute still exists (see
[How it works](#how-it-works) for what happens when the whole resource is
deleted instead).

### Annotation reference

All annotations are optional overrides on top of Kuma's own defaults, and
apply to `Ingress`, `HTTPRoute`, and `Service` alike (see
[Watching Services](#watching-services) below for what's Service-specific).

Since a `Service` can produce a monitor of any Kuma type (not just HTTP),
the annotation contract mirrors the [Monitor CRD](#the-monitor-crd)'s own
shape: fields that exist once regardless of monitor type stay unscoped
under `uptime-kuma.io/`; fields specific to one Kuma type are scoped
under `<type>.uptime-kuma.io/` (e.g. `http.uptime-kuma.io/timeout`) — the
type lives in the DNS-subdomain prefix, since a Kubernetes annotation key
allows only one `/` (the same pattern well-known operators use for their
own annotation families, e.g. `cert-manager.io/*`).

#### Top-level annotations

These apply to every resource kind (`Ingress`, `HTTPRoute`, `Service`)
regardless of monitor type.

| Annotation | Type | Notes |
|---|---|---|
| `uptime-kuma.io/enabled` | `true`/`false` | Opts the resource in/out — see [above](#choosing-what-gets-watched) |
| `uptime-kuma.io/name` | string | Monitor name; defaults to `<namespace>/<name>/<host>` (Ingress/HTTPRoute) or `<namespace>/<name>` (Service) |
| `uptime-kuma.io/type` | `HTTP`/`TCP`/`Ping`/`DNS`/`Gamedig` | **Service only** — selects the monitor type; defaults to `HTTP`. Ingress/HTTPRoute never read this — they are always HTTP. |
| `uptime-kuma.io/interval` | integer (seconds) | Check interval |
| `uptime-kuma.io/retry-interval` | integer (seconds) | Interval between retries |
| `uptime-kuma.io/max-retries` | integer | Retries before a monitor is marked down |
| `uptime-kuma.io/description` | string | Shown alongside the monitor in the Kuma UI |
| `uptime-kuma.io/resend-interval` | integer | Failed checks between repeated down notifications; `0` disables resending |
| `uptime-kuma.io/upside-down` | `true`/`false` | Inverts up/down |
| `uptime-kuma.io/tags` | comma-separated list | See [Tags, notifications, groups, and proxies](#tags-notifications-groups-and-proxies) |
| `uptime-kuma.io/notifications` | comma-separated list | Notification channel names; each must already exist in Kuma |
| `uptime-kuma.io/group` | string | Parent group monitor name; must already exist in Kuma |
| `uptime-kuma.io/proxy` | integer | Kuma proxy ID (proxies have no name in Kuma, only an ID) |

#### `http.uptime-kuma.io/` annotations

Used by Ingress and HTTPRoute always, and by Service when `type` is
`HTTP` (the default). `host`/`port` are Service-only — Ingress/HTTPRoute
derive their host from routing rules instead.

| Annotation | Type | Notes |
|---|---|---|
| `http.uptime-kuma.io/host` | string | **Service only.** Overrides the default `<name>.<namespace>.svc.cluster.local` target |
| `http.uptime-kuma.io/port` | integer or Service port name | **Service only.** Required if the Service exposes more than one port; auto-picked if it exposes exactly one |
| `http.uptime-kuma.io/scheme` | `http`/`https` | Overrides the derived scheme (Ingress: from `spec.tls`; HTTPRoute/Service: `https`/`http` default) |
| `http.uptime-kuma.io/path` | string, starts with `/` | Request path; defaults to `/` |
| `http.uptime-kuma.io/accepted-statuscodes` | comma-separated list | e.g. `200-299,301` |
| `http.uptime-kuma.io/timeout` | integer (seconds) | Request timeout |
| `http.uptime-kuma.io/max-redirects` | integer | Redirects to follow |
| `http.uptime-kuma.io/ignore-tls` | `true`/`false` | Skip TLS certificate validation |
| `http.uptime-kuma.io/cache-bust` | `true`/`false` | Append a cache-busting query parameter |
| `http.uptime-kuma.io/expiry-notification` | `true`/`false` | TLS certificate expiry notifications |
| `http.uptime-kuma.io/domain-expiry-notification` | `true`/`false` | Domain expiry notifications |
| `http.uptime-kuma.io/headers` | string | Opaque passthrough to Kuma (raw JSON, as Kuma's UI expects) |
| `http.uptime-kuma.io/body` | string | Opaque passthrough to Kuma |

#### `tcp.uptime-kuma.io/` annotations (Service, `type: TCP`)

| Annotation | Type | Notes |
|---|---|---|
| `tcp.uptime-kuma.io/host` | string | Overrides the default `<name>.<namespace>.svc.cluster.local` target |
| `tcp.uptime-kuma.io/port` | integer or Service port name | Required if the Service exposes more than one port; auto-picked if it exposes exactly one |
| `tcp.uptime-kuma.io/tls-mode` | `nostarttls`/`secure`/`starttls` | TLS handshake mode; empty means plain TCP |
| `tcp.uptime-kuma.io/expected-ssl-alert` | string | Expected TLS alert name during the handshake |
| `tcp.uptime-kuma.io/expiry-notification` | `true`/`false` | TLS certificate expiry notifications (only honoured when `tls-mode` is `secure` or `starttls`) |
| `tcp.uptime-kuma.io/domain-expiry-notification` | `true`/`false` | Domain expiry notifications |

#### `ping.uptime-kuma.io/` annotations (Service, `type: Ping`)

| Annotation | Type | Notes |
|---|---|---|
| `ping.uptime-kuma.io/host` | string | Overrides the default `<name>.<namespace>.svc.cluster.local` target |
| `ping.uptime-kuma.io/timeout` | integer (seconds) | Per-ping timeout |
| `ping.uptime-kuma.io/packet-size` | integer (bytes) | ICMP packet size |
| `ping.uptime-kuma.io/domain-expiry-notification` | `true`/`false` | Domain expiry notifications |

There is no Ping port annotation — ICMP has no port concept in Kuma.

#### `dns.uptime-kuma.io/` annotations (Service, `type: DNS`)

| Annotation | Type | Notes |
|---|---|---|
| `dns.uptime-kuma.io/host` | string | Overrides the default `<name>.<namespace>.svc.cluster.local` target — the domain name to resolve |
| `dns.uptime-kuma.io/port` | integer | The resolver's query port. **Not resolved against the Service's own ports** — unlike `http`/`tcp`/`gamedig`'s `port`, this is a plain integer matching the resolver's own query port |
| `dns.uptime-kuma.io/resolver-server` | string | DNS resolver server address |
| `dns.uptime-kuma.io/resolve-type` | string | Record type to resolve (e.g. `A`) |
| `dns.uptime-kuma.io/domain-expiry-notification` | `true`/`false` | Domain expiry notifications |

#### `gamedig.uptime-kuma.io/` annotations (Service, `type: Gamedig`)

| Annotation | Type | Notes |
|---|---|---|
| `gamedig.uptime-kuma.io/host` | string | Overrides the default `<name>.<namespace>.svc.cluster.local` target |
| `gamedig.uptime-kuma.io/port` | integer or Service port name | Required if the Service exposes more than one port; auto-picked if it exposes exactly one |
| `gamedig.uptime-kuma.io/game` | string | **Required.** Gamedig game ID (Kuma's `game` field) |
| `gamedig.uptime-kuma.io/given-port-only` | `true`/`false` | Probe only the given port instead of letting Kuma guess it |
| `gamedig.uptime-kuma.io/domain-expiry-notification` | `true`/`false` | Domain expiry notifications |

> HTTP Basic/Bearer/OAuth2 auth and Gamedig tokens are **not** available
> as annotations on any resource — each is a credential that has to
> reference a Kubernetes `Secret`, which doesn't fit in a single
> annotation value. Use the [Monitor CRD](#the-monitor-crd) for those.

Three annotations are written *by* the operator and shouldn't be set or
edited by hand: `uptime-kuma.io/monitor-ids` (host → Kuma monitor ID),
`uptime-kuma.io/synced-hash` (fingerprint used to skip redundant syncs),
and `uptime-kuma.io/finalizer`.

### Example

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-app
  annotations:
    uptime-kuma.io/enabled: "true"
    uptime-kuma.io/interval: "60"
    http.uptime-kuma.io/accepted-statuscodes: "200-299"
    uptime-kuma.io/tags: "production,customer-facing"
    uptime-kuma.io/notifications: "slack-oncall"
spec:
  rules:
    - host: my-app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-app
                port: { number: 80 }
```

## Watching Services

A plain `Service` can be opted in the same way as an Ingress/HTTPRoute,
but — since a Service carries no routing information of its own — it can
produce a monitor of **any** Kuma type, selected via `uptime-kuma.io/type`
(default `HTTP`):

```bash
kubectl annotate service my-app uptime-kuma.io/enabled=true
```

By default, the check target is the Service's in-cluster DNS name
(`<name>.<namespace>.svc.cluster.local`); every type's `host` annotation
(`http.uptime-kuma.io/host`, `tcp.uptime-kuma.io/host`, etc. — see the
[annotation reference](#annotation-reference) above) overrides that.
HTTP, TCP, and Gamedig monitors additionally need a Service port: it's
auto-picked when the Service exposes exactly one, otherwise the matching
`port` annotation (by port number or by `Service.spec.ports[].name`) is
required and reconciliation fails with a clear error without it.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: game-server
  annotations:
    uptime-kuma.io/enabled: "true"
    uptime-kuma.io/type: "Gamedig"
    gamedig.uptime-kuma.io/game: "csgo"
spec:
  ports:
    - port: 27015
  selector:
    app: game-server
```

## The Monitor CRD

Reach for a `Monitor` custom resource when you need a monitor type
routing can't express (DNS, TCP, Ping, Gamedig), or an HTTP monitor with
authentication, notifications, a group, or a proxy — fields too rich for
an annotation.

```bash
kubectl apply -f config/samples/uptime-kuma_v1alpha1_monitor.yaml
```

Check on it:

```bash
kubectl get monitors
# NAME                TYPE      MONITORID
# http-auth-sample     HTTP      42

kubectl describe monitor http-auth-sample   # Ready condition + any SyncFailed events
```

### Common fields (`spec`)

| Field | Notes |
|---|---|
| `type` | One of `HTTP`, `TCP`, `Ping`, `DNS`, `Gamedig` — required, and must match exactly one of the type-specific blocks below |
| `name` | Monitor name in Kuma |
| `interval`, `retries`, `retryInterval` | Check timing |
| `description` | Shown in the Kuma UI |
| `resendInterval` | Failed checks between repeated down notifications; `0` disables |
| `upsideDown` | Inverts up/down |
| `notifications` | Notification channel names; each must already exist in Kuma |
| `group` | Parent group monitor name; must already exist in Kuma |
| `proxy` | Kuma proxy ID |
| `tags` | Tag names; a tag that doesn't exist yet is created automatically. Declarative — an empty/omitted list **clears** any tags currently on the monitor, including ones added by hand in Kuma |

### Type-specific fields

<details>
<summary><code>spec.http</code></summary>

| Field | Notes |
|---|---|
| `url` | required |
| `method` | HTTP method |
| `acceptedStatusCodes` | e.g. `["200-299"]` |
| `timeout`, `maxRedirects` | |
| `ignoreTLS`, `cacheBust` | |
| `expiryNotification`, `domainExpiryNotification` | TLS/domain expiry alerts |
| `headers`, `body` | Opaque passthrough to Kuma |
| `authMethod` | `""` (none), `basic`, `bearer`, or `oauth2-cc`. NTLM and mTLS aren't supported |
| `basicAuthUsername` | plain text; used with `authMethod: basic` |
| `basicAuthPasswordSecretRef` | `SecretKeySelector`, same namespace |
| `bearerTokenSecretRef` | `SecretKeySelector`, same namespace; used with `authMethod: bearer` |
| `oauthClientID`, `oauthTokenURL`, `oauthScopes`, `oauthAudience` | plain text; used with `authMethod: oauth2-cc` |
| `oauthClientSecretRef` | `SecretKeySelector`, same namespace |

</details>

<details>
<summary><code>spec.tcp</code></summary>

| Field | Notes |
|---|---|
| `host`, `port` | required |
| `tlsMode` | `""` (plain TCP), `nostarttls`, `secure`, or `starttls` |
| `expectedSSLAlert` | expected TLS alert name (e.g. for mTLS verification) |
| `expiryNotification` | only honored by Kuma when `tlsMode` is `secure` or `starttls` |
| `domainExpiryNotification` | |

</details>

<details>
<summary><code>spec.ping</code></summary>

| Field | Notes |
|---|---|
| `host` | required |
| `timeout` | per-ping timeout, seconds |
| `packetSize` | ICMP packet size, bytes |
| `domainExpiryNotification` | |

</details>

<details>
<summary><code>spec.dns</code></summary>

| Field | Notes |
|---|---|
| `host` | required |
| `resolverServer`, `resolveType`, `port` | |
| `domainExpiryNotification` | |

</details>

<details>
<summary><code>spec.gamedig</code></summary>

| Field | Notes |
|---|---|
| `host`, `port`, `game` | required |
| `givenPortOnly` | probe only the given port instead of letting Kuma guess it |
| `domainExpiryNotification` | |
| `tokenSecretRef` | optional auth token, `SecretKeySelector`, same namespace |

</details>

Every `*SecretKeySelector` field points at a `Secret` the operator does
**not** create or manage — create it yourself, in the same namespace as
the `Monitor`.

### Example: HTTP monitor with Basic auth

```yaml
apiVersion: uptime-kuma.io/v1alpha1
kind: Monitor
metadata:
  name: http-auth-sample
spec:
  type: HTTP
  name: "Sample HTTP Monitor"
  interval: 60
  notifications: [email-alerts, slack-oncall]
  group: production-web
  proxy: 1
  tags: [production, customer-facing]
  http:
    url: https://example.com/health
    authMethod: basic
    basicAuthUsername: monitoring-user
    basicAuthPasswordSecretRef:
      name: http-auth-sample-credentials
      key: password
---
apiVersion: v1
kind: Secret
metadata:
  name: http-auth-sample-credentials
type: Opaque
stringData:
  password: correct-horse-battery-staple
```

More worked examples (Gamedig, plain HTTP) live in
[`config/samples/uptime-kuma_v1alpha1_monitor.yaml`](../config/samples/uptime-kuma_v1alpha1_monitor.yaml).

## Tags, notifications, groups, and proxies

A monitor's final tag set is the union of up to three independent
sources — none of them override each other, they merge:

1. **Per-resource tags** — the `uptime-kuma.io/tags` annotation, or
   `spec.tags` on a `Monitor` CR.
2. **Operator-wide default tags** — the chart's `defaultTags` value,
   applied to *every* monitor the operator manages.
3. **Label-derived tags** — if the chart's `labelTagPatterns` value is
   set, any label on the source resource whose key matches one of those
   regex patterns is imported as a `key=value` Kuma tag. Off by default.

Tags that don't exist yet in Kuma are created automatically. Because tag
handling is fully declarative, removing a tag from all three sources
removes it from the monitor — including tags someone added by hand
in the Kuma UI.

`notifications` and `group` are matched **by name** and `proxy` **by
numeric ID** against what already exists in Kuma — the operator doesn't
create these for you. An unresolvable name/ID fails that reconcile (you'll
see it as a `SyncFailed` Kubernetes Event — see [Troubleshooting](#troubleshooting)).

## Configuration reference

Operator-wide behavior (not tied to any one resource) is set via Helm
values on `kuma.url`, `watchAll`/`watchNamespaces`, `optInByDefault`,
`driftCheckInterval`, `logLevel`, `defaultTags`, and `labelTagPatterns` —
see the [chart README's values table](../charts/uptime-kuma-operator/README.md#values)
for defaults and full descriptions, plus the security-relevant values
(`resources`, `securityContext`, `networkPolicy`, etc.).

## Operations

**Logs.** The operator logs every Kuma monitor create/update/delete at
Info level by default:

```bash
kubectl logs -l app.kubernetes.io/instance=uptime-kuma-operator -f
```

Set `--set logLevel=debug` for per-reconcile detail — what triggered a
reconcile and which decision branch was taken (skip, full sync, drift
correction).

**Status.**

- `Monitor` CRs carry a `Ready` condition (`kubectl describe monitor
  <name>`) and `status.monitorID`.
- `Ingress`/`HTTPRoute` don't have a status subresource; check
  `kubectl describe ingress/httproute <name>` for `SyncFailed` Events, or
  the operator's logs.

## Uninstalling

`helm uninstall` removes the Deployment, RBAC, and ServiceAccount, but —
by Helm design — **not** the `Monitor` CRD, and it does **not** touch
your `Ingress`/`HTTPRoute`/`Monitor` resources or their Kuma monitors.

If you want Kuma cleaned up too, delete the managed resources (or just
their `uptime-kuma.io/enabled` annotation / the `Monitor` CRs) *before*
uninstalling, so the operator is still running to process the deletion
and remove its finalizer. Uninstalling first leaves those monitors
orphaned in Kuma, and leaves the managed resources' finalizers in place
until you either reinstall the operator or remove the finalizers by hand.

## Troubleshooting

**A monitor isn't showing up in Kuma.**
1. Is the resource's namespace actually watched? Check `watchAll`/
   `watchNamespaces` against where the resource lives.
2. Is the resource opted in? Check for `uptime-kuma.io/enabled: "true"`,
   or that `optInByDefault=true` and it isn't annotated `="false"`.
3. Check the operator's logs at `logLevel=debug` for that reconcile.
4. For Ingress/HTTPRoute, check `kubectl describe` for a `SyncFailed`
   Event.

**Operator logs show 403s on list/watch.** The RBAC `Role`/`ClusterRole`
scope didn't line up with what's actually being watched — most often
`watchNamespaces` set without matching `POD_NAMESPACE`/release namespace,
or a namespace added to `watchNamespaces` after install without a
`helm upgrade`.

**Reconcile fails resolving `notifications`, `group`, or `proxy`.** These
must already exist in Kuma under that exact name (or ID, for `proxy`) —
the operator only references them, it doesn't create them.

**`SecretKeySelector` reconcile failures (Monitor CRD auth).** The
referenced `Secret` must exist in the *same namespace* as the `Monitor`
resource — cross-namespace references aren't supported.

**A monitor I edited by hand in Kuma keeps reverting.** This is expected
— see [Drift correction](#how-it-works). The operator treats its source
resource as the single source of truth; edit the resource (or its
annotations), not the monitor.

**Cluster-wide installs can read any Secret in scope.** This is a known,
documented RBAC trade-off, not a bug — see the
[chart README's security notes](../charts/uptime-kuma-operator/README.md#security-notes).
