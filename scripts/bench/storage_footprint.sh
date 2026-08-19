#!/usr/bin/env bash
# Reproduces the storage footprint table in docs/BENCHMARKS.md. Requires HydraDB running (make up).
set -euo pipefail
cd "$(dirname "$0")/../.."

echo "== deploy/hydradb-data =="
du -sh deploy/hydradb-data
du -sh deploy/hydradb-data/*

echo
echo "== container memory, steady state =="
docker stats --no-stream --format "table {{.Name}}\t{{.MemUsage}}\t{{.CPUPerc}}"
