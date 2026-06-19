#!/usr/bin/env bash
# devbrain — nightshift green-gate tests. Sources the orchestrator in NIGHTSHIFT_LIB
# mode (functions only, no fleet) and exercises the gate-correctness fixes from the
# field report: F1 (gate venv honors requires-python) and F2 (collection/import errors
# are NOT a red base). No real services — stubbed python/pip/pytest, fast.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ORCH="$HERE/nightshift-orchestrate.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# Stub the binaries the orchestrator preflight + gate shell out to, so the test runs
# anywhere (no claude, no GNU timeout needed). timeout just drops its duration arg.
BIN="$TMP/bin"; mkdir -p "$BIN"
printf '#!/usr/bin/env bash\nexit 0\n' > "$BIN/claude"
printf '#!/usr/bin/env bash\nshift; exec "$@"\n' > "$BIN/timeout"
chmod +x "$BIN/claude" "$BIN/timeout"
export PATH="$BIN:$PATH"

BASE="$TMP/repo"; mkdir -p "$BASE"
# Source functions only (the guard returns before boot). --repo satisfies the preflight.
NIGHTSHIFT_LIB=1 . "$ORCH" --repo "$BASE" >/dev/null 2>&1

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# ── F1: pick_gate_python honors requires-python ───────────────────────────────
writepyproject(){ printf '[project]\n%s\n' "$1" > "$BASE/pyproject.toml"; }

writepyproject 'requires-python = ">=3.99"'
check "F1 unsatisfiable requires-python → no interpreter" '[ -z "$(pick_gate_python)" ]'

writepyproject 'requires-python = ">=3.0"'
check "F1 satisfiable floor → picks an interpreter"       '[ -n "$(pick_gate_python)" ]'

writepyproject 'name = "x"'   # no requires-python line
check "F1 no floor declared → picks an interpreter"       '[ -n "$(pick_gate_python)" ]'

rm -f "$BASE/pyproject.toml"
check "F1 no pyproject → picks an interpreter"             '[ -n "$(pick_gate_python)" ]'

# ── F2: run_gate classifies ERROR-only (collection/import) vs real FAILED ──────
# Stub the gate venv so run_gate's pytest branch runs without a real interpreter.
# (Do this BEFORE the base_gate block below, which replaces run_gate with a stub.)
VENV="$TMP/venv"; mkdir -p "$VENV/bin"; TEST_CMD=""
printf '#!/usr/bin/env bash\nexit 0\n' > "$VENV/bin/pip"
# python stub: on `-m pytest` echo the canned output + rc; otherwise succeed.
cat > "$VENV/bin/python" <<'PY'
#!/usr/bin/env bash
case " $* " in *" pytest "*) cat "$VENV_FAKE_OUT" 2>/dev/null; exit "$(cat "$VENV_FAKE_RC" 2>/dev/null || echo 0)";; esac
exit 0
PY
chmod +x "$VENV/bin/pip" "$VENV/bin/python"
export VENV_FAKE_OUT="$VENV/.out" VENV_FAKE_RC="$VENV/.rc"
fake(){ printf '%s\n' "$1" > "$VENV_FAKE_OUT"; printf '%s' "$2" > "$VENV_FAKE_RC"; }

fake "ERROR tests/test_a.py - ImportError" 2
check "F2 run_gate ERROR-only → fail + import flag" 'run_gate "$BASE" >/dev/null 2>&1; rc=$?; [ "$rc" -eq 1 ] && [ "$GATE_IMPORT_ERROR" -eq 1 ]'

fake "FAILED tests/test_a.py::test_x - assert" 1
check "F2 run_gate real FAILED → fail, NOT import" 'run_gate "$BASE" >/dev/null 2>&1; rc=$?; [ "$rc" -eq 1 ] && [ "$GATE_IMPORT_ERROR" -eq 0 ]'

fake "" 0
check "F2 run_gate pass → 0"                       'run_gate "$BASE" >/dev/null 2>&1; [ "$?" -eq 0 ]'

fake "" 5
check "F2 run_gate no tests → inconclusive(2)"     'run_gate "$BASE" >/dev/null 2>&1; [ "$?" -eq 2 ]'

# ── F2: base_gate goes RED only on a genuine test FAILED, not collection/import ─
# These REPLACE run_gate with stubs (must run after the run_gate tests above).
NO_GATE=0; NOTIFY=0; STAGE_WT="$TMP/stage"   # base_gate touches git on these (best-effort, 2>/dev/null)
bg(){ base_gate >/dev/null 2>&1; }   # returns base_gate's rc with git noise suppressed

run_gate(){ GATE_IMPORT_ERROR=1; GATE_DETAIL="ERROR collect"; return 1; }   # import/collection error
check "F2 import error is NOT a red base"  'bg; [ "$?" -eq 0 ]'

run_gate(){ GATE_IMPORT_ERROR=0; GATE_DETAIL="FAILED test_x"; return 1; }   # genuine assertion failure
check "F2 real test FAILED IS a red base"  'bg; [ "$?" -eq 1 ]'

run_gate(){ GATE_IMPORT_ERROR=0; return 0; }
check "F2 passing gate is green"           'bg; [ "$?" -eq 0 ]'

run_gate(){ GATE_IMPORT_ERROR=0; return 2; }
check "F2 inconclusive gate is green"      'bg; [ "$?" -eq 0 ]'

NO_GATE=1
check "F2 --no-gate short-circuits green"  'bg; [ "$?" -eq 0 ]'
NO_GATE=0

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
