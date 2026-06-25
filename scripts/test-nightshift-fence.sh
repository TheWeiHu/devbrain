#!/usr/bin/env bash
# devbrain — nightshift fixed-set FENCE tests. The `--only` scoping must fail CLOSED: even if
# the installed todo.sh ignores DEVBRAIN_TODO_ONLY, the orchestrator parks every out-of-set
# OPEN task at boot so `next` can only hand out the chosen subset — and restores them on exit.
# Sources the orchestrator's functions (NIGHTSHIFT_LIB mode, no fleet); uses the repo todo.sh.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ORCH="$HERE/nightshift-orchestrate.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

BIN="$TMP/bin"; mkdir -p "$BIN"; printf '#!/usr/bin/env bash\nexit 0\n' > "$BIN/claude"; chmod +x "$BIN/claude"
export PATH="$BIN:$PATH"
export DEVBRAIN_DATA="$TMP/data"

BASE="$TMP/repo"; mkdir -p "$BASE"; git -C "$BASE" init -q
git -C "$BASE" remote add origin git@github.com:test/repo.git
TD="$DEVBRAIN_DATA/projects/test__repo/todo"; mkdir -p "$TD"
mk(){ printf -- '---\nid: %s\nstatus: open\npriority: %s\ncreated: 2026-06-2%s\nclaimed_by:\nclaimed_at:\npr:\n---\n# %s\n' \
        "$1" "$2" "${3}T00:00:00Z" "$4" > "$TD/$1.md"; }
mk 0001-alpha 90 1 "Build the alpha thing"; mk 0002-beta 80 2 "Wire beta"
mk 0003-gamma 70 3 "Gamma docs";           mk 0004-delta 60 4 "Delta fix"

# Source in lib mode with --only; the guard returns before the fleet boots.
NIGHTSHIFT_LIB=1 . "$ORCH" --repo "$BASE" --only 0002-beta,0003-gamma >/dev/null 2>&1
TODO="$HERE/todo.sh"   # use the repo todo (deterministic; the fence doesn't depend on the install)

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }
# Run queries with the env filter OFF — this simulates a stale/unaware installed todo.sh that
# ignores DEVBRAIN_TODO_ONLY, proving the FENCE (held tasks) is what scopes the queue.
tq(){ ( cd "$BASE" && DEVBRAIN_TODO_ONLY= "$TODO" "$@" 2>/dev/null ); }
visible(){ tq list | sed -nE 's/^[[:space:]]*\[[^]]*\][[:space:]]+([0-9]{4}-[a-z]+).*/\1/p' | tr '\n' ' '; }

check "in_only matches full slug"   'in_only 0002-beta'
check "in_only matches bare number" 'in_only 0003'
check "in_only rejects out-of-set"  '! in_only 0001-alpha'

check "worker count capped to task count" '[ "$N" -eq 2 ]'
check "fixed-set mode armed"               '[ "$FIXED_SET" = 1 ]'

check "before fence: all 4 open"    '[ "$(visible | wc -w)" -eq 4 ]'
fixedset_fence >/dev/null 2>&1
check "after fence: only the subset is visible" '[ "$(visible)" = "0002-beta 0003-gamma " ]'
check "next returns a subset task"  '[ "$(tq next)" = "0002-beta" ]'
check "parked tasks are held, not open" '[ "$(tq show 0001-alpha | sed -n "s/^status: //p")" = "held" ]'
check "park note carries the recovery marker" 'tq show 0001-alpha | grep -q "^reason: fixed-set: parked"'

fixedset_unfence >/dev/null 2>&1
check "after unfence: all 4 open again"   '[ "$(visible | wc -w)" -eq 4 ]'
check "unfence clears the stale note"     '[ -z "$(tq show 0001-alpha | sed -n "s/^reason: //p" | head -1)" ]'
check "unfence is idempotent (no error)"  'fixedset_unfence'
# RECOVERY: a hold left by a crashed run (no file, just the marker on the task) is still released.
( cd "$BASE" && "$TODO" hold 0004-delta "fixed-set: parked while nightshift runs your selected tasks" >/dev/null 2>&1 )
check "orphaned fence hold present"        '[ "$(tq show 0004-delta | sed -n "s/^status: //p")" = "held" ]'
fixedset_unfence >/dev/null 2>&1
check "marker-based unfence recovers it"   '[ "$(tq show 0004-delta | sed -n "s/^status: //p")" = "open" ]'
# a NON-fence human hold must NOT be touched by recovery
( cd "$BASE" && "$TODO" hold 0001-alpha "blocked: needs a human decision" >/dev/null 2>&1 )
fixedset_unfence >/dev/null 2>&1
check "human hold survives recovery"       '[ "$(tq show 0001-alpha | sed -n "s/^status: //p")" = "held" ]'
( cd "$BASE" && "$TODO" release 0001-alpha >/dev/null 2>&1 )

# wind-down: stop only when EVERY selected task is terminal (done|held). A selected `review`
# task (worker opened a PR / pushed its branch) must keep the fleet alive so the orchestrator
# harvests + merges it — quitting early was the turns=0 / unmerged-branch bug.
st(){ printf -- '---\nid: %s\nstatus: %s\npriority: 50\ncreated: 2026-06-25T00:00:00Z\nclaimed_by:\nclaimed_at:\npr:\n---\n# %s\n' "$1" "$2" "$3" > "$TD/$1.md"; }
st 0005-rev review "in review"; st 0006-don done "merged"; st 0007-hel held "blocked"
ONLY="0005-rev,0006-don,0007-hel"
check "wind-down waits on a selected review task" '[ "$(fixedset_unresolved)" -eq 1 ]'
ONLY="0006-don,0007-hel"
check "wind-down fires when all selected are done/held" '[ "$(fixedset_unresolved)" -eq 0 ]'
ONLY="0002-beta"
check "wind-down waits on a selected open task" '[ "$(fixedset_unresolved)" -eq 1 ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
