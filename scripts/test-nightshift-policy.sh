#!/usr/bin/env bash
# devbrain — nightshift shared assignment policy. After the orchestrator simplification, BOTH
# backends (headless + tmux) decide a worker's next turn through ONE function, PickTurn, which
# returns the prompt to launch ("" = park) and the updated shared throttles (BR_ASSIGNED,
# PLANNED_LAST). These tests pin that decision tree so the two backends can't drift apart again.
# Drives the Go port's `pick-turn --state JSON` plumbing verb with explicit inputs — the state
# the bash globals used to carry — and reads the decision JSON it prints
# ("work" = the /work prompt, "plan" = the planning rules).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ROOT="$HERE/.."
BIN="${DEVBRAIN_BIN:-$ROOT/devbrain}"
[ -x "$BIN" ] || { echo "skip: devbrain binary not built (go build -o devbrain ./cmd/devbrain)"; exit 0; }
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

BIND="$TMP/bin"; mkdir -p "$BIND"; printf '#!/usr/bin/env bash\nexit 0\n' > "$BIND/claude"; chmod +x "$BIND/claude"
export PATH="$BIND:$PATH"
export DEVBRAIN_DATA="$TMP/data"
BASE="$TMP/repo"; mkdir -p "$BASE"; git -C "$BASE" init -q
git -C "$BASE" remote add origin git@github.com:test/repo.git

ns(){ "$BIN" nightshift internal "$@" --repo "$BASE"; }

pass=0; fail=0; OUT=""
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 (OUT='${OUT:0:64}')"; fi; }
# Inputs pick_turn reads — the bash globals, passed explicitly as the state JSON.
# now=1000; REPLAN=300; STALL_K=8 throughout.
pt(){  # $1..$6 = stalled nomerge base_red br_assigned open fixed_set ; $7 planned_last (default 0)
  OUT="$(ns pick-turn --state "{\"stalled\":$1,\"nomerge\":$2,\"stall_k\":8,\"base_red\":$3,\"br_assigned\":$4,\"open\":$5,\"fixed_set\":$6,\"now\":1000,\"planned_last\":${7:-0},\"replan\":300}")"
}
pick(){ printf '%s' "$OUT" | sed -n 's/.*"pick":"\([^"]*\)".*/\1/p'; }
num(){  printf '%s' "$OUT" | sed -n "s/.*\"$1\":\([0-9]*\).*/\1/p"; }

# open work → assign /work, and bump the per-open-task assignment counter
pt false 0 false 0 3 false
check "open work → /work"                  '[ "$(pick)" = "work" ]'
check "/work bumps BR_ASSIGNED"            '[ "$(num br_assigned)" -eq 1 ]'

# one worker per open task: once this poll's assignments reach oc, the rest park (the fan-out cap)
pt false 0 false 3 3 false
check "assignments capped at open count → park" '[ -z "$(pick)" ]'

# gone quiet: STALLED, or K turns with no merge → park (no prompt), even with open work
pt true 0 false 0 3 false
check "STALLED → park"            '[ -z "$(pick)" ]'
pt false 8 false 0 3 false
check "NOMERGE≥STALL_K → park"    '[ -z "$(pick)" ]'

# red base: feed exactly ONE fixer per cycle
pt false 0 true 0 3 false
check "red base, first worker → /work" '[ "$(pick)" = "work" ]'
pt false 0 true 1 3 false
check "red base, already fed one → park"   '[ -z "$(pick)" ]'

# empty queue + forever mode: plan to replenish, and stamp the cooldown
pt false 0 false 0 0 false 0
check "empty queue → planning turn"            '[ "$(pick)" = "plan" ]'
check "planning stamps PLANNED_LAST=now"       '[ "$(num planned_last)" -eq 1000 ]'
# planned within REPLAN seconds → park (one plan per window)
pt false 0 false 0 0 false 900
check "replan cooldown → park"    '[ -z "$(pick)" ]'

# fixed-set (--only) run, empty queue → NEVER plan; just park (wind-down handled in main loop)
pt false 0 false 0 0 true 0
check "fixed-set + empty → park (never plans)" '[ -z "$(pick)" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
