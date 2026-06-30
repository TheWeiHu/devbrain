#!/usr/bin/env bash
# devbrain — Stage A capture, response side (Stop hook).
#
# Fires when the agent finishes a turn. Appends a compact, MODEL-FREE trace of the
# response under the matching prompt in the same session log (the merged-#15 shape):
# the closing sentence of the agent's FINAL message (the recap — the global CLAUDE.md
# instruction tells the agent to end its final message with one), the files touched and
# tools used, and a bounded head/middle SAMPLE of the turn's prose. The recap/sample/
# redaction rules come from devbrain_lib.py (shared with import.py). No model call,
# never blocks, always exit 0 — enrichment, not the source-of-truth prompt.

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

payload="$(cat 2>/dev/null)" || exit 0
command -v python3 >/dev/null 2>&1 || exit 0   # field extraction + redaction live in devbrain_lib.py

# Field extraction via the per-harness event shim (keyed by $DEVBRAIN_HARNESS) in
# devbrain_lib.py — the single place that knows the host harness's hook JSON shape.
_hd="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
for _c in "$_hd/hook-common.sh" "$HOME/.claude/hooks/devbrain-hook-common.sh"; do
  [ -f "$_c" ] && { . "$_c"; break; }
done
command -v devbrain_read_event >/dev/null 2>&1 || exit 0
devbrain_has_python_lib || exit 0

transcript="$(devbrain_read_event transcript)"
cwd="$(devbrain_read_event cwd)"
session="$(devbrain_read_event session)"
last_assistant="$(devbrain_read_event last-assistant-message)"
[ -n "$transcript" ] && [ -f "$transcript" ] || exit 0
[ -n "$cwd" ] || cwd="$PWD"

# Same identity resolution as capture.sh — via the shared OFFLINE resolver
# (project-key.sh) — so we append to the SAME projects/<owner>__<repo> folder the
# prompt was captured to. This MUST match capture.sh; deriving the project any other
# way (e.g. the bare basename) sends the recap to a different folder and it's lost.
# Installed alongside as devbrain-project-key.sh; repo copy is hooks/project-key.sh.
devbrain_source_project_key || exit 0
project="$(devbrain_project_key "$cwd" "$DATA")"; [ -n "$project" ] || project="unknown"
worktree="$(devbrain_worktree_slug "$cwd")"
session="$(devbrain_sanitize "$session")"; [ -n "$session" ] || session="nosession"

file="$DATA/projects/$project/log/$(date -u +%F)/$worktree.$session.md"   # UTC day, matches capture.sh
# Token capture must NOT depend on a logged prompt. Nightshift workers (and any session
# whose first prompt capture.sh filtered as synthetic) have no log file, yet burn real
# tokens. This live Stop is the fast path but can't fire for a SIGKILLed worker; the
# orchestrator's teardown backfills those via import.py. Run the harvest regardless; gate
# ONLY the human-readable log-append (below) on the file existing.
log_exists=1; [ -e "$file" ] || log_exists=0

# Build the recap + a bounded response sample via the ONE summarizer in
# devbrain_lib.py (merged-#15: closing sentence + head/middle body). It also parses
# the transcript into the turn's text/tool/file lists, so live capture and import share
# one token/tool/recap implementation.
# It ALSO sums this turn's token usage + model from the transcript and (a) adds a
# parseable `tokens: in/out/cache_create/cache_read · model: …` field to the meta line,
# (b) appends one machine record to projects/<proj>/tokens.jsonl — the sidecar the
# dashboard's cost view reads (same per-project JSONL shape as gbrain-queries.log).
sidecar="$DATA/projects/$project/tokens.jsonl"
mkdir -p "$DATA/projects/$project" 2>/dev/null   # no-log sessions: capture.sh never made the dir
rec_ts="$(date -u +%FT%TZ)"   # UTC instant for the token record (matches capture.sh tz)
# auto = this is an autonomous (nightshift/drain worker) session, not interactive — so the
# cost view's typed/bot toggle can split your spend from the fleet's. Same rule as the
# queue's session_is_autonomous: cwd under nightshift/drain, or a -w<N> worktree.
auto=0
case "$cwd" in */nightshift/*|*/drain/*) auto=1;; esac
[ "$auto" = 1 ] || { [[ "$worktree" =~ -w[0-9]+$ ]] && auto=1; }
out="$(python3 "$DEVBRAIN_LIB" response-capture "$transcript" "$sidecar" "$session" "$rec_ts" "$auto" "$last_assistant" 2>/dev/null)"

# The token sidecar was already written inside the heredoc above (its side effect, run
# unconditionally). The block below is the human-readable trace — only meaningful when a
# prompt was logged for this session-day, so skip it when the log file is absent.
[ "$log_exists" = 1 ] || exit 0

summary="$(printf '%s' "$out" | sed -n '1p')"
meta="$(printf '%s' "$out" | sed -n '2p')"
body="$(printf '%s' "$out" | tail -n +3)"
[ -n "$summary$meta$body" ] || exit 0

{
  ts="$(date -u +%H:%M:%S)"   # UTC, matches capture.sh
  [ -n "$summary" ] && printf '↳ %s — %s\n' "$ts" "$summary" || printf '↳ %s — (response)\n' "$ts"
  [ -n "$meta" ] && printf '   %s\n' "$meta"
  if [ -n "$body" ]; then
    printf '   ⤷ response sample:\n'
    printf '%s\n' "$body" | sed 's/^/   > /'   # quote each line so the block is clearly delimited
  fi
  printf '\n'
} >> "$file" 2>/dev/null

exit 0
