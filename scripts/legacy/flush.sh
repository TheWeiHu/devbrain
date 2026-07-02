#!/usr/bin/env bash
# devbrain — flusher.
#
# Durably pushes the brain off-machine. Per-machine, serialized; operates on the
# data repo EXPLICITLY (git -C) so it never inherits a working repo's cwd. Safe to
# run on any interval; a no-op when there's nothing to commit. Fails open.
#
# Durability ladder: capture appends locally (instant) -> this flusher
# commits/pushes (off-machine). Per-session sharding means pulls only ADD files,
# so a rebase pull never hits a content conflict.
#
# Runs two ways: on a timer (LaunchAgent, every 5 min) and on demand at the end of
# /distill, so a checkpoint is durable immediately rather than ≤5 min later. The
# push is conditional by nature — `git push` is a no-op when no remote is set.
# Optional arg $1 = commit-message reason (default "capture"; /distill passes "distill").
set -uo pipefail

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
REASON="${1:-capture}"
[ -d "$DATA/.git" ] || { echo "no data repo at $DATA"; exit 0; }

cd "$DATA" || exit 0

# Pull first so the local commit lands on top of any other machine's pushes.
git pull --rebase --autostash --quiet 2>/dev/null || true

# Nothing to do?
[ -n "$(git status --porcelain)" ] || exit 0

git add -A
git diff --cached --quiet && exit 0   # nothing staged after add

# Commit identity: env override → repo's git config → generic default. The default
# is intentionally impersonal so a fresh install never commits under someone else's
# name; set DEVBRAIN_GIT_NAME / DEVBRAIN_GIT_EMAIL (or the data repo's git config)
# to use your own.
name="${DEVBRAIN_GIT_NAME:-$(git config user.name 2>/dev/null || true)}";   [ -n "$name" ]  || name="devbrain"
email="${DEVBRAIN_GIT_EMAIL:-$(git config user.email 2>/dev/null || true)}"; [ -n "$email" ] || email="devbrain@localhost"
host="$(hostname -s 2>/dev/null || echo host)"
msg="$REASON: $(date '+%Y-%m-%d %H:%M:%S %z') on $host"

git -c user.name="$name" -c user.email="$email" commit --quiet -m "$msg" || exit 0
git push --quiet 2>/dev/null || true
