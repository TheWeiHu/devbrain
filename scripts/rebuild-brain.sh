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
# Slug per-project (<project>/<topic>) and tag with the page's ACTUAL project —
# derived from its projects/<project>/ path — not a blanket constant. (The old code
# tagged every page `devbrain`+`architecture`, mislabeling other projects' pages.)
while IFS= read -r f; do
  [ -n "$f" ] || continue
  project="$(basename "$(dirname "$(dirname "$f")")")"   # projects/<project>/brain/<file>.md
  base="$(basename "$f" .md)"
  slug="$project/${base#"$project"-}"
  gbrain put "$slug" < "$f" >/dev/null
  gbrain tag "$slug" "$project" >/dev/null 2>&1 || true
  echo "  put $slug"
done < <(find "$DATA"/projects -type f -path '*/brain/*.md' 2>/dev/null)

echo "Linking devbrain overview -> sections ..."
for s in capture brain assemble concurrency-sync decisions; do
  gbrain link "devbrain/overview" "devbrain/$s" --type references >/dev/null 2>&1 || true
done

echo "Embedding (incremental) ..."
gbrain embed --stale >/dev/null 2>&1 || true

echo "Done. Verify:"
echo "  gbrain list --tag devbrain"
echo "  gbrain query \"how does devbrain handle concurrency\" --detail low"
