#!/usr/bin/env bash
# devbrain — Stage A capture, response side (Stop hook).
#
# Fires when the agent finishes a turn. Appends a compact, MODEL-FREE trace of the
# response under the matching prompt in the same session log: the closing sentence
# of the agent's FINAL message (the turn's conclusion — where the recap lives; the
# global CLAUDE.md instruction tells the agent to end its final message with one),
# the files touched and tools used, AND the full final message body (bounded), so
# the log holds the turn's reasoning/decisions — not just a headline — and distill
# can rebuild full-fidelity pages from logs alone (task 0013). No model call, never
# blocks, always exit 0 — enrichment, not the source-of-truth prompt.

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

payload="$(cat 2>/dev/null)" || exit 0
command -v jq >/dev/null 2>&1 || exit 0
command -v python3 >/dev/null 2>&1 || exit 0

transcript="$(printf '%s' "$payload" | jq -r '.transcript_path // empty' 2>/dev/null)"
cwd="$(printf '%s'        "$payload" | jq -r '.cwd // empty' 2>/dev/null)"
session="$(printf '%s'    "$payload" | jq -r '.session_id // "nosession"' 2>/dev/null)"
[ -n "$transcript" ] && [ -f "$transcript" ] || exit 0
[ -n "$cwd" ] || cwd="$PWD"

# Same identity resolution as capture.sh — via the shared OFFLINE resolver
# (project-key.sh) — so we append to the SAME projects/<owner>__<repo> folder the
# prompt was captured to. This MUST match capture.sh; deriving the project any other
# way (e.g. the bare basename) sends the recap to a different folder and it's lost.
# Installed alongside as devbrain-project-key.sh; repo copy is hooks/project-key.sh.
_pk="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
for _c in "$_pk/devbrain-project-key.sh" "$_pk/project-key.sh" "$HOME/.claude/hooks/devbrain-project-key.sh"; do
  [ -f "$_c" ] && { . "$_c"; break; }
