#!/usr/bin/env bash
# Creates the local kind cluster and installs the operators the chart needs.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
cluster=unfraud
strimzi_version=1.2.0

if ! kind get clusters | grep -qx "$cluster"; then
  DATA_DIR="$root/data/raw" envsubst < "$root/deploy/kind/cluster.yaml" | kind create cluster --config - --wait 120s
fi
kubectl config use-context "kind-$cluster" >/dev/null

# Strimzi creates RoleBindings in the namespaces it watches, so they must exist first.
kubectl create namespace unfraud --dry-run=client -o yaml | kubectl apply -f - >/dev/null

helm upgrade --install strimzi oci://quay.io/strimzi-helm/strimzi-kafka-operator \
  --version "$strimzi_version" \
  --namespace strimzi-system --create-namespace \
  --set 'watchNamespaces={unfraud}' \
  --wait
