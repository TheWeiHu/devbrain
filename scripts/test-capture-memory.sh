#!/usr/bin/env bash
# devbrain — capture-memory.sh (SessionEnd) integration tests. Feeds a fake payload
# whose transcript has a sibling memory/ dir, and checks the memory store is mirrored
# into the data repo, redacted, and idempotent.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; HOOK="$HERE/../hooks/capture-memory.sh"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not installed"; exit 0; }

export DEVBRAIN_DATA="$(mktemp -d)"
export DEVBRAIN_PROJECT="testproj"     # deterministic project key (resolver honors this)
workdir="$(mktemp -d)"
trap 'rm -rf "$DEVBRAIN_DATA" "$workdir"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# A transcript file with a sibling memory/ dir (mirrors ~/.claude/projects/<slug>/).
projslug="$workdir/projects/-some-slug"
mkdir -p "$projslug/memory"
transcript="$projslug/session.jsonl"
printf '%s\n' '{"type":"user","message":{"content":"hi"}}' > "$transcript"
printf '%s\n' '# Memory Index' '- [staging](reference_staging.md) — staging box' > "$projslug/memory/MEMORY.md"
{
  printf '%s\n' '---' 'name: staging-box' 'type: reference' '---'
  printf '%s\n' 'Staging at 18.211.217.170. Token sk-abcdefghijklmnopqrstuvwxyz0123 must not leak.'
} > "$projslug/memory/reference_staging.md"

payload="$(python3 -c 'import json,sys;print(json.dumps({"transcript_path":sys.argv[1],"cwd":sys.argv[2]}))' "$transcript" "$workdir")"
run(){ printf '%s' "$payload" | bash "$HOOK"; }

dest="$DEVBRAIN_DATA/projects/$DEVBRAIN_PROJECT/memory"

# Guard 1: no memory dir -> no-op (point transcript at a path with no sibling memory/).
nomem="$(python3 -c 'import json,sys;print(json.dumps({"transcript_path":sys.argv[1],"cwd":sys.argv[2]}))' "$workdir/none/session.jsonl" "$workdir")"
printf '%s' "$nomem" | bash "$HOOK"
check "no-op when no memory dir" '[ ! -d "$dest" ]'

# Real run.
run
check "mirrors memory files"       '[ -f "$dest/reference_staging.md" ] && [ -f "$dest/MEMORY.md" ]'
check "preserves frontmatter/body" 'grep -q "Staging at 18.211.217.170" "$dest/reference_staging.md"'
check "redacts secret in memory"   'grep -q "REDACTED" "$dest/reference_staging.md" && ! grep -q "sk-abcdefghijklmnopqrstuvwxyz0123" "$dest/reference_staging.md"'

# Idempotency: a second run with no source change rewrites nothing (mtime stable).
# Try GNU `stat -c %Y` FIRST: on GNU/Linux `-f` is *filesystem* status (volatile free
# blocks/inodes), not BSD's *format*, so `stat -f %m` there emits churning fs numbers and
# flaky-fails under load. `stat -c` errors cleanly on BSD/macOS -> falls back to `stat -f %m`.
mtime(){ stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null; }
before="$(mtime "$dest/reference_staging.md")"
sleep 1; run
after="$(mtime "$dest/reference_staging.md")"
check "idempotent (unchanged file not rewritten)" '[ "$before" = "$after" ]'

# A changed source file gets re-mirrored.
printf '\nNew fact added.\n' >> "$projslug/memory/reference_staging.md"
run
check "re-mirrors changed file"    'grep -q "New fact added." "$dest/reference_staging.md"'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