done
sanitize() { printf '%s' "$1" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-'; }
project="$(devbrain_project_key "$cwd" "$DATA")"; [ -n "$project" ] || project="unknown"
toplevel="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)"
worktree="$(basename "${toplevel:-$cwd}")"
worktree="$(sanitize "$worktree")"; [ -n "$worktree" ] || worktree="unknown"
session="$(sanitize "$session")";   [ -n "$session" ]  || session="nosession"

file="$DATA/projects/$project/log/$(date -u +%F)/$worktree.$session.md"   # UTC day, matches capture.sh
[ -e "$file" ] || exit 0   # no prompt captured for this session-day; nothing to attach to

# Parse the transcript tail for the final response text + tool/file trace.
out="$(python3 - "$transcript" <<'PY' 2>/dev/null
import json, sys, re
from collections import deque, OrderedDict
try:
    with open(sys.argv[1], encoding="utf-8", errors="replace") as fh:
        lines = list(deque(fh, maxlen=1500))   # tail only — bound per-turn cost
except Exception:
    sys.exit(0)

events = []
for ln in lines:
    ln = ln.strip()
    if ln:
        try: events.append(json.loads(ln))
        except Exception: pass

def is_user_prompt(e):
    if e.get("type") != "user": return False
    c = e.get("message", {}).get("content")
    if isinstance(c, str): return bool(c.strip())
    if isinstance(c, list):
        return any(isinstance(b, dict) and b.get("type") == "text" for b in c)
    return False

last_user = max((i for i, e in enumerate(events) if is_user_prompt(e)), default=-1)
segment = events[last_user + 1:] if last_user >= 0 else events

texts, tools, files = [], OrderedDict(), OrderedDict()
for e in segment:
    if e.get("type") != "assistant": continue
    for b in e.get("message", {}).get("content", []):
        if not isinstance(b, dict): continue
        if b.get("type") == "text":
            texts.append(b.get("text", ""))
        elif b.get("type") == "tool_use":
            n = b.get("name", "?"); tools[n] = tools.get(n, 0) + 1
            inp = b.get("input", {}) or {}
            fp = inp.get("file_path") or inp.get("path")
            if fp: files[fp.rsplit("/", 1)[-1]] = True

# Summarize from the LAST non-empty text block (the turn conclusion, where the
# recap lives) rather than the first block, which is usually a preamble.
last_text = ""
for t in texts:
    if t.strip():
        last_text = t
# Use the LAST substantive line: skip blanks and pure markdown heading lines
# ("## Done"), and strip leading bullet/quote markers, so the recap reads clean.
# We read to the bottom because the recap CLOSES the final message.
chosen = ""
for line in last_text.splitlines():
    s = re.sub(r"^[>\-\*\s]+", "", line.strip())
    if not s or s.startswith("#"):
        continue
    chosen = s   # no break — keep going so chosen ends on the LAST substantive line
if not chosen:
    chosen = re.sub(r"^[#>\-\*\s]+", "", last_text.strip())
chosen = re.sub(r"\s+", " ", chosen).strip()
parts = re.findall(r".+?[.!?](?:\s|$)", chosen)
if parts:
    summary = parts[-1].strip()
    if len(summary) < 60 and len(parts) > 1:   # extend a too-short tail backwards
        summary = (parts[-2].strip() + " " + summary).strip()
else:
    summary = chosen
summary = summary[:500].strip()

meta = []
if files: meta.append("touched: " + ", ".join(files))
if tools: meta.append("tools: " + ", ".join(f"{k}×{v}" for k, v in tools.items()))

# Full final message body (bounded) — the reasoning/decisions of the turn verbatim,
# so the log is a faithful projection, not just the one-line recap. Collapse runs of
# blank lines; cap length to keep per-turn log growth sane.
body = re.sub(r"\n{3,}", "\n\n", last_text.strip())
MAXB = 4000
if len(body) > MAXB:
    body = body[:MAXB].rstrip() + "\n… (truncated)"

if not summary and not meta and not body: sys.exit(0)
print(summary)                       # line 1: recap sentence
print("  ·  ".join(meta))            # line 2: touched/tools (may be blank)
print(body)                          # line 3+: full final message body
PY
)"

# Scrub secret shapes before writing — the agent's final message could echo a key.
# Same high-confidence, prefix-anchored, fail-open patterns as capture.sh.
redact() {
  sed -E \
    -e 's/sk-[A-Za-z0-9_-]{20,}/[REDACTED]/g' \
    -e 's/(gh[pousr]_)[A-Za-z0-9]{20,}/[REDACTED]/g' \
    -e 's/github_pat_[A-Za-z0-9_]{20,}/[REDACTED]/g' \
    -e 's/(AKIA|ASIA)[0-9A-Z]{16}/[REDACTED]/g' \
    -e 's/xox[baprs]-[A-Za-z0-9-]{10,}/[REDACTED]/g' \
    -e 's/(Bearer )[A-Za-z0-9._-]{16,}/\1[REDACTED]/g'
}
redacted="$(printf '%s' "$out" | redact 2>/dev/null)"
[ -n "$redacted" ] && out="$redacted"

summary="$(printf '%s' "$out" | sed -n '1p')"
meta="$(printf '%s' "$out" | sed -n '2p')"
body="$(printf '%s' "$out" | tail -n +3)"
[ -n "$summary$meta$body" ] || exit 0

{
  ts="$(date -u +%H:%M:%S)"   # UTC, matches capture.sh
  [ -n "$summary" ] && printf '↳ %s — %s\n' "$ts" "$summary" || printf '↳ %s — (response)\n' "$ts"
  [ -n "$meta" ] && printf '   %s\n' "$meta"
  if [ -n "$body" ]; then
    printf '   ⤷ full response:\n'
    printf '%s\n' "$body" | sed 's/^/   > /'   # quote each line so the block is clearly delimited
  fi
  printf '\n'
} >> "$file" 2>/dev/null

exit 0
