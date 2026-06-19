#!/usr/bin/env bash
# devbrain — nightshift green-gate tests. Sources the orchestrator's functions
# (NIGHTSHIFT_LIB mode, no fleet) and checks the two decisions that matter:
#   1. pick_gate_python selects an interpreter matching the project's requires-python
#   2. base_gate flags a RED base ONLY on a real test failure, not a collection/import error
# Pure-function tests — a single `claude` stub to satisfy the preflight, no venv/services.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ORCH="$HERE/nightshift-orchestrate.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

BIN="$TMP/bin"; mkdir -p "$BIN"; printf '#!/usr/bin/env bash\nexit 0\n' > "$BIN/claude"; chmod +x "$BIN/claude"
export PATH="$BIN:$PATH"

BASE="$TMP/repo"; mkdir -p "$BASE"
NIGHTSHIFT_LIB=1 . "$ORCH" --repo "$BASE" >/dev/null 2>&1   # the guard returns before boot

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# ── pick_gate_python honors requires-python (floor + optional ceiling) ─────────
pyproject(){ printf '[project]\n%s\n' "$1" > "$BASE/pyproject.toml"; }
pyproject 'requires-python = ">=3.99"';      check "unsatisfiable floor → none"      '[ -z "$(pick_gate_python)" ]'
pyproject 'requires-python = ">=3.0"';       check "satisfiable floor → picks one"   '[ -n "$(pick_gate_python)" ]'
pyproject 'requires-python = ">=3.0,<3.1"';  check "exclusive cap <3.1 → none"       '[ -z "$(pick_gate_python)" ]'
pyproject 'requires-python = ">=3.0,<=3.0"'; check "inclusive cap <=3.0 → none"      '[ -z "$(pick_gate_python)" ]'
pyproject 'requires-python = ">=3.0,<4.0"';  check "<4.0 is no real ceiling → picks" '[ -n "$(pick_gate_python)" ]'
pyproject 'name = "x"';                      check "no floor declared → picks one"   '[ -n "$(pick_gate_python)" ]'
rm -f "$BASE/pyproject.toml";                check "no pyproject → picks one"        '[ -n "$(pick_gate_python)" ]'

# ── base_gate goes RED only on a real test FAILED, not a collection/import error ─
# Stub run_gate's verdict (the single input base_gate decides on) — no venv needed.
NO_GATE=0; NOTIFY=0; STAGE_WT="$TMP/stage"   # base_gate pokes git here, best-effort (2>/dev/null)
bg(){ base_gate >/dev/null 2>&1; }
run_gate(){ GATE_IMPORT_ERROR=1; return 1; }; check "import/collection error is NOT red" 'bg; [ "$?" -eq 0 ]'
run_gate(){ GATE_IMPORT_ERROR=0; return 1; }; check "real test FAILED IS red"            'bg; [ "$?" -eq 1 ]'
run_gate(){ GATE_IMPORT_ERROR=0; return 0; }; check "passing gate is green"              'bg; [ "$?" -eq 0 ]'
run_gate(){ GATE_IMPORT_ERROR=0; return 2; }; check "inconclusive gate is green"         'bg; [ "$?" -eq 0 ]'
NO_GATE=1;                                    check "--no-gate short-circuits green"     'bg; [ "$?" -eq 0 ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
