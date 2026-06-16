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

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

# Hook payload is JSON on stdin.
payload="$(cat 2>/dev/null)" || exit 0

# jq is required to parse; if missing, fail open (don't block the session).
command -v jq >/dev/null 2>&1 || exit 0

prompt="$(printf '%s' "$payload"  | jq -r '.prompt // empty' 2>/dev/null)"
cwd="$(printf '%s' "$payload"     | jq -r '.cwd // empty' 2>/dev/null)"
session="$(printf '%s' "$payload" | jq -r '.session_id // "nosession"' 2>/dev/null)"

[ -n "$prompt" ] || exit 0          # nothing to capture
[ -n "$cwd" ] || cwd="$PWD"

# Redact common secret shapes before anything is written. Prompts can carry API
# keys, and the log is auto-pushed (private repo, but defense-in-depth). High-
# confidence prefixes only — we'd rather miss an exotic token than mangle prose,
# so every pattern is anchored on a distinctive prefix and a length floor. Uses
# portable ERE (no \b / PCRE) so it works under both BSD (macOS) and GNU sed.
redact() {
  sed -E \
    -e 's/sk-[A-Za-z0-9_-]{20,}/[REDACTED]/g' \
    -e 's/(gh[pousr]_)[A-Za-z0-9]{20,}/[REDACTED]/g' \
    -e 's/github_pat_[A-Za-z0-9_]{20,}/[REDACTED]/g' \
    -e 's/(AKIA|ASIA)[0-9A-Z]{16}/[REDACTED]/g' \
    -e 's/xox[baprs]-[A-Za-z0-9-]{10,}/[REDACTED]/g' \
    -e 's/(Bearer )[A-Za-z0-9._-]{16,}/\1[REDACTED]/g'
}
redacted="$(printf '%s' "$prompt" | redact 2>/dev/null)"
[ -n "$redacted" ] && prompt="$redacted"   # fail open: keep original if sed hiccups

# Identity from the working repo. Worktrees of one repo collapse to one project
# (same remote). Delegated to the shared OFFLINE resolver (project-key.sh) so capture,
# todo.sh, and the skills agree on the projects/<owner>__<repo> folder. Installed
# alongside as devbrain-project-key.sh; repo copy is hooks/project-key.sh.
_pk="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
for _c in "$_pk/devbrain-project-key.sh" "$_pk/project-key.sh" "$HOME/.claude/hooks/devbrain-project-key.sh"; do
  [ -f "$_c" ] && { . "$_c"; break; }
done

# Filesystem-safe slugs.
sanitize() { printf '%s' "$1" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-'; }

project="$(devbrain_project_key "$cwd" "$DATA")"; [ -n "$project" ] || project="unknown"

toplevel="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)"
worktree="$(basename "${toplevel:-$cwd}")"
worktree="$(sanitize "$worktree")"; [ -n "$worktree" ] || worktree="unknown"
session="$(sanitize "$session")";   [ -n "$session" ]  || session="nosession"

# UTC always — so timestamps (and the /distill ledger that mirrors them) stay
# unambiguous and correctly ordered even if the machine's timezone changes or
# logs sync between machines in different zones.
day="$(date -u +%F)"
ts="$(date -u +%H:%M:%S)"
dir="$DATA/projects/$project/log/$day"
file="$dir/$worktree.$session.md"

mkdir -p "$dir" 2>/dev/null || exit 0

# Header on first write of this session-day.
if [ ! -e "$file" ]; then
  {
    printf '# %s — %s — session %s\n\n' "$project" "$day" "$session"
    printf '> devbrain Stage A raw prompt log. Append-only, source of truth.\n'
    printf '> worktree: %s · cwd: %s · times in UTC\n\n' "$worktree" "$cwd"
  } >> "$file" 2>/dev/null
fi

# Append the entry verbatim.
{
  printf '## %s\n\n' "$ts"
  printf '%s\n\n' "$prompt"
} >> "$file" 2>/dev/null

exit 0
