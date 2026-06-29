#!/usr/bin/env bash
# devbrain — link-preferences.sh tests. Wires a throwaway ~/.claude/CLAUDE.md to import
# a global preferences page, all offline. One temp HOME/DATA drives every assertion
# (wire, idempotency, preserve existing memory, unlink) — no services, no sleep.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; L="$HERE/link-preferences.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
export DEVBRAIN_DATA="$TMP/data"
export CLAUDE_CONFIG_DIR="$TMP/claude"
MEM="$CLAUDE_CONFIG_DIR/CLAUDE.md"
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# ── wire into a fresh (nonexistent) user memory ──────────────────────────────
bash "$L" >/dev/null
check "creates user memory"              '[ -f "$MEM" ]'
check "adds the import line"             'grep -qF "/preferences/global.md" "$MEM"'
check "import uses @ syntax"             'grep -qE "^@.*/preferences/global.md$" "$MEM"'
check "adds the managed marker"          'grep -qF "devbrain: global preferences" "$MEM"'

# ── idempotent: re-run adds nothing ──────────────────────────────────────────
bash "$L" >/dev/null
check "single import after re-run"       '[ "$(grep -cF "/preferences/global.md" "$MEM")" = "1" ]'

# ── preserves existing user-memory content ───────────────────────────────────
printf '# My rules\n\nKeep this line.\n' > "$MEM"
bash "$L" >/dev/null
check "preserves hand-written memory"    'grep -qF "Keep this line." "$MEM"'
check "appends import after it"          'grep -qF "/preferences/global.md" "$MEM"'

# ── unlink removes the managed lines, keeps the rest ─────────────────────────
bash "$L" --unlink >/dev/null
check "unlink drops the import"          '! grep -qF "/preferences/global.md" "$MEM"'
check "unlink drops the marker"          '! grep -qF "devbrain: global preferences" "$MEM"'
check "unlink keeps user content"        'grep -qF "Keep this line." "$MEM"'

# ── unlink that empties the file must NOT leave it wired (the pipefail trap) ──
rm -f "$MEM"; bash "$L" >/dev/null            # file = only the 2 managed lines
bash "$L" --unlink >/dev/null
check "unlink fully clears managed-only file" '! grep -qF "/preferences/global.md" "$MEM"'

echo "== link-preferences: $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
