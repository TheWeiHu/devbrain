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
harness="${DEVBRAIN_HARNESS:-claude}"

# Hook payload is JSON on stdin.
payload="$(cat 2>/dev/null)" || exit 0

# Both field extraction (the per-harness event shim, keyed by $DEVBRAIN_HARNESS) and
# redaction live in devbrain_lib.py, so python3 is the only parse dep; fail open if it's
# missing so a capture failure never breaks the user's turn.
command -v python3 >/dev/null 2>&1 || exit 0
_lib="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)/devbrain_lib.py"
[ -f "$_lib" ] || _lib="$HOME/.claude/hooks/devbrain_lib.py"
ev() { printf '%s' "$payload" | python3 "$_lib" read-event "$1" 2>/dev/null; }

prompt="$(ev prompt)"
cwd="$(ev cwd)"
session="$(ev session)"

[ -n "$prompt" ] || exit 0          # nothing to capture
[ -n "$cwd" ] || cwd="$PWD"

# Skip injected/synthetic prompts AND redact secrets in one step, via the single
# rule definition in devbrain_lib.py (so capture.sh, capture-response.sh,
# capture-memory.sh and import.py never drift). prompt-filter prints the redacted
# prompt, or NOTHING if the prompt is synthetic -> skip.
filtered="$(printf '%s' "$prompt" | python3 "$_lib" prompt-filter 2>/dev/null)"
[ -n "$filtered" ] || exit 0     # empty = synthetic prompt, or python hiccup -> skip
prompt="$filtered"

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
    printf '> agent: %s · worktree: %s · cwd: %s · times in UTC\n\n' "$harness" "$worktree" "$cwd"
  } >> "$file" 2>/dev/null
fi

# Append the entry verbatim.
{
  printf '## %s\n\n' "$ts"
  printf '%s\n\n' "$prompt"
} >> "$file" 2>/dev/null

exit 0
