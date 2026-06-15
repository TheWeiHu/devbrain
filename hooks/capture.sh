#!/usr/bin/env bash
# devbrain — Stage A capture (UserPromptSubmit hook).
#
# Appends every prompt verbatim to the private data repo. Model-free, never
# blocks, never fails the session. Reads identity FROM the working repo (cwd)
# and writes TO the fixed data home — the two git repos never entangle.
#
# Layout (one file per session per day):
#   $DEVBRAIN_DATA/projects/<project>/log/<YYYY-MM-DD>/<worktree>.<session-id>.md
#
# MUST always exit 0: a capture failure must never break the user's turn.

DATA="${DEVBRAIN_DATA:-$HOME/Desktop/devbrain-data}"

# Hook payload is JSON on stdin.
payload="$(cat 2>/dev/null)" || exit 0

# jq is required to parse; if missing, fail open (don't block the session).
command -v jq >/dev/null 2>&1 || exit 0

prompt="$(printf '%s' "$payload"  | jq -r '.prompt // empty' 2>/dev/null)"
cwd="$(printf '%s' "$payload"     | jq -r '.cwd // empty' 2>/dev/null)"
session="$(printf '%s' "$payload" | jq -r '.session_id // "nosession"' 2>/dev/null)"

[ -n "$prompt" ] || exit 0          # nothing to capture
[ -n "$cwd" ] || cwd="$PWD"

# Identity from the working repo. Worktrees of one repo collapse to one project
# (same remote). Degrade gracefully when cwd isn't a git repo.
remote="$(git -C "$cwd" remote get-url origin 2>/dev/null)"
if [ -n "$remote" ]; then
  project="$(basename "${remote%.git}")"
else
  project="$(basename "$cwd")"
fi
toplevel="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)"
worktree="$(basename "${toplevel:-$cwd}")"

# Filesystem-safe slugs.
sanitize() { printf '%s' "$1" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-'; }
project="$(sanitize "$project")";   [ -n "$project" ]  || project="unknown"
worktree="$(sanitize "$worktree")"; [ -n "$worktree" ] || worktree="unknown"
session="$(sanitize "$session")";   [ -n "$session" ]  || session="nosession"

day="$(date +%F)"
ts="$(date +%H:%M:%S)"
dir="$DATA/projects/$project/log/$day"
file="$dir/$worktree.$session.md"

mkdir -p "$dir" 2>/dev/null || exit 0

# Header on first write of this session-day.
if [ ! -e "$file" ]; then
  {
    printf '# %s — %s — session %s\n\n' "$project" "$day" "$session"
    printf '> devbrain Stage A raw prompt log. Append-only, source of truth.\n'
    printf '> worktree: %s · cwd: %s\n\n' "$worktree" "$cwd"
  } >> "$file" 2>/dev/null
fi

# Append the entry verbatim.
{
  printf '## %s\n\n' "$ts"
  printf '%s\n\n' "$prompt"
} >> "$file" 2>/dev/null

exit 0
