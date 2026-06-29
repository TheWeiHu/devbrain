#!/usr/bin/env bash
# devbrain — nightshift state-consistency regressions. Task status (open/taken/done) and the
# `nightshift` branch are two sources of truth that drift apart on transitions; these pin the
# assignment-lock + empty-turn + shutdown-release fixes. Sources the orchestrator (NIGHTSHIFT_LIB
# mode, no fleet) and drives the real functions against throwaway git repos + a fake queue.
# (The reset/discard recovery is covered separately by test-nightshift-guards.sh.)
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ORCH="$HERE/nightshift-orchestrate.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# claude on PATH so the lib's command -v guard passes; it's never invoked here.
BIN="$TMP/bin"; mkdir -p "$BIN"; printf '#!/usr/bin/env bash\nexit 0\n' > "$BIN/claude"; chmod +x "$BIN/claude"
export PATH="$BIN:$PATH"; export DEVBRAIN_DATA="$TMP/data"
GIT="git -c user.email=a@b.c -c user.name=t"

# A bare remote with main + a nightshift branch, and a clone to drive (origin = github-style so the
# queue resolves a project key; a separate `up` remote carries the real branches).
REM="$TMP/rem.git"; git init -q --bare "$REM"
SEED="$TMP/seed"; git clone -q "$REM" "$SEED" 2>/dev/null
( cd "$SEED" && echo base > f && $GIT add . && $GIT commit -qm init && git push -q origin HEAD:main
  git checkout -q -b nightshift && git push -q origin nightshift )
BASE="$TMP/clone"; git clone -q "$REM" "$BASE"
git -C "$BASE" remote remove origin 2>/dev/null; git -C "$BASE" remote add origin git@github.com:test/repo.git
git -C "$BASE" remote add up "$REM"; git -C "$BASE" fetch -q up
git -C "$BASE" update-ref refs/remotes/origin/main up/main
git -C "$BASE" update-ref refs/remotes/origin/nightshift up/nightshift

TD="$DEVBRAIN_DATA/projects/test__repo/todo"; mkdir -p "$TD"
mkstatus(){ printf -- '---\nid: %s\nstatus: %s\npriority: 50\ncreated: 2026-06-20T00:00:00Z\nclaimed_by: %s\nclaimed_at: %s\npr:\n%s---\n# %s\n' \
  "$1" "$2" "$4" "$5" "${6:-}" "$3" > "$TD/$1.md"; }

# Source the library (functions only — the guard returns before any fleet boots).
NIGHTSHIFT_LIB=1 . "$ORCH" --repo "$BASE" --no-gate >/dev/null 2>&1
TODO="$HERE/todo.sh"   # use the repo todo (deterministic; same pattern as the fence test)
st(){ ( cd "$BASE" && "$TODO" show "$1" 2>/dev/null ) | sed -n 's/^status:[[:space:]]*//p' | head -1; }

echo "== Bug 1b — an empty turn (no commit) is not counted as landed =="
WT0="$TMP/wt-empty"; git clone -q "$REM" "$WT0" 2>/dev/null; git -C "$WT0" checkout -q -B todo/0099-eee origin/nightshift
B0="$(git -C "$WT0" rev-parse HEAD)"
check "no commits since fork base → empty turn" '! turn_made_commits "$WT0" "$B0"'
( cd "$WT0" && echo work > essay-0099 && $GIT add . && $GIT commit -qm "essay 0099" )
check "one commit since fork base → real turn"  'turn_made_commits "$WT0" "$B0"'
check "missing fork base → cannot prove empty"  'turn_made_commits "$WT0" ""'

echo "== Bug 2 — shutdown releases EVERY taken task in scope =="
mkstatus 0101-fff taken "in-flight A" w@h 2026-06-20T00:00:00Z
mkstatus 0102-ggg taken "in-flight B" w@h 2026-06-20T00:00:00Z
mkstatus 0103-hhh taken "in-flight C" w@h 2026-06-20T00:00:00Z
check "three tasks taken before shutdown" '[ "$( ( cd "$BASE" && "$TODO" list taken 2>/dev/null ) | grep -cE "010[123]-" )" -eq 3 ]'
# Drive the orchestrator's shutdown reaper: no live children, worktrees not on todo branches,
# so the per-worker release is a no-op and ONLY the taken-sweep can free them.
N=3; MODE=headless; CLEANED=0; FIXED_SET=0
WT=("$TMP/none0" "$TMP/none1" "$TMP/none2"); WTPID=("" "" "")
cleanup >/dev/null 2>&1
check "0101 released to open on shutdown" '[ "$(st 0101-fff)" = open ]'
check "0102 released to open on shutdown" '[ "$(st 0102-ggg)" = open ]'
check "0103 released to open on shutdown" '[ "$(st 0103-hhh)" = open ]'
check "no task left taken after shutdown" '[ "$( ( cd "$BASE" && "$TODO" list taken 2>/dev/null ) | grep -cE "010[123]-" )" -eq 0 ]'
# A HELD task (merge hit the retry cap) must SURVIVE shutdown — the per-worker release is gated on
# WTPID and the sweep only lists `taken`, so neither reopens it and defeats the hold.
mkstatus 0104-iii held "held" "" "" "reason: gate failed (after 2 attempts)
"
CLEANED=0; cleanup >/dev/null 2>&1
check "held task survives shutdown (not reopened)" '[ "$(st 0104-iii)" = held ]'

echo "== Bug 1a — at most ONE worker is assigned per open task in a poll =="
# Drive the REAL hl_step idle path with a stub launcher; count how many of N idle workers get a
# turn when only `oc` tasks are open. Pre-fix, every idle worker fired (open=1 → 8 duplicate turns).
ASSIGNED=""; run_headless_turn(){ ASSIGNED="$ASSIGNED $1"; }   # stub: record, don't launch claude
STALLED=0; NOMERGE=0; BASE_RED=0; FIXED_SET=1; now=1000; PLANNED_LAST=1000; REPLAN=300
WTPID=(); STATE=(); for i in $(seq 0 7); do WTPID[$i]=""; STATE[$i]=idle; done
assign_round(){ oc="$1"; BR_ASSIGNED=0; ASSIGNED=""; for i in $(seq 0 7); do hl_step "$i" >/dev/null 2>&1; done; }
assign_round 1; check "open=1, 8 idle → exactly 1 assigned" '[ "$(printf %s "$ASSIGNED" | wc -w | tr -d " ")" -eq 1 ]'
assign_round 3; check "open=3, 8 idle → exactly 3 assigned" '[ "$(printf %s "$ASSIGNED" | wc -w | tr -d " ")" -eq 3 ]'
assign_round 0; check "open=0 (fixed-set) → none assigned"  '[ -z "${ASSIGNED// /}" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
