#!/usr/bin/env bash
# devbrain — nightshift RECONCILE tests for the branchless `review` orphan heal. A task whose work
# already landed in nightshift but whose status never advanced to `done`, AND whose todo/<id> branch
# is gone (merged + pruned, or a worker direct-merge whose `done` never stuck), used to sit `review`
# forever — pinning a fixed-set wind-down and undercounting the dashboard. reconcile() now detects
# the merge in nightshift's history (which survives the branch deletion) and closes the task.
# Sources the orchestrator's functions (NIGHTSHIFT_LIB mode, no fleet); uses a local origin so the
# fetch/ls-remote inside reconcile are instant and offline.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ORCH="$HERE/nightshift-orchestrate.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

BIN="$TMP/bin"; mkdir -p "$BIN"; printf '#!/usr/bin/env bash\nexit 0\n' > "$BIN/claude"; chmod +x "$BIN/claude"
export PATH="$BIN:$PATH"
export DEVBRAIN_DATA="$TMP/data"
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t

ORIGIN="$TMP/origin.git"; git init -q --bare "$ORIGIN"
BASE="$TMP/repo"; git init -q "$BASE"; git -C "$BASE" remote add origin "$ORIGIN"
git -C "$BASE" commit --allow-empty -qm init
# A nightshift branch carrying a merge commit that names the merged task's branch — this subject
# (`nightshift: merge todo/<id> into nightshift`) is what reconcile greps for, and it survives the
# branch being deleted from origin. The task's own todo/* branch is intentionally NEVER pushed, so
# the reconcile remote-branch loop can't see it: it's a pure branchless orphan.
git -C "$BASE" checkout -q -b nightshift
git -C "$BASE" commit --allow-empty -qm "nightshift: merge todo/0010-merged into nightshift"
git -C "$BASE" push -q origin nightshift
git -C "$BASE" fetch -q origin

TODO="$HERE/todo.sh"   # deterministic repo todo (matches the orchestrator's resolution)
proj="$( cd "$BASE" && "$TODO" list 2>/dev/null | sed -n 's/^queue: //p' | awk '{print $1}' )"
TD="$DEVBRAIN_DATA/projects/$proj/todo"; mkdir -p "$TD"
st(){ printf -- '---\nid: %s\nstatus: %s\npriority: 50\ncreated: 2026-06-25T00:00:00Z\nclaimed_by:\nclaimed_at:\npr:\n---\n# %s\n' "$1" "$2" "$3" > "$TD/$1.md"; }
st 0010-merged  review "work landed in nightshift but status stuck at review"
st 0011-pending review "PR still open — branch genuinely awaiting merge"
st 0012-landed  open   "remote todo branch already landed in nightshift"
git -C "$BASE" branch -qf todo/0012-landed origin/nightshift
git -C "$BASE" push -q origin todo/0012-landed

# Source in lib mode (no fleet) to get reconcile()/fixedset_unresolved().
NIGHTSHIFT_LIB=1 . "$ORCH" --repo "$BASE" >/dev/null 2>&1
TODO="$HERE/todo.sh"

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }
status(){ ( cd "$BASE" && "$TODO" show "$1" 2>/dev/null ) | sed -n 's/^status:[[:space:]]*//p' | head -1; }

check "before reconcile: orphan is review"           '[ "$(status 0010-merged)" = review ]'
check "before reconcile: landed live branch is open" '[ "$(status 0012-landed)" = open ]'
reconcile_task 0012-landed >/dev/null 2>&1
check "reconcile_task closes a live branch already in nightshift" '[ "$(status 0012-landed)" = done ]'
check "reconcile_task prunes the spent remote branch" '! git -C "$BASE" ls-remote --exit-code --heads origin todo/0012-landed >/dev/null 2>&1'
reconcile >/dev/null 2>&1
check "reconcile closes the landed branchless orphan" '[ "$(status 0010-merged)" = done ]'
check "reconcile leaves a genuinely-pending review"   '[ "$(status 0011-pending)" = review ]'
# The whole point: with the orphan closed, a fixed-set wind-down no longer waits on it forever.
ONLY="0010-merged,0011-pending"
check "wind-down now counts only the pending review"  '[ "$(fixedset_unresolved)" -eq 1 ]'
# Idempotent: re-running heals nothing new and errors on nothing.
reconcile >/dev/null 2>&1
check "reconcile is idempotent"                       '[ "$(status 0010-merged)" = done ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
