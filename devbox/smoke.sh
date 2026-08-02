#!/usr/bin/env bash
# Assertions for the devbox-plugin smoke test (see .github/workflows/devbox.yml).
# Runs inside the synthetic consumer's devbox environment, CWD = the harness dir,
# after `devbox install` has applied the plugin (packages + init_hook + env).
set -euo pipefail

echo "--- toolchain on PATH (from the plugin's packages) ---"
for t in dctl kubectl velero flux jq just; do
  command -v "$t" >/dev/null || { echo "MISSING: $t"; exit 1; }
  echo "  ok $t"
done

echo "--- files materialized into ./.just (init_hook) ---"
test -f .just/cluster.just || { echo "cluster.just not materialized"; exit 1; }
test -f .just/.trivyignore-base || { echo ".trivyignore-base not materialized"; exit 1; }

echo "--- env set + base ignore resolves to a real, non-empty file ---"
echo "DCTL=${DCTL:?DCTL not set by plugin}  TRIVYIGNORE_BASE=${TRIVYIGNORE_BASE:?TRIVYIGNORE_BASE not set}"
test -s "$TRIVYIGNORE_BASE"

echo "--- just cluster … dispatch routes to the flat recipes ---"
just --dry-run cluster verify backup 2>&1 | grep -q 'cluster-verify'
just --dry-run cluster verify image-scan 2>&1 | grep -q 'cluster-verify'
just --dry-run cluster debug 2>&1 | grep -q 'cluster-debug'

echo "DEVBOX PLUGIN SMOKE OK"
