# uptime-kuma-operator

Syncs Uptime Kuma monitors from Ingress, HTTPRoute, and a Monitor CRD.

## Installing

Published releases are available as an OCI artifact — no `git clone` or
`helm repo add` needed:

```bash
helm install my-release oci://ghcr.io/roxxas96/uptime-kuma-operator/charts/uptime-kuma-operator \
  --version 0.1.0 \
  --set kuma.url=https://kuma.example.com \
  --set kuma.existingSecret=kuma-credentials
```

Or, from a checkout of this repo:

```bash
helm install my-release charts/uptime-kuma-operator \
  --set kuma.url=https://kuma.example.com \
  --set kuma.existingSecret=kuma-credentials
```

`kuma.existingSecret` must name a Secret (in the release namespace) with
`username` and `password` keys — the chart never takes credentials directly
as values.

## Values

| Key | Default | Description |
|---|---|---|
| `replicaCount` | `1` | Fixed at 1: no leader election, so >1 replica would race reconciles against Kuma. |
| `image.repository` | `uptime-kuma-operator` | Manager image repository. |
| `image.tag` | `latest` | Manager image tag. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | `[]` | Existing docker-registry Secrets, e.g. `[{name: my-registry-secret}]`. |
| `kuma.url` | `""` | Uptime Kuma base URL. Required. |
| `kuma.existingSecret` | `""` | Secret with `username`/`password` keys. Required. |
| `watchAll` | `false` | Watch every namespace (ClusterRole) instead of `watchNamespaces` (Role per namespace). |
| `watchNamespaces` | `[]` | Namespaces to watch when `watchAll` is false; defaults to the release namespace if empty. |
| `optInByDefault` | `false` | Sync every Ingress/HTTPRoute unless annotated `uptime-kuma.io/enabled=false`, instead of requiring an explicit `=true`. |
| `driftCheckInterval` | `30s` | How often idle reconciles re-check Kuma still has the monitor(s). |
| `logLevel` | `info` | One of `debug`, `info`, `warn`, `error`. |
| `defaultTags` | `[]` | Tag names applied to every managed monitor. |
| `labelTagPatterns` | `[]` | Regex patterns for deriving Kuma tags from resource labels; see `values.yaml` for matching semantics. |
| `serviceAccount.create` | `true` | Whether to create a ServiceAccount. |
| `serviceAccount.name` | `""` | ServiceAccount name; generated from the release name if empty and `create` is true, required if `create` is false. |
| `serviceAccount.annotations` | `{}` | ServiceAccount annotations, e.g. for cloud workload identity. |
| `podAnnotations` / `podLabels` | `{}` | Extra pod metadata. |
| `podSecurityContext` | runAsNonRoot, seccompProfile RuntimeDefault | Pod-level `securityContext`. |
| `securityContext` | no privilege escalation, read-only rootfs, all capabilities dropped | Container-level `securityContext`. |
| `resources` | `{}` | Container resources; unset by default, size for your workload. |
| `nodeSelector` / `tolerations` / `affinity` | `{}` / `[]` / `{}` | Standard scheduling controls. |
| `service.enabled` | `false` | Expose the metrics port (`:8080`, plain HTTP, unauthenticated) via a ClusterIP Service. |
| `service.port` | `8080` | Service port for metrics. |
| `networkPolicy.enabled` | `false` | Lock down pod ingress/egress. Blocks all traffic except DNS and the Kubernetes API by default. |
| `networkPolicy.kubeApiServerCIDR` | `""` | Restrict API server egress to this CIDR; otherwise allowed on 443/6443 to any destination. |
| `networkPolicy.extraEgress` / `extraIngress` | `[]` | Additional raw `NetworkPolicyEgressRule`/`NetworkPolicyIngressRule` entries — needed for reaching `kuma.url`, and for `service.enabled` scraping, respectively. |

## Security notes

- **RBAC grants `get` on every Secret** in the watched namespace(s) (or the
  whole cluster under `watchAll`). This is unavoidable: the operator
  resolves HTTP-auth/Gamedig-token Secrets referenced by arbitrary
  Monitor/Ingress/HTTPRoute resources whose names aren't known ahead of
  time, so RBAC `resourceNames` can't scope this down generically. Secrets
  are fetched one-by-one by name and never listed or cached. In effect,
  anyone who can create or edit a watched Ingress, HTTPRoute, or Monitor
  can make the operator read any Secret in scope — treat this
  ServiceAccount as equivalent to Secret-read access across whatever it
  watches, and scope `watchNamespaces`/`watchAll` accordingly.
- The manager container runs as the image's non-root user with a read-only
  root filesystem and all Linux capabilities dropped.
- `networkPolicy.enabled` and `service.enabled` are both off by default —
  turning them on changes the pod's network exposure, so review the values
  table above before enabling either.
