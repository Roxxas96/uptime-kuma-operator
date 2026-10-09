# Uptime Kuma Operator

Syncs Uptime Kuma monitors from `Ingress`, `HTTPRoute`, and `Service`
resources (any monitor type via a `uptime-kuma.io/type` annotation on
`Service`), and from a `Monitor` CRD for full manual control.

See **[`docs/README.md`](docs/README.md)** for the full user guide
(annotations, the Monitor CRD, tags/notifications, troubleshooting), or
`docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md` for the
internal design.

## Quick start

1. Create a secret with your Kuma credentials:

   ```bash
   kubectl create secret generic kuma-credentials \
     --from-literal=username=admin --from-literal=password=<password>
   ```

   For an account with two-factor authentication, add a `totpSecret` key
   (or use a `sessionToken` instead of username/password) — see the
   [user guide](docs/README.md#installing). Uptime Kuma API keys only cover
   `/metrics` and can't be used.

2. Install the chart. The `Monitor` CRD ships in the chart's `crds/`
   directory, so Helm installs it automatically — no separate
   `kubectl apply` step:

   ```bash
   helm install uptime-kuma-operator charts/uptime-kuma-operator \
     --set kuma.url=https://kuma.example.com \
     --set kuma.existingSecret=kuma-credentials \
     --set watchNamespaces={default}
   ```

   `watchNamespaces` lists the namespaces to watch; it defaults to the
   release namespace. Set `--set watchAll=true` instead to watch the whole
   cluster (switches the chart from per-namespace `Role`s to a
   `ClusterRole`). This only changes *which namespaces* are watched — it
   has no effect on whether a resource needs the opt-in annotation below.

   Pulling the operator image from a private registry? Set
   `--set imagePullSecrets[0].name=my-registry-secret` to an existing
   `docker-registry` Secret in the release namespace.

3. Opt an Ingress in:

   ```bash
   kubectl annotate ingress my-app uptime-kuma.io/enabled=true
   ```

   By default every `Ingress`/`HTTPRoute` needs this annotation to be
   synced, regardless of `watchAll`. Set `--set optInByDefault=true` to
   invert that: every resource is synced unless annotated
   `uptime-kuma.io/enabled=false`. `watchAll` and `optInByDefault` are
   independent — set either, both, or neither.

   If a monitor is deleted or reconfigured directly in Kuma (e.g. via its
   UI) while its source Ingress/HTTPRoute/Monitor still exists, the
   operator recreates or corrects it on its next reconcile of that
   resource — at most `driftCheckInterval` (default `30s`) later, even with
   no other trigger. Set `--set driftCheckInterval=5m` for a lighter check
   cadence.

   The operator logs every Kuma monitor it creates, updates, or deletes at
   Info level (the default). Set `--set logLevel=debug` for per-reconcile
   detail — what triggered a reconcile and which decision branch was taken
   (skip, full sync, drift correction, etc).

   Set `--set defaultTags={k8s,managed}` to apply tag names to every
   monitor the operator manages, in addition to whatever tags that
   monitor's own resource specifies. Empty by default.

   Set `--set labelTagPatterns={team,env-.*}` to automatically add a
   `"key=value"` tag for every label on a managed Ingress/HTTPRoute/Monitor
   whose key fully matches one of these regex patterns (each pattern is
   anchored, so a plain prefix filter reads as e.g. `"team-.*"`). Empty by
   default — off until configured. See
   `docs/superpowers/specs/2026-09-19-monitor-config-expansion-phase3-design.md`
   for the full design and its tag-proliferation caveat.

   Phase 1 monitor configuration (description, timeouts, TLS options,
   notification toggles, custom headers/body, and more) is available via
   both annotations (HTTP-applicable fields only — see the annotation
   table in `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`)
   and the Monitor CRD (all types — see
   `config/samples/uptime-kuma_v1alpha1_monitor.yaml`).

   Phase 2 adds `tags`, `notifications`, `proxy`, and `group`, also
   available via annotation on `Ingress`/`HTTPRoute` (same table as
   above) as well as on the Monitor CRD. Tags are fully declarative like
   every other managed field: omitting them clears any tags currently on
   the monitor in Kuma, including ones added manually. HTTP Basic/Bearer/OAuth2-Client-
   Credentials auth and the Gamedig `token` are Monitor-CRD-only, since
   each credential is a `SecretKeySelector` referencing a Kubernetes
   `Secret` in the same namespace — there's no sane way to represent that
   as a single annotation value. See the updated
   `config/samples/uptime-kuma_v1alpha1_monitor.yaml` for a worked
   example and `docs/superpowers/specs/2026-09-18-monitor-config-expansion-phase2-design.md`
   for the full design.

4. Or declare a monitor Kuma can't derive from routing (DNS, Gamedig, TCP,
   Ping, Push) with a `Monitor` CR — see
   `config/samples/uptime-kuma_v1alpha1_monitor.yaml`:

   ```bash
   kubectl apply -f config/samples/uptime-kuma_v1alpha1_monitor.yaml
   ```

