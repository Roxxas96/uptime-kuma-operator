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
   cluster (which switches the chart from per-namespace `Role`s to a
   `ClusterRole`, and flips the opt-in default: every `Ingress`/`HTTPRoute`
   is synced unless annotated `uptime-kuma.io/enabled=false`).

3. Opt an Ingress in:

   ```bash
   kubectl annotate ingress my-app uptime-kuma.io/enabled=true
   ```

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
