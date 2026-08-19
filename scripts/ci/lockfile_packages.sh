#!/usr/bin/env bash
# Extracts a flat package name list from an npm package-lock.json (lockfile version 2 or 3, the
# "packages" object keyed by node_modules path) for `keyholders ingest -packages` and
# `keyholders resolve -packages` to scope against, rather than ingesting the whole registry to
# check one lockfile.
set -euo pipefail

LOCKFILE="${1:?usage: lockfile_packages.sh <package-lock.json>}"

jq -r '
  .packages
  | to_entries[]
  | .key
  | select(startswith("node_modules/"))
  | sub("^node_modules/"; "")
  | sub(".*/node_modules/"; "")
' "$LOCKFILE" | sort -u
