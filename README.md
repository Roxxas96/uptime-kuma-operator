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

2. Install the chart:

   ```bash
   helm install uptime-kuma-operator charts/uptime-kuma-operator \
     --set kuma.url=https://kuma.example.com \
     --set kuma.existingSecret=kuma-credentials \
     --set watchNamespaces={default}
   ```

3. Opt an Ingress in:

   ```bash
   kubectl annotate ingress my-app uptime-kuma.io/enabled=true
   ```

## Development

- `make test` runs unit tests and the `envtest`-backed controller suite.
- `go test -tags=integration ./test/integration/...` runs the real-Kuma
  integration test; requires `docker compose -f test/integration/docker-compose.yaml up -d`
  and completing Kuma's one-time setup wizard first. Not part of `make test`.
- `./charts/uptime-kuma-operator/templates/tests/rbac_test.sh` checks the
  chart renders per-namespace `Role`s or a `ClusterRole` correctly depending
  on `watchAll`.

## Annotations

See the design spec's "Annotation contract" section for the full list
(`uptime-kuma.io/enabled`, `.../name`, `.../scheme`, `.../interval`,
`.../retry-interval`, `.../max-retries`, `.../accepted-statuscodes`).
