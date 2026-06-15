#!/usr/bin/env bash
# Rebuild the gbrain index from the markdown brain in the *data* repo.
# The brain pages live in the private devbrain-data repo (default ~/devbrain-data),
# NOT in this system repo. Override the location with $DEVBRAIN_DATA.
# Idempotent: re-running re-puts the pages (gbrain upserts by slug).
#
# This rebuilds the INDEX from the markdown; it does not alter the markdown.
# (Losing the gbrain index is cheap — this is how you recover it on a new machine.)
set -euo pipefail

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

command -v gbrain >/dev/null || { echo "gbrain not found on PATH"; exit 1; }
[ -d "$DATA" ] || { echo "data repo not found at $DATA — clone your devbrain-data repo there (or set \$DEVBRAIN_DATA)"; exit 1; }

echo "Loading brain pages from $DATA ..."
# find (not bash globstar) — macOS ships bash 3.2, which lacks `shopt -s globstar`.
while IFS= read -r f; do
  [ -n "$f" ] || continue
  slug="project/$(basename "$f" .md)"
  gbrain put "$slug" < "$f" >/dev/null
  gbrain tag "$slug" devbrain >/dev/null 2>&1 || true
  echo "  put $slug"
done < <(find "$DATA"/projects -type f -path '*/brain/*.md' 2>/dev/null)

# Embeddings are an optional OpenAI-backed enhancement; skip cleanly without a key.
if [ -n "${OPENAI_API_KEY:-}" ]; then
  echo "Embedding (incremental) ..."
  gbrain embed --stale >/dev/null 2>&1 || true
else
  echo "No OPENAI_API_KEY — skipping embeddings (keyword search still works)."
fi

echo "Done. Verify:"
echo "  gbrain list --tag devbrain"
echo "  gbrain search devbrain"
