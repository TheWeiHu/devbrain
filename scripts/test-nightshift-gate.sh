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
pyproject 'requires-python = "==3.99"';      check "exact pin ==3.99 → none"         '[ -z "$(pick_gate_python)" ]'
pyproject 'requires-python = "~=3.0"';       check "compatible-release ~=3.0 → picks" '[ -n "$(pick_gate_python)" ]'
pyproject 'name = "x"';                      check "no floor declared → picks one"   '[ -n "$(pick_gate_python)" ]'
rm -f "$BASE/pyproject.toml";                check "no pyproject → picks one"        '[ -n "$(pick_gate_python)" ]'

# ── run_gate strips DEVBRAIN_TODO_ONLY so the fixed-set fence can't poison the suite ─
# In --only runs the orchestrator exports DEVBRAIN_TODO_ONLY to fence the live queue, but
# the gate's tests build their own throwaway queues and must NOT inherit it — otherwise
# todo-queue tests see an empty fenced queue and fail, false-REDing the gate.
export DEVBRAIN_TODO_ONLY=9999-nonexistent DEVBRAIN_TODO_DERIVE_GIT=1
TEST_CMD='[ -z "$DEVBRAIN_TODO_ONLY" ] && [ -z "$DEVBRAIN_TODO_DERIVE_GIT" ]'   # passes only if both cleared
check "gate strips DEVBRAIN_TODO_ONLY + DERIVE_GIT" 'run_gate "$TMP" >/dev/null 2>&1; [ "$?" -eq 0 ]'
unset DEVBRAIN_TODO_ONLY DEVBRAIN_TODO_DERIVE_GIT; TEST_CMD=""

# ── run_gate retries once so a single flaky test can't RED the base and deadlock every merge ─
gcnt="$TMP/gate_attempts"; : > "$gcnt"
TEST_CMD='c=$(wc -c < '"$gcnt"'); printf x >> '"$gcnt"'; (( c >= 1 ))'   # fail 1st attempt, pass 2nd
check "gate retries a one-off flake → pass" 'run_gate "$TMP" >/dev/null 2>&1; [ "$?" -eq 0 ]'
check "gate ran exactly twice (one retry)"  '[ "$(wc -c < "'"$gcnt"'" | tr -d " ")" = 2 ]'
TEST_CMD='false';                           check "persistent failure still FAILs" 'run_gate "$TMP" >/dev/null 2>&1; [ "$?" -eq 1 ]'
TEST_CMD=""

# ── base_gate goes RED only on a real test FAILED, not a collection/import error ─
# Stub run_gate's verdict (the single input base_gate decides on) — no venv needed.
NO_GATE=0; NOTIFY=0; STAGE_WT="$TMP/stage"   # base_gate pokes git here, best-effort (2>/dev/null)
bg(){ base_gate >/dev/null 2>&1; }
run_gate(){ GATE_IMPORT_ERROR=1; return 1; }; check "import/collection error is NOT red" 'bg; [ "$?" -eq 0 ]'
run_gate(){ GATE_IMPORT_ERROR=0; return 1; }; check "real test FAILED IS red"            'bg; [ "$?" -eq 1 ]'
run_gate(){ GATE_IMPORT_ERROR=0; return 0; }; check "passing gate is green"              'bg; [ "$?" -eq 0 ]'
run_gate(){ GATE_IMPORT_ERROR=0; return 2; }; check "inconclusive gate is green"         'bg; [ "$?" -eq 0 ]'
NO_GATE=1;                                    check "--no-gate short-circuits green"     'bg; [ "$?" -eq 0 ]'

# ── ci_scope_unsafe: flags a pull_request trigger that fires on per-task PRs ───
# CI must run only on main; a workflow that CIs `-> nightshift` PRs is unsafe.
wf="$TMP/wf.yml"; w(){ printf '%s\n' "$1" > "$wf"; }
w 'name: t
on:
  pull_request:
  push:
    branches: [main]';                          check "bare pull_request → unsafe"        'ci_scope_unsafe "$wf"'
w 'name: t
on:
  pull_request:
    branches: [main]
  push:
    branches: [main]';                          check "pull_request scoped to main → safe" '! ci_scope_unsafe "$wf"'
w 'on: pull_request';                           check "inline on: pull_request → unsafe"  'ci_scope_unsafe "$wf"'
w 'on: [push, pull_request]';                   check "inline flow-list pull_request → unsafe" 'ci_scope_unsafe "$wf"'
w 'on:
  - push
  - pull_request';                              check "block-list pull_request → unsafe"  'ci_scope_unsafe "$wf"'
w 'on:
  - push';                                      check "block-list without pull_request → safe" '! ci_scope_unsafe "$wf"'
w 'on:
  pull_request:
    branches:
      - main
      - nightshift';                            check "branches include nightshift → unsafe" 'ci_scope_unsafe "$wf"'
w 'on:
  push:
    branches: [main]';                          check "no pull_request trigger → safe"    '! ci_scope_unsafe "$wf"'
check "missing workflow file → safe"            '! ci_scope_unsafe "$TMP/nope.yml"'
# The repo's own workflow must be scoped (regression guard for the shipped fix).
check "shipped test.yml is scoped to main"      '! ci_scope_unsafe "$HERE/../.github/workflows/test.yml"'

# ── fixed-set: a red base must NOT file a fix task — the fenced fleet can't see it (deadlock),
# and every red gate re-run would drop another orphan "NIGHTSHIFT IS RED" task into the queue.
added="$TMP/fix_added"; : > "$added"
todo(){ [ "${1:-}" = add ] && echo x >> "$added"; return 0; }
todo_all(){ return 0; }
FIXED_SET=1; ensure_base_fix_task "detail" >/dev/null 2>&1
check "fixed-set: red base files NO fix task"    '[ ! -s "$added" ]'
FIXED_SET=0; ensure_base_fix_task "detail" >/dev/null 2>&1
check "unbounded: red base files the fix task"   '[ -s "$added" ]'
# Dedup must read the WHOLE queue (todo_all) — an ONLY-scoped view hides the existing task.
: > "$added"
todo_all(){ printf '[open] NIGHTSHIFT IS RED — fix the failing test(s) to unblock all merges\n'; }
ensure_base_fix_task "detail" >/dev/null 2>&1
check "dedup sees the whole queue (no duplicate)" '[ ! -s "$added" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
