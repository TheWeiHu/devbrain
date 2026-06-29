#!/usr/bin/env bash
# devbrain — nightshift shared assignment policy. After the orchestrator simplification, BOTH
# backends (headless + tmux) decide a worker's next turn through ONE function, pick_turn(), which
# sets $PICK to the prompt to launch ("" = park) and updates the shared throttles (BR_ASSIGNED,
# PLANNED_LAST). These tests pin that decision tree so the two backends can't drift apart again.
# Sources the orchestrator's functions (NIGHTSHIFT_LIB mode, no fleet) and drives pick_turn with
# explicit inputs — the globals it reads live in the main loop, so the test sets them directly.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ORCH="$HERE/nightshift-orchestrate.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

BIN="$TMP/bin"; mkdir -p "$BIN"; printf '#!/usr/bin/env bash\nexit 0\n' > "$BIN/claude"; chmod +x "$BIN/claude"
export PATH="$BIN:$PATH"
export DEVBRAIN_DATA="$TMP/data"
BASE="$TMP/repo"; mkdir -p "$BASE"; git -C "$BASE" init -q
git -C "$BASE" remote add origin git@github.com:test/repo.git

NIGHTSHIFT_LIB=1 . "$ORCH" --repo "$BASE" >/dev/null 2>&1   # gives us pick_turn() + PLAN_RULES

pass=0; fail=0; PICK=""
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ] (PICK='${PICK:0:24}')"; fi; }
# Inputs pick_turn reads — these live in the main loop, unset under NIGHTSHIFT_LIB, so we set them.
now=1000; REPLAN=300; STALL_K=8

# open work → assign /continue, and bump the red-base throttle
STALLED=0; NOMERGE=0; BASE_RED=0; BR_ASSIGNED=0; oc=3; FIXED_SET=0
pick_turn 0
check "open work → /continue"                  '[ "$PICK" = "/continue" ]'
check "/continue bumps BR_ASSIGNED"            '[ "$BR_ASSIGNED" -eq 1 ]'

# gone quiet: STALLED, or K turns with no merge → park (no prompt), even with open work
STALLED=1; NOMERGE=0; BASE_RED=0; BR_ASSIGNED=0; oc=3; FIXED_SET=0
pick_turn 0; check "STALLED → park"            '[ -z "$PICK" ]'
STALLED=0; NOMERGE=8; BASE_RED=0; BR_ASSIGNED=0; oc=3; FIXED_SET=0
pick_turn 0; check "NOMERGE≥STALL_K → park"    '[ -z "$PICK" ]'

# red base: feed exactly ONE fixer per cycle
STALLED=0; NOMERGE=0; BASE_RED=1; BR_ASSIGNED=0; oc=3; FIXED_SET=0
pick_turn 0; check "red base, first worker → /continue" '[ "$PICK" = "/continue" ]'
STALLED=0; NOMERGE=0; BASE_RED=1; BR_ASSIGNED=1; oc=3; FIXED_SET=0
pick_turn 0; check "red base, already fed one → park"   '[ -z "$PICK" ]'

# empty queue + forever mode: plan to replenish, and stamp the cooldown
STALLED=0; NOMERGE=0; BASE_RED=0; BR_ASSIGNED=0; oc=0; FIXED_SET=0; PLANNED_LAST=0; now=1000
pick_turn 0
check "empty queue → planning turn"            '[ "$PICK" = "$PLAN_RULES" ]'
check "planning stamps PLANNED_LAST=now"       '[ "$PLANNED_LAST" -eq 1000 ]'
# planned within REPLAN seconds → park (one plan per window)
STALLED=0; NOMERGE=0; BASE_RED=0; BR_ASSIGNED=0; oc=0; FIXED_SET=0; PLANNED_LAST=900; now=1000
pick_turn 0; check "replan cooldown → park"    '[ -z "$PICK" ]'

# fixed-set (--only) run, empty queue → NEVER plan; just park (wind-down handled in main loop)
STALLED=0; NOMERGE=0; BASE_RED=0; BR_ASSIGNED=0; oc=0; FIXED_SET=1; PLANNED_LAST=0; now=1000
pick_turn 0; check "fixed-set + empty → park (never plans)" '[ -z "$PICK" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
