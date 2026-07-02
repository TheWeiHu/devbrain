#!/usr/bin/env bash
# devbrain — nightshift fixed-set FENCE tests. The `--only` scoping must fail CLOSED: even if
# the installed todo ignores DEVBRAIN_TODO_ONLY, the orchestrator parks every out-of-set
# OPEN task at boot so `next` can only hand out the chosen subset — and restores them on exit.
# Drives the Go port's plumbing verbs (`devbrain nightshift internal …`); uses the repo todo.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ROOT="$HERE/.."
BIN="${DEVBRAIN_BIN:-$ROOT/devbrain}"
[ -x "$BIN" ] || { echo "skip: devbrain binary not built (go build -o devbrain ./cmd/devbrain)"; exit 0; }
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

BIND="$TMP/bin"; mkdir -p "$BIND"; printf '#!/usr/bin/env bash\nexit 0\n' > "$BIND/claude"; chmod +x "$BIND/claude"
export PATH="$BIND:$PATH"
export DEVBRAIN_DATA="$TMP/data"

BASE="$TMP/repo"; mkdir -p "$BASE"; git -C "$BASE" init -q
# Local origin + pinned key: derive_init runs `git fetch origin`; a github-style URL makes that a
# multi-second SSH hang. A local bare origin keeps it instant, and DEVBRAIN_PROJECT holds the key.
REM="$TMP/rem.git"; git init -q --bare "$REM"; git -C "$BASE" remote add origin "$REM"
export DEVBRAIN_PROJECT=test__repo
TD="$DEVBRAIN_DATA/projects/test__repo/todo"; mkdir -p "$TD"
mk(){ printf -- '---\nid: %s\nstatus: open\npriority: %s\ncreated: 2026-06-2%s\nclaimed_by:\nclaimed_at:\npr:\n---\n# %s\n' \
        "$1" "$2" "${3}T00:00:00Z" "$4" > "$TD/$1.md"; }
mk 0001-alpha 90 1 "Build the alpha thing"; mk 0002-beta 80 2 "Wire beta"
mk 0003-gamma 70 3 "Gamma docs";           mk 0004-delta 60 4 "Delta fix"

# The fixed-set fence under test (parse-only validates it the way boot does).
ONLY="0002-beta,0003-gamma"
ns(){ "$BIN" nightshift internal "$@" --repo "$BASE"; }
TODO="$HERE/todo.sh"   # use the repo todo (deterministic; the fence doesn't depend on the install)

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }
# Run queries with the env filter OFF — this simulates a stale/unaware installed todo that
# ignores DEVBRAIN_TODO_ONLY, proving the FENCE (held tasks) is what scopes the queue.
tq(){ ( cd "$BASE" && DEVBRAIN_TODO_ONLY= "$TODO" "$@" 2>/dev/null ); }
visible(){ tq list | sed -nE 's/^[[:space:]]*\[[^]]*\][[:space:]]+([0-9]{4}-[a-z]+).*/\1/p' | tr '\n' ' '; }

check "in_only matches full slug"   'ns in-only 0002-beta --only "$ONLY"'
check "in_only matches bare number" 'ns in-only 0003 --only "$ONLY"'
check "in_only rejects out-of-set"  '! ns in-only 0001-alpha --only "$ONLY"'

# parse-only arms the run the way boot does: caps workers to the task count + fixed-set mode
POUT="$(ns parse-only --only "$ONLY" --workers 3 2>&1)"
check "worker count capped to task count" 'printf "%s" "$POUT" | grep -q "capping workers 3 → 2"'
check "fixed-set mode armed"               'printf "%s" "$POUT" | grep -q "fixed-set mode"'

check "before fence: all 4 open"    '[ "$(visible | wc -w)" -eq 4 ]'
ns fence --only "$ONLY" >/dev/null 2>&1
check "after fence: only the subset is visible" '[ "$(visible)" = "0002-beta 0003-gamma " ]'
check "next returns a subset task"  '[ "$(tq next)" = "0002-beta" ]'
check "parked tasks are held, not open" '[ "$(tq show 0001-alpha | sed -n "s/^status: //p")" = "held" ]'
check "park note carries the recovery marker" 'tq show 0001-alpha | grep -q "^reason: fixed-set: parked"'

ns unfence >/dev/null 2>&1
check "after unfence: all 4 open again"   '[ "$(visible | wc -w)" -eq 4 ]'
check "unfence clears the stale note"     '[ -z "$(tq show 0001-alpha | sed -n "s/^reason: //p" | head -1)" ]'
check "unfence is idempotent (no error)"  'ns unfence'
# RECOVERY: a hold left by a crashed run (no file, just the marker on the task) is still released.
( cd "$BASE" && "$TODO" hold 0004-delta "fixed-set: parked while nightshift runs your selected tasks" >/dev/null 2>&1 )
check "orphaned fence hold present"        '[ "$(tq show 0004-delta | sed -n "s/^status: //p")" = "held" ]'
ns unfence >/dev/null 2>&1
check "marker-based unfence recovers it"   '[ "$(tq show 0004-delta | sed -n "s/^status: //p")" = "open" ]'
# a NON-fence human hold must NOT be touched by recovery
( cd "$BASE" && "$TODO" hold 0001-alpha "blocked: needs a human decision" >/dev/null 2>&1 )
ns unfence >/dev/null 2>&1
check "human hold survives recovery"       '[ "$(tq show 0001-alpha | sed -n "s/^status: //p")" = "held" ]'
( cd "$BASE" && "$TODO" release 0001-alpha >/dev/null 2>&1 )

