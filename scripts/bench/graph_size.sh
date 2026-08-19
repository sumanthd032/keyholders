#!/usr/bin/env bash
# Reproduces the graph size table in docs/BENCHMARKS.md. Requires HydraDB running (make up) and the
# 2,000 package ingest already loaded.
set -euo pipefail
cd "$(dirname "$0")/../.."

go run ./cmd/bench-graph-size
