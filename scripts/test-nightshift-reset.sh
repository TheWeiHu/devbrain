#!/usr/bin/env bash
# devbrain — nightshift base-reset regression. A REUSED clone keeps a worktree (the stage / a
# worker) checked out on `nightshift`, which makes `git branch -f nightshift origin/main` fail.
# setup_nightshift must DETACH such worktrees first, then reset — not die on the FATAL guard
# (the "launch ran but nothing merged / orchestrator exited immediately" bug). The first half
# replicates the exact git sequence; the second drives the Go port's setup-nightshift verb
# through the same trap.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ROOT="$HERE/.."
BIN="${DEVBRAIN_BIN:-$ROOT/devbrain}"
[ -x "$BIN" ] || { echo "skip: devbrain binary not built (go build -o devbrain ./cmd/devbrain)"; exit 0; }
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# a bare "remote" with main, plus a clone standing in for ~/nightshift/<repo>
REM="$TMP/rem.git"; git init -q --bare "$REM"
SEED="$TMP/seed"; git clone -q "$REM" "$SEED"
( cd "$SEED" && echo x > f && git add . && git -c user.email=a@b.c -c user.name=t commit -qm init && git push -q origin HEAD:main )
BASE="$TMP/clone"; git clone -q "$REM" "$BASE"
git -C "$BASE" branch -f nightshift origin/main 2>/dev/null
# simulate a prior run: a stage worktree checked out on nightshift
git -C "$BASE" worktree add -q "$TMP/clone-stage" nightshift
# move main forward on the remote so a reset is meaningful
( cd "$SEED" && echo y >> f && git -c user.email=a@b.c -c user.name=t commit -aqm next && git push -q origin HEAD:main )
git -C "$BASE" fetch -q origin

check "repro: branch -f FAILS while stage holds nightshift" \
  '! git -C "$BASE" branch -f nightshift origin/main 2>/dev/null'

# the fix: detach any worktree on nightshift, then reset
git -C "$BASE" worktree prune 2>/dev/null
for wt in $(git -C "$BASE" worktree list --porcelain 2>/dev/null | awk '/^worktree /{w=$2} /^branch refs\/heads\/nightshift$/{print w}'); do
  git -C "$wt" checkout -q --detach 2>/dev/null
done
check "after detach: branch -f succeeds" 'git -C "$BASE" branch -f nightshift origin/main 2>/dev/null'
check "nightshift now equals origin/main" \
  '[ "$(git -C "$BASE" rev-parse nightshift)" = "$(git -C "$BASE" rev-parse origin/main)" ]'
check "the stage worktree is now detached (not on nightshift)" \
  '[ -z "$(git -C "$TMP/clone-stage" symbolic-ref -q --short HEAD 2>/dev/null)" ]'

# The Go port's setup-nightshift takes the SAME trap: re-arm the stage on nightshift and move
# main forward again — setup must detach + reset (not FATAL) and land nightshift on origin/main.
git -C "$TMP/clone-stage" checkout -q nightshift
( cd "$SEED" && echo z >> f && git -c user.email=a@b.c -c user.name=t commit -aqm third && git push -q origin HEAD:main )
git -C "$BASE" fetch -q origin
check "go setup-nightshift resets past a stage holding nightshift" \
  '"$BIN" nightshift internal setup-nightshift --repo "$BASE" --no-gate >/dev/null 2>&1'
check "go setup-nightshift: nightshift equals the new origin/main" \
  '[ "$(git -C "$BASE" rev-parse nightshift)" = "$(git -C "$BASE" rev-parse origin/main)" ]'
check "go setup-nightshift pushed the reset branch" \
  '[ "$(git -C "$BASE" ls-remote origin refs/heads/nightshift | cut -f1)" = "$(git -C "$BASE" rev-parse origin/main)" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
