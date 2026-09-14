#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../../.."

echo "== per-namespace mode =="
out=$(helm template test-release charts/uptime-kuma-operator \
  --set watchAll=false --set 'watchNamespaces={prod,staging}')
echo "$out" | grep -q 'kind: Role$' || { echo "FAIL: expected a namespaced Role"; exit 1; }
echo "$out" | grep -c '^  namespace: prod$' | grep -q '^2$' || { echo "FAIL: expected Role+RoleBinding in namespace prod"; exit 1; }
echo "$out" | grep -q 'kind: ClusterRole$' && { echo "FAIL: did not expect a ClusterRole in per-namespace mode"; exit 1; }

echo "== watch-all mode =="
out=$(helm template test-release charts/uptime-kuma-operator --set watchAll=true)
echo "$out" | grep -q 'kind: ClusterRole$' || { echo "FAIL: expected a ClusterRole in watch-all mode"; exit 1; }
echo "$out" | grep -q 'kind: Role$' && { echo "FAIL: did not expect a namespaced Role in watch-all mode"; exit 1; }

echo "OK"
