#!/usr/bin/env bash
# Rebuild the devbrain gbrain index from the markdown sources in this repo.
# Idempotent: re-running re-puts the pages (gbrain upserts by slug).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BRAIN="$ROOT/projects/devbrain/brain"

command -v gbrain >/dev/null || { echo "gbrain not found on PATH"; exit 1; }

echo "Loading pages from $BRAIN ..."
for f in "$BRAIN"/*.md; do
  slug="project/$(basename "$f" .md)"
  gbrain put "$slug" < "$f" >/dev/null
  gbrain tag "$slug" devbrain >/dev/null 2>&1 || true
  gbrain tag "$slug" architecture >/dev/null 2>&1 || true
  echo "  put $slug"
done

echo "Linking overview -> sections ..."
for s in capture brain assemble concurrency-sync decisions; do
  gbrain link "project/devbrain-overview" "project/devbrain-$s" --type references >/dev/null 2>&1 || true
done

echo "Embedding (incremental) ..."
gbrain embed --stale >/dev/null 2>&1 || true

echo "Done. Verify:"
echo "  gbrain list --tag devbrain"
echo "  gbrain query \"how does devbrain handle concurrency\" --detail low"
