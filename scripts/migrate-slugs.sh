#!/usr/bin/env bash
# One-time migration: re-key gbrain pages from the flat shared `project/<basename>`
# namespace to per-project `<project>/<topic>` (topic = basename minus a redundant
# leading `<project>-`). It also rewrites `[[project/<base>]]` wikilinks in the
# source markdown so the brain graph stays intact, then deletes the stale
# `project/*` slugs from the index.
#
# Scope: only pages that have source markdown under $DATA/projects/<project>/brain/
# — pages owned by other tools (no devbrain-data source) are left untouched.
# Idempotent: re-running is a no-op once everything is already `<project>/<topic>`.
#
# Usage:  [DEVBRAIN_DATA=…] scripts/migrate-slugs.sh [--dry-run]
set -euo pipefail
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
DRY=0; [ "${1:-}" = "--dry-run" ] && DRY=1
command -v gbrain >/dev/null || { echo "gbrain not found on PATH"; exit 1; }
[ -d "$DATA" ] || { echo "no data repo at $DATA"; exit 1; }

echo "Building slug map$([ "$DRY" = 1 ] && echo ' (dry run)') + rewriting wikilinks ..."
mapfile="$(mktemp)"
DRY="$DRY" python3 - "$DATA" "$mapfile" <<'PY'
import sys, os, re, glob
DATA, mapfile = sys.argv[1], sys.argv[2]
dry = os.environ.get("DRY") == "1"
files = sorted(glob.glob(os.path.join(DATA, "projects", "*", "brain", "*.md")))
pairs = []  # (file, project, old_slug, new_slug)
for f in files:
    project = os.path.basename(os.path.dirname(os.path.dirname(f)))
    base = os.path.basename(f)[:-3]  # strip .md
    topic = base[len(project) + 1:] if base.startswith(project + "-") else base
    pairs.append((f, project, f"project/{base}", f"{project}/{topic}"))

# Rewrite links old->new in every file. Longest old first so no slug is a prefix of
# another; the (?![\w-]) guard keeps us from matching inside a longer slug token.
subs = sorted({(o, n) for _, _, o, n in pairs}, key=lambda x: -len(x[0]))
for f, _, _, _ in pairs:
    s = open(f, encoding="utf-8").read()
    new = s
    for old, repl in subs:
        new = re.sub(re.escape(old) + r"(?![\w-])", repl, new)
    if new != s:
        print("  links:", os.path.basename(f))
        if not dry:
            open(f, "w", encoding="utf-8").write(new)

with open(mapfile, "w") as mf:
    for f, project, old, new in pairs:
        mf.write(f"{f}\t{project}\t{old}\t{new}\n")
PY

echo "Re-keying pages in gbrain ..."
while IFS=$'\t' read -r f project old new; do
  [ -n "$f" ] || continue
  [ "$old" = "$new" ] && { echo "  = $new (already namespaced)"; continue; }
  if [ "$DRY" = 1 ]; then echo "  $old -> $new"; continue; fi
  gbrain put "$new" < "$f" >/dev/null 2>&1 && gbrain tag "$new" "$project" >/dev/null 2>&1 || true
  gbrain delete "$old" >/dev/null 2>&1 || true
  echo "  $old -> $new"
done < "$mapfile"
rm -f "$mapfile"

[ "$DRY" = 0 ] && [ -n "${OPENAI_API_KEY:-}" ] && gbrain embed --stale >/dev/null 2>&1 || true
echo "Done.  Verify:  gbrain list -n 80 | grep -v '^project/'   # no flat project/* should remain for migrated projects"