# A task carrying done_at (a DONE task that derive-git read as "open") must NOT be fence-parked:
# parking then unfencing it via `release` wipes its done_at and corrupts the queue. done_at is the
# raw done signal, so the fence skips any task that carries it, even one listed as open.
printf -- '---\nid: 0008-donez\nstatus: open\npriority: 45\ncreated: 2026-06-25T00:00:00Z\nclaimed_by:\nclaimed_at:\npr:\ndone_at: 2026-06-25T17:00:00Z\n---\n# carries done_at but reads open\n' > "$TD/0008-donez.md"
check "before fence: task with done_at is visible (open)" 'visible | grep 0008-donez >/dev/null'
ns fence --only "$ONLY" >/dev/null 2>&1
check "fence does NOT park a task carrying done_at" '[ "$(tq show 0008-donez | sed -n "s/^status: //p")" != held ]'
check "its done_at survives the fence"              '[ -n "$(tq show 0008-donez | sed -n "s/^done_at: //p")" ]'
ns unfence >/dev/null 2>&1
rm -f "$TD/0008-donez.md"

# wind-down: stop only when EVERY selected task is terminal (done|held). A selected `review`
# task (worker opened a PR / pushed its branch) must keep the fleet alive so the orchestrator
# harvests + merges it — quitting early was the turns=0 / unmerged-branch bug.
st(){ printf -- '---\nid: %s\nstatus: %s\npriority: 50\ncreated: 2026-06-25T00:00:00Z\nclaimed_by:\nclaimed_at:\npr:\n---\n# %s\n' "$1" "$2" "$3" > "$TD/$1.md"; }
st 0005-rev review "in review"; st 0006-don done "merged"; st 0007-hel held "blocked"
check "wind-down waits on a selected review task" '[ "$(ns unresolved --only 0005-rev,0006-don,0007-hel)" -eq 1 ]'
check "wind-down fires when all selected are done/held" '[ "$(ns unresolved --only 0006-don,0007-hel)" -eq 0 ]'
check "wind-down waits on a selected open task" '[ "$(ns unresolved --only 0002-beta)" -eq 1 ]'

# ── reconcile is fenced: a fixed-set run must not adopt out-of-set residue from prior runs ──
# The fence parks only OPEN tasks, so an out-of-set taken/review leftover with a pushed
# todo/ branch would otherwise get merged into the contained run (the stale-branch thrash).
st 0001-alpha taken "leftover from a prior run"; st 0002-beta taken "selected, pushed"
( cd "$BASE" && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m base \
  && git branch -f todo/0001-alpha >/dev/null && git branch -f todo/0002-beta >/dev/null \
  && git push -q origin todo/0001-alpha todo/0002-beta )
# NIGHTSHIFT_TEST_MERGE_LOG records which branches reconcile WOULD merge (the Go seam
# standing in for the bash test's merge_to_nightshift stub; no nightshift branch exists,
# so task_in_nightshift is naturally false here).
MLOG="$TMP/merged"; : > "$MLOG"
NIGHTSHIFT_TEST_MERGE_LOG="$MLOG" ns reconcile --only "$ONLY" --fixed-set >/dev/null 2>&1
check "reconcile merges a selected pushed branch"    'grep -q 0002-beta "$MLOG"'
check "reconcile skips an out-of-set pushed branch"  '! grep -q 0001-alpha "$MLOG"'
: > "$MLOG"
NIGHTSHIFT_TEST_MERGE_LOG="$MLOG" ns reconcile >/dev/null 2>&1
check "unbounded reconcile still adopts the leftover" 'grep -q 0001-alpha "$MLOG"'

# ── reclaim is fenced: an out-of-set stale claim stays `taken` (fail closed — releasing it
# to `open` would expose it to a stale installed todo that ignores DEVBRAIN_TODO_ONLY).
st 0001-alpha taken "out-of-set stale claim"; st 0002-beta taken "selected stale claim"
ns reclaim --only "$ONLY" --fixed-set >/dev/null 2>&1
check "reclaim releases a selected stale claim"      '[ "$(tq show 0002-beta | sed -n "s/^status: //p")" = open ]'
check "reclaim leaves an out-of-set claim taken"     '[ "$(tq show 0001-alpha | sed -n "s/^status: //p")" = taken ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