## Monitoring

The operator records metrics with the OpenTelemetry SDK and serves them in
Prometheus format on `:8080/metrics` (exposed by the chart's Service when
`service.enabled=true`). The same endpoint carries controller-runtime's
built-in reconcile, workqueue, Kubernetes client and Go runtime metrics.

| Metric | Type | Labels |
| --- | --- | --- |
| `uptime_kuma_operator_managed_monitors` | gauge | `source` (`ingress`, `httproute`, `service`, `monitor`), `resource_namespace` |
| `uptime_kuma_operator_kuma_requests_total` | counter | `operation` (`create`, `update`, `delete`, `list_monitors`, `list_notifications`, `find_group`, `list_tags`, `create_tag`, `set_monitor_tags`), `outcome` (`success`, `error`) |
| `uptime_kuma_operator_kuma_request_duration_seconds` | histogram | `operation`, `outcome` |

To also push the operator's metrics over OTLP/gRPC, set
`otlp.endpoint` (or the standard `OTEL_EXPORTER_OTLP_ENDPOINT` /
`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` env vars; `OTEL_METRICS_EXPORTER=none`
turns it off). Only the `uptime_kuma_operator_*` metrics are pushed;
controller-runtime's built-ins stay Prometheus-only.

The chart can wire this into a Prometheus Operator / Grafana setup such as
kube-prometheus-stack:

```bash
helm upgrade uptime-kuma-operator charts/uptime-kuma-operator --reuse-values \
  --set service.enabled=true \
  --set serviceMonitor.enabled=true \
  --set serviceMonitor.labels.release=kube-prometheus-stack \
  --set grafanaDashboard.enabled=true
```

`grafanaDashboard.enabled` ships
`charts/uptime-kuma-operator/dashboards/uptime-kuma-operator.json` as a
ConfigMap labelled `grafana_dashboard: "1"` for Grafana's dashboard sidecar.
You can also import that file into Grafana by hand. It expects a Prometheus
data source and finds the operator through its `target_info{service_name="uptime-kuma-operator"}`
series, so keep the default `OTEL_SERVICE_NAME`.

## Development

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for setup (`mise install` +
`prek install`) and PR conventions.

- `make test` runs unit tests and the `envtest`-backed controller suite.
- `go test -tags=integration ./test/integration/...` runs the real-Kuma
  integration test against Uptime Kuma **2.x** (`github.com/breml/go-uptime-kuma-client`
  requires 2.x; 1.x is not supported and uses an incompatible schema/login
  protocol). Requires `docker compose -f test/integration/docker-compose.yaml up -d`,
  then `KUMA_URL=http://localhost:3001 KUMA_USERNAME=admin KUMA_PASSWORD=<password> go test -tags=integration ./test/integration/... -v`
  — no manual setup wizard needed, the test bootstraps the instance itself.
  Not part of `make test`.
- `./charts/uptime-kuma-operator/templates/tests/rbac_test.sh` checks the
  chart renders per-namespace `Role`s or a `ClusterRole` correctly depending
  on `watchAll`, including at chart defaults.
- `make manifests` regenerates `config/crd/bases/` and then runs
  `make sync-chart-crds`, which copies the CRD into the chart's `crds/`
  directory. The two must stay byte-identical; never edit either by hand.

## Releasing

1. Tag a commit on `main` and push the tag: `git tag -s v0.1.0 -m v0.1.0
   && git push origin v0.1.0` (`vX.Y.Z`, or `vX.Y.Z-rcN` for a
   prerelease). Tags must be signed. No version bump is needed:
   `Chart.yaml`'s `version` is a placeholder.
2. CI's `release` job (`.github/workflows/ci.yml`) takes it from there:
   it packages the chart with its `version` and `appVersion` set from the
   tag, pushes it to
   `oci://ghcr.io/roxxas96/uptime-kuma-operator/charts`, and creates the
   GitHub Release with auto-generated notes and the packaged chart
   attached. The Docker image (already built/signed by the `build` job
   for the same tag) is tagged to match.

## Annotations

See [`docs/README.md`](docs/README.md#watching-services) for the full,
per-type annotation tables (top-level plus `http.uptime-kuma.io/`,
`tcp.uptime-kuma.io/`, `ping.uptime-kuma.io/`, `dns.uptime-kuma.io/`,
`gamedig.uptime-kuma.io/`).
