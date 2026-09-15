#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../../.."

# deployment.yaml `required`s these two, so every render below must set them.
REQUIRED=(--set kuma.url=https://kuma.example.com --set kuma.existingSecret=kuma-credentials)

echo "== per-namespace mode =="
out=$(helm template test-release charts/uptime-kuma-operator "${REQUIRED[@]}" \
  --set watchAll=false --set 'watchNamespaces={prod,staging}')
echo "$out" | grep -q 'kind: Role$' || { echo "FAIL: expected a namespaced Role"; exit 1; }
echo "$out" | grep -c '^  namespace: prod$' | grep -q '^2$' || { echo "FAIL: expected Role+RoleBinding in namespace prod"; exit 1; }
echo "$out" | grep -q 'kind: ClusterRole$' && { echo "FAIL: did not expect a ClusterRole in per-namespace mode"; exit 1; }

echo "== watch-all mode =="
out=$(helm template test-release charts/uptime-kuma-operator "${REQUIRED[@]}" --set watchAll=true)
echo "$out" | grep -q 'kind: ClusterRole$' || { echo "FAIL: expected a ClusterRole in watch-all mode"; exit 1; }
echo "$out" | grep -q 'kind: Role$' && { echo "FAIL: did not expect a namespaced Role in watch-all mode"; exit 1; }

echo "== chart defaults (watchAll=false, no watchNamespaces) =="
# `helm template <release>` with no --namespace puts the release in "default",
# which is also what config.Load resolves to via the POD_NAMESPACE downward-API
# env var. The two must agree or the manager 403s on every list/watch.
out=$(helm template test-release charts/uptime-kuma-operator "${REQUIRED[@]}")
echo "$out" | grep -c '^kind: Role$' | grep -q '^1$' || { echo "FAIL: expected exactly one Role at chart defaults"; exit 1; }
echo "$out" | grep -c '^kind: RoleBinding$' | grep -q '^1$' || { echo "FAIL: expected exactly one RoleBinding at chart defaults"; exit 1; }
echo "$out" | grep -q 'kind: ClusterRole$' && { echo "FAIL: did not expect a ClusterRole at chart defaults"; exit 1; }
# Role + RoleBinding + Deployment + ServiceAccount all live in the release namespace.
echo "$out" | grep -c '^  namespace: default$' | grep -q '^4$' || { echo "FAIL: expected Role+RoleBinding scoped to the release namespace 'default'"; exit 1; }

echo "OK"
