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
_hd="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
for _c in "$_hd/hook-common.sh" "$HOME/.claude/hooks/devbrain-hook-common.sh"; do
  [ -f "$_c" ] && { . "$_c"; break; }
done
command -v devbrain_read_event >/dev/null 2>&1 || exit 0
devbrain_has_python_lib || exit 0

prompt="$(devbrain_read_event prompt)"
cwd="$(devbrain_read_event cwd)"
session="$(devbrain_read_event session)"

[ -n "$prompt" ] || exit 0          # nothing to capture
[ -n "$cwd" ] || cwd="$PWD"

# Skip injected/synthetic prompts AND redact secrets in one step, via the single
# rule definition in devbrain_lib.py (so capture.sh, capture-response.sh,
# capture-memory.sh and import.py never drift). prompt-filter prints the redacted
# prompt, or NOTHING if the prompt is synthetic -> skip.
filtered="$(printf '%s' "$prompt" | python3 "$DEVBRAIN_LIB" prompt-filter 2>/dev/null)"
[ -n "$filtered" ] || exit 0     # empty = synthetic prompt, or python hiccup -> skip
prompt="$filtered"

# Identity from the working repo. Worktrees of one repo collapse to one project
# (same remote). Delegated to the shared OFFLINE resolver (project-key.sh) so capture,
# todo.sh, and the skills agree on the projects/<owner>__<repo> folder. Installed
# alongside as devbrain-project-key.sh; repo copy is hooks/project-key.sh.
devbrain_source_project_key || exit 0

project="$(devbrain_project_key "$cwd" "$DATA")"; [ -n "$project" ] || project="unknown"

worktree="$(devbrain_worktree_slug "$cwd")"
session="$(devbrain_sanitize "$session")"; [ -n "$session" ] || session="nosession"

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
    printf '> agent: %s · worktree: %s · cwd: %s · times in UTC\n' "$harness" "$worktree" "$cwd"
    # Cost caveat: the inline `tokens:` meta lines are per-turn best-effort. The authoritative,
    # deduped cost source is projects/<proj>/tokens.jsonl — pre-2026-06-25 inline counts run
    # ~2.85× high (per-content-block double-count, since fixed). Do not sum the inline lines.
    printf '> cost: `tokens:` lines are per-turn best-effort; authoritative deduped source is projects/<proj>/tokens.jsonl (pre-2026-06-25 inline counts run ~2.85x high — do not sum).\n\n'
  } >> "$file" 2>/dev/null
fi

# Append the entry verbatim.
{
  printf '## %s\n\n' "$ts"
  printf '%s\n\n' "$prompt"
} >> "$file" 2>/dev/null

exit 0
