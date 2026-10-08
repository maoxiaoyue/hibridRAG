#!/usr/bin/env bash
# Build and push the custom PostgreSQL 18.3 + pg_jieba image.
#
# Usage:
#   REGISTRY=harbor.local/mcp ./infra/postgres/build.sh
#   REGISTRY=docker.io/yourname ./infra/postgres/build.sh
#   REGISTRY=192.168.50.201:5000 ./infra/postgres/build.sh   # local registry
set -euo pipefail

: "${REGISTRY:?REGISTRY env var required (e.g. harbor.local/mcp)}"
TAG="${TAG:-18.3-jieba}"
IMAGE="${REGISTRY}/mcp-postgres:${TAG}"

cd "$(dirname "$0")/../.."

echo "Building ${IMAGE}"
docker build -t "${IMAGE}" -f infra/postgres/Dockerfile .

echo "Pushing ${IMAGE}"
docker push "${IMAGE}"

echo
echo "Done. Roll out with:"
echo "  kubectl -n <namespace> set image statefulset/postgres-std postgres=${IMAGE}"
echo "  kubectl -n <namespace> rollout status statefulset/postgres-std"
