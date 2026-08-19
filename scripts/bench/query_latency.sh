#!/usr/bin/env bash
# Reproduces the query latency table in docs/BENCHMARKS.md: 20 runs of `keyholders scan` against a
# real lockfile on a warm graph, sorted wall clock times. Requires HydraDB running (make up) and the
# lockfile's packages ingested; web/package-lock.json is what the published numbers were measured
# against.
set -euo pipefail
cd "$(dirname "$0")/../.."

LOCKFILE="${1:-web/package-lock.json}"
RUNS="${2:-20}"
BIN="$(mktemp -d)/keyholders"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

go build -o "$BIN" ./cmd/keyholders

echo "run,seconds"
for i in $(seq 1 "$RUNS"); do
  start=$(date +%s.%N)
  "$BIN" scan -json "$LOCKFILE" > /dev/null
  end=$(date +%s.%N)
  echo "$i,$(echo "$end - $start" | bc)"
done
