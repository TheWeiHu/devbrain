#!/usr/bin/env bash
# devbrain — nightshift green-gate tests. Drives the Go port's plumbing verbs
# (`devbrain nightshift internal …` — the same functions the orchestrator uses)
# and checks the two decisions that matter:
#   1. pick_gate_python selects an interpreter matching the project's requires-python
#   2. base_gate flags a RED base ONLY on a real test failure, not a collection/import error
# Pure-function tests — a single `claude` stub to satisfy the preflight, no venv/services.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ROOT="$HERE/.."
BIN="${DEVBRAIN_BIN:-$ROOT/devbrain}"
[ -x "$BIN" ] || { echo "skip: devbrain binary not built (go build -o devbrain ./cmd/devbrain)"; exit 0; }
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

BIND="$TMP/bin"; mkdir -p "$BIND"; printf '#!/usr/bin/env bash\nexit 0\n' > "$BIND/claude"; chmod +x "$BIND/claude"
export PATH="$BIND:$PATH"

BASE="$TMP/repo"; mkdir -p "$BASE"
ns(){ "$BIN" nightshift internal "$@" --repo "$BASE"; }

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# ── pick_gate_python honors requires-python (floor + optional ceiling) ─────────
pyproject(){ printf '[project]\n%s\n' "$1" > "$BASE/pyproject.toml"; }
pyproject 'requires-python = ">=3.99"';      check "unsatisfiable floor → none"      '[ -z "$(ns pick-gate-python)" ]'
pyproject 'requires-python = ">=3.0"';       check "satisfiable floor → picks one"   '[ -n "$(ns pick-gate-python)" ]'
pyproject 'requires-python = ">=3.0,<3.1"';  check "exclusive cap <3.1 → none"       '[ -z "$(ns pick-gate-python)" ]'
pyproject 'requires-python = ">=3.0,<=3.0"'; check "inclusive cap <=3.0 → none"      '[ -z "$(ns pick-gate-python)" ]'
pyproject 'requires-python = ">=3.0,<4.0"';  check "<4.0 is no real ceiling → picks" '[ -n "$(ns pick-gate-python)" ]'
pyproject 'requires-python = "==3.99"';      check "exact pin ==3.99 → none"         '[ -z "$(ns pick-gate-python)" ]'
pyproject 'requires-python = "~=3.0"';       check "compatible-release ~=3.0 → picks" '[ -n "$(ns pick-gate-python)" ]'
pyproject 'name = "x"';                      check "no floor declared → picks one"   '[ -n "$(ns pick-gate-python)" ]'
rm -f "$BASE/pyproject.toml";                check "no pyproject → picks one"        '[ -n "$(ns pick-gate-python)" ]'

# ── run_gate strips DEVBRAIN_TODO_ONLY so the fixed-set fence can't poison the suite ─
# In --only runs the queue env fences the live queue, but the gate's tests build their
# own throwaway queues and must NOT inherit it — otherwise todo-queue tests see an empty
# fenced queue and fail, false-REDing the gate.
export DEVBRAIN_TODO_ONLY=9999-nonexistent DEVBRAIN_TODO_DERIVE_GIT=1
TEST_CMD='[ -z "$DEVBRAIN_TODO_ONLY" ] && [ -z "$DEVBRAIN_TODO_DERIVE_GIT" ]'   # passes only if both cleared
check "gate strips DEVBRAIN_TODO_ONLY + DERIVE_GIT" 'ns run-gate "$TMP" --test-cmd "$TEST_CMD" >/dev/null 2>&1; [ "$?" -eq 0 ]'
unset DEVBRAIN_TODO_ONLY DEVBRAIN_TODO_DERIVE_GIT; TEST_CMD=""

# ── run_gate retries once so a single flaky test can't RED the base and deadlock every merge ─
gcnt="$TMP/gate_attempts"; : > "$gcnt"
TEST_CMD='c=$(wc -c < '"$gcnt"'); printf x >> '"$gcnt"'; (( c >= 1 ))'   # fail 1st attempt, pass 2nd
check "gate retries a one-off flake → pass" 'ns run-gate "$TMP" --test-cmd "$TEST_CMD" >/dev/null 2>&1; [ "$?" -eq 0 ]'
check "gate ran exactly twice (one retry)"  '[ "$(wc -c < "'"$gcnt"'" | tr -d " ")" = 2 ]'
check "persistent failure still FAILs" 'ns run-gate "$TMP" --test-cmd false >/dev/null 2>&1; [ "$?" -eq 1 ]'
TEST_CMD=""

