# Uptime Kuma Operator

Syncs Uptime Kuma monitors from `Ingress` and `HTTPRoute` resources, and
from a `Monitor` CRD for manually declared DNS/Gamedig/TCP/Ping monitors.

See `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md` for
the full design.

## Quick start

1. Create a secret with your Kuma credentials:

   ```bash
   kubectl create secret generic kuma-credentials \
     --from-literal=username=admin --from-literal=password=<password>
   ```

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
   Ping) with a `Monitor` CR — see
   `config/samples/uptime-kuma_v1alpha1_monitor.yaml`:

   ```bash
   kubectl apply -f config/samples/uptime-kuma_v1alpha1_monitor.yaml
   ```

## Development

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

## Annotations

See the design spec's "Annotation contract" section for the full list
(`uptime-kuma.io/enabled`, `.../name`, `.../scheme`, `.../interval`,
`.../retry-interval`, `.../max-retries`, `.../accepted-statuscodes`).
