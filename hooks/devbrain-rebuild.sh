#!/usr/bin/env bash
# Rebuild the gbrain index from the markdown brain in the *data* repo.
# The brain pages live in the private devbrain-data repo (default ~/devbrain-data),
# NOT in this system repo. Override the location with $DEVBRAIN_DATA.
# Idempotent: re-running re-puts the pages (gbrain upserts by slug).
set -euo pipefail

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

command -v gbrain >/dev/null || { echo "gbrain not found on PATH"; exit 1; }
[ -d "$DATA" ] || { echo "data repo not found at $DATA — clone TheWeiHu/devbrain-data there (or set \$DEVBRAIN_DATA)"; exit 1; }

echo "Loading brain pages from $DATA ..."
# find (not bash globstar) — macOS ships bash 3.2, which lacks `shopt -s globstar`.
while IFS= read -r f; do
  [ -n "$f" ] || continue
  slug="project/$(basename "$f" .md)"
  gbrain put "$slug" < "$f" >/dev/null
  gbrain tag "$slug" devbrain >/dev/null 2>&1 || true
  gbrain tag "$slug" architecture >/dev/null 2>&1 || true
  echo "  put $slug"
done < <(find "$DATA"/projects -type f -path '*/brain/*.md' 2>/dev/null)

echo "Linking overview -> sections ..."
for s in capture brain assemble concurrency-sync decisions; do
  gbrain link "project/devbrain-overview" "project/devbrain-$s" --type references >/dev/null 2>&1 || true
done

echo "Embedding (incremental) ..."
gbrain embed --stale >/dev/null 2>&1 || true

echo "Done. Verify:"
echo "  gbrain list --tag devbrain"
echo "  gbrain query \"how does devbrain handle concurrency\" --detail low"