# ── base_gate goes RED only on a real test FAILED, not a collection/import error ─
# classify-base takes run_gate's verdict (the single input base_gate decides on) — no venv needed.
bg(){ ns classify-base "$@" >/dev/null 2>&1; }
check "import/collection error is NOT red" 'bg --rc 1 --import-error; [ "$?" -eq 0 ]'
check "real test FAILED IS red"            'bg --rc 1; [ "$?" -eq 1 ]'
check "passing gate is green"              'bg --rc 0; [ "$?" -eq 0 ]'
check "inconclusive gate is green"         'bg --rc 2; [ "$?" -eq 0 ]'
check "--no-gate short-circuits green"     'bg --rc 1 --no-gate; [ "$?" -eq 0 ]'

# ── ci_scope_unsafe: flags a pull_request trigger that fires on per-task PRs ───
# CI must run only on main; a workflow that CIs `-> nightshift` PRs is unsafe.
wf="$TMP/wf.yml"; w(){ printf '%s\n' "$1" > "$wf"; }
w 'name: t
on:
  pull_request:
  push:
    branches: [main]';                          check "bare pull_request → unsafe"        'ns ci-scope-unsafe "$wf"'
w 'name: t
on:
  pull_request:
    branches: [main]
  push:
    branches: [main]';                          check "pull_request scoped to main → safe" '! ns ci-scope-unsafe "$wf"'
w 'on: pull_request';                           check "inline on: pull_request → unsafe"  'ns ci-scope-unsafe "$wf"'
w 'on: [push, pull_request]';                   check "inline flow-list pull_request → unsafe" 'ns ci-scope-unsafe "$wf"'
w 'on:
  - push
  - pull_request';                              check "block-list pull_request → unsafe"  'ns ci-scope-unsafe "$wf"'
w 'on:
  - push';                                      check "block-list without pull_request → safe" '! ns ci-scope-unsafe "$wf"'
w 'on:
  pull_request:
    branches:
      - main
      - nightshift';                            check "branches include nightshift → unsafe" 'ns ci-scope-unsafe "$wf"'
w 'on:
  push:
    branches: [main]';                          check "no pull_request trigger → safe"    '! ns ci-scope-unsafe "$wf"'
check "missing workflow file → safe"            '! ns ci-scope-unsafe "$TMP/nope.yml"'
# The repo's own workflow must be scoped (regression guard for the shipped fix).
check "shipped test.yml is scoped to main"      '! ns ci-scope-unsafe "$HERE/../.github/workflows/test.yml"'

# ── fixed-set: a red base must NOT file a fix task — the fenced fleet can't see it (deadlock),
# and every red gate re-run would drop another orphan "NIGHTSHIFT IS RED" task into the queue.
# A real throwaway queue replaces the bash test's todo() stubs.
export DEVBRAIN_DATA="$TMP/data" DEVBRAIN_PROJECT=test__repo
mkdir -p "$DEVBRAIN_DATA/projects/test__repo/todo"
red_count(){ ns todo-all list all 2>/dev/null | grep -c "NIGHTSHIFT IS RED"; }
ns ensure-base-fix-task --detail "detail" --fixed-set >/dev/null 2>&1
check "fixed-set: red base files NO fix task"    '[ "$(red_count)" -eq 0 ]'
ns ensure-base-fix-task --detail "detail" >/dev/null 2>&1
check "unbounded: red base files the fix task"   '[ "$(red_count)" -eq 1 ]'
# Dedup must read the WHOLE queue (todo_all) — an ONLY-scoped view hides the existing task.
ns ensure-base-fix-task --detail "detail" --only 9999-nonexistent >/dev/null 2>&1
check "dedup sees the whole queue (no duplicate)" '[ "$(red_count)" -eq 1 ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
