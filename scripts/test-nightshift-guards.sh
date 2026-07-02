#!/usr/bin/env bash
# devbrain — nightshift INPUT/OUTPUT guard-rail tests.
#   Bug 5 (input precondition): a present-but-empty/unparseable `--only` must be a HARD ERROR,
#     never a silent no-op that degrades to an unfenced "run everything, forever" run.
#   Bug 4 (output post-condition): a fixed-set run may only report success after VERIFYING that
#     every selected `done` task's work is actually present on origin/nightshift — the landing
#     SHA it recorded must still be an ancestor. A base reset drops those SHAs → loud INCOMPLETE.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ORCH="$HERE/nightshift-orchestrate.sh"
TODO="$HERE/todo.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# claude stub (the orchestrator refuses to run without it on PATH)
BIN="$TMP/bin"; mkdir -p "$BIN"; printf '#!/usr/bin/env bash\nexit 0\n' > "$BIN/claude"; chmod +x "$BIN/claude"
export PATH="$BIN:$PATH"
export DEVBRAIN_DATA="$TMP/data"
unset DEVBRAIN_TODO_ONLY DEVBRAIN_TODO_DERIVE_GIT   # the env-containment checks below assert these stay unset
export DEVBRAIN_PROJECT=test__repo   # pin queue identity so it's independent of the (local) remote URL
GIT="git -c user.email=a@b.c -c user.name=t"

# a bare remote with main, a clone standing in for ~/nightshift/<repo>, and a queue
REM="$TMP/rem.git"; git init -q --bare "$REM"
SEED="$TMP/seed"; git clone -q "$REM" "$SEED"
( cd "$SEED" && echo base > f && $GIT add . && $GIT commit -qm init && git push -q origin HEAD:main )
BASE="$TMP/repo"; git clone -q "$REM" "$BASE"
TD="$DEVBRAIN_DATA/projects/test__repo/todo"; mkdir -p "$TD"
mk(){ printf -- '---\nid: %s\nstatus: %s\npriority: 50\ncreated: 2026-06-20T00:00:00Z\nclaimed_by:\nclaimed_at:\npr:\n---\n# %s\n' "$1" "$2" "$3" > "$TD/$1.md"; }
mk 0001-alpha open  "Alpha"; mk 0002-beta open "Beta"

echo "== Bug 5 — --only input precondition (LIB-mode source: validates, then returns before the fleet) =="
# A FATAL exit() / a clean return() both end the sourced-in-subshell; capture the exit code + output.
# Capture rc + output WITHOUT a pipe (pipefail would otherwise mask grep behind the FATAL exit).
only_rc(){ ( NIGHTSHIFT_LIB=1 . "$ORCH" --repo "$BASE" --only "$1" ) >/dev/null 2>&1; echo $?; }
OUT=""; only_out(){ OUT="$( ( NIGHTSHIFT_LIB=1 . "$ORCH" --repo "$BASE" --only "$1" ) 2>&1 )"; }
check "empty --only is a hard error"             '[ "$(only_rc "")" = 1 ]'
check "whitespace/comma-only --only is an error" '[ "$(only_rc " , , ")" = 1 ]'
check "all-unknown --only is an error"           '[ "$(only_rc "9999,8888")" = 1 ]'
check "valid --only is accepted (returns 0)"     '[ "$(only_rc "0001,0002")" = 0 ]'
check "valid --only echoes the resolved fence (canonical slugs)" 'only_out "0001,0002"; printf "%s" "$OUT" | grep -q "fixed set:.*0001-alpha"'
check "empty --only names the danger"            'only_out ""; printf "%s" "$OUT" | grep -qi "unfenced run"'
check "mixed valid+unknown warns but proceeds"   'only_out "0001,7777"; printf "%s" "$OUT" | grep -q "no such task.*7777"'
check "mixed valid+unknown still starts (rc 0)"  '[ "$(only_rc "0001,7777")" = 0 ]'

echo "== Bug 4 — output post-condition (landing SHAs must survive on origin/nightshift) =="
# Build origin/nightshift, then source the orchestrator's functions (LIB mode: no fleet booted).
git -C "$BASE" fetch -q origin
git -C "$BASE" branch -f nightshift origin/main && git -C "$BASE" push -q origin nightshift
git -C "$BASE" fetch -q origin
NIGHTSHIFT_LIB=1 . "$ORCH" --repo "$BASE" --only 0001-alpha,0002-beta >/dev/null 2>&1
TODO="$HERE/todo.sh"   # sourcing the orchestrator reset TODO to the INSTALLED copy — pin it back to the repo's
mkdir -p "$BASE/.nightshift"

