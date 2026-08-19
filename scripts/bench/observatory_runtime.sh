#!/usr/bin/env bash
# Reproduces the observatory runtime table in docs/BENCHMARKS.md: a full run against the 2,000
# package list, then the same run with graph writes skipped. Requires HydraDB running (make up).
set -euo pipefail
cd "$(dirname "$0")/../.."

PACKAGES="${1:-var/packages-2k.txt}"
BIN="$(mktemp -d)/keyholders"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

go build -o "$BIN" ./cmd/keyholders

echo "== full run, 8 epochs, writes graph state =="
time "$BIN" observatory -packages "$PACKAGES" -epochs 8 -top 1 -validate 0 > /dev/null

echo
echo "== same run, -write=false: propagation and aggregation only =="
time "$BIN" observatory -packages "$PACKAGES" -epochs 8 -top 1 -validate 0 -write=false > /dev/null