echo "== env containment — the queue env must never be exported process-wide =="
# The #164/#169 leak class: an exported DEVBRAIN_TODO_ONLY / DEVBRAIN_TODO_DERIVE_GIT reaches
# every child the orchestrator spawns (the green-gate's suite most painfully). The vars now
# live only in the todo wrappers + the per-worker launch env — so after sourcing a fixed-set
# run, the process env must NOT carry them, while the wrappers still apply them per call.
mk 0003-gamma open "Gamma"   # out-of-set: visible to todo_all, invisible to the scoped todo
check "--only does not export DEVBRAIN_TODO_ONLY"     '[ -z "$(printenv DEVBRAIN_TODO_ONLY)" ]'
check "boot does not export DEVBRAIN_TODO_DERIVE_GIT" '[ -z "$(printenv DEVBRAIN_TODO_DERIVE_GIT)" ]'
# grep WITHOUT -q throughout: `list` writes a line at a time, so a -q early-exit SIGPIPEs it
# mid-write and pipefail turns the real match into a ~50% flake (this exact check RED-gated
# live nightshift runs). Draining the stream makes the pipeline deterministic.
check "todo wrapper scopes to the fixed set"          'todo list 2>/dev/null | grep 0001-alpha >/dev/null && ! todo list 2>/dev/null | grep 0003-gamma >/dev/null'
check "todo_all wrapper sees the whole queue"         'todo_all list 2>/dev/null | grep 0003-gamma >/dev/null'
rm -f "$TD/0003-gamma.md"   # keep the rest of the test's queue exactly as before

# land 0001: a real commit on nightshift, then record_landed stamps the post-push SHA
git -C "$BASE" checkout -q nightshift
echo work0001 > "$BASE/g" && git -C "$BASE" add g && $GIT -C "$BASE" commit -qm "work 0001"
$GIT -C "$BASE" commit --allow-empty -qm "nightshift: merge todo/0001-alpha into nightshift"
git -C "$BASE" push -q origin nightshift; git -C "$BASE" fetch -q origin
record_landed 0001-alpha
GOOD_SHA="$(git -C "$BASE" rev-parse origin/nightshift)"
check "record_landed writes a landing SHA"      '[ -n "$(landed_sha 0001-alpha)" ]'
check "landed SHA == current origin/nightshift" '[ "$(landed_sha 0001-alpha)" = "$GOOD_SHA" ]'

# Mark both done; 0001 landed (present), 0002 done but never landed (the silent-loss case).
( cd "$BASE" && "$TODO" done 0001-alpha >/dev/null 2>&1 )
( cd "$BASE" && "$TODO" done 0002-beta  >/dev/null 2>&1 )
ONLY="0001-alpha"
check "verify PASSES when the done task's work is present" 'fixedset_verify >/dev/null 2>&1'
ONLY="0001-alpha,0002-beta"
check "derived status makes absent done work unresolved"  '[ "$(fixedset_unresolved)" -eq 1 ]'
check "verify ignores absent stored-done work no longer derived as done" 'fixedset_verify >/dev/null 2>&1'

# Simulate a hard base RESET: move nightshift off the landed history (the Bug 3 → Bug 4 root cause).
# nightshift is the checked-out branch, so reset --hard (not branch -f) is how the orchestrator moves it.
git -C "$BASE" checkout -q nightshift; git -C "$BASE" reset --hard -q origin/main && git -C "$BASE" push -f -q origin nightshift
git -C "$BASE" fetch -q origin
ONLY="0001-alpha"
check "after a reset, derived status reopens previously-present work" '[ "$(fixedset_unresolved)" -eq 1 ]'

echo "== reopen verb — force a verified-absent done task back to open =="
( cd "$BASE" && "$TODO" done 0001-alpha >/dev/null 2>&1 )
check "release REFUSES to reopen a done task" '( cd "$BASE" && "$TODO" release 0001-alpha >/dev/null 2>&1; [ "$( ( cd "$BASE" && "$TODO" show 0001-alpha ) | sed -n "s/^status: //p")" = done ] )'
( cd "$BASE" && "$TODO" reopen 0001-alpha >/dev/null 2>&1 )
check "reopen forces done -> open"      '[ "$( ( cd "$BASE" && "$TODO" show 0001-alpha ) | sed -n "s/^status: //p")" = open ]'
check "reopen clears the done_at stamp" '[ -z "$( ( cd "$BASE" && "$TODO" show 0001-alpha ) | sed -n "s/^done_at: //p")" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
