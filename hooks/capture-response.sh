#!/usr/bin/env bash
# devbrain — Stage A capture, response side (Stop hook).
#
# Fires when the agent finishes a turn. Appends a compact, MODEL-FREE trace of the
# response under the matching prompt in the same session log: the opening sentence
# of the agent's FINAL message (the turn's conclusion — where the recap lives; the
# global CLAUDE.md instruction tells the agent to open its final message with one),
# plus the files touched and tools used, extracted from the transcript. No model
# call, never blocks, always exit 0 — enrichment, not the source-of-truth prompt.

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

payload="$(cat 2>/dev/null)" || exit 0
command -v jq >/dev/null 2>&1 || exit 0
command -v python3 >/dev/null 2>&1 || exit 0

transcript="$(printf '%s' "$payload" | jq -r '.transcript_path // empty' 2>/dev/null)"
cwd="$(printf '%s'        "$payload" | jq -r '.cwd // empty' 2>/dev/null)"
session="$(printf '%s'    "$payload" | jq -r '.session_id // "nosession"' 2>/dev/null)"
[ -n "$transcript" ] && [ -f "$transcript" ] || exit 0
[ -n "$cwd" ] || cwd="$PWD"

# Same identity resolution as capture.sh, so we append to the same file.
remote="$(git -C "$cwd" remote get-url origin 2>/dev/null)"
if [ -n "$remote" ]; then project="$(basename "${remote%.git}")"; else project="$(basename "$cwd")"; fi
toplevel="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)"
worktree="$(basename "${toplevel:-$cwd}")"
sanitize() { printf '%s' "$1" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-'; }
project="$(sanitize "$project")";   [ -n "$project" ]  || project="unknown"
worktree="$(sanitize "$worktree")"; [ -n "$worktree" ] || worktree="unknown"
session="$(sanitize "$session")";   [ -n "$session" ]  || session="nosession"

file="$DATA/projects/$project/log/$(date +%F)/$worktree.$session.md"
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
# Use the first substantive line: skip blanks and pure markdown heading lines
# ("## Done"), and strip leading bullet/quote markers, so the recap reads clean.
chosen = ""
for line in last_text.splitlines():
    s = re.sub(r"^[>\-\*\s]+", "", line.strip())
    if not s or s.startswith("#"):
        continue
    chosen = s
    break
if not chosen:
    chosen = re.sub(r"^[#>\-\*\s]+", "", last_text.strip())
chosen = re.sub(r"\s+", " ", chosen).strip()
parts = re.findall(r".+?[.!?](?:\s|$)", chosen)
if parts:
    summary = parts[0].strip()
    if len(summary) < 60 and len(parts) > 1:   # extend a too-short headline
        summary = (summary + " " + parts[1].strip()).strip()
else:
    summary = chosen
summary = summary[:300].strip()

meta = []
if files: meta.append("touched: " + ", ".join(files))
if tools: meta.append("tools: " + ", ".join(f"{k}×{v}" for k, v in tools.items()))
if not summary and not meta: sys.exit(0)
print(summary)
print("  ·  ".join(meta))
PY
)"

summary="$(printf '%s' "$out" | sed -n '1p')"
meta="$(printf '%s' "$out" | sed -n '2p')"
[ -n "$summary$meta" ] || exit 0

{
  ts="$(date +%H:%M:%S)"
  [ -n "$summary" ] && printf '↳ %s — %s\n' "$ts" "$summary" || printf '↳ %s — (response)\n' "$ts"
  [ -n "$meta" ] && printf '   %s\n' "$meta"
  printf '\n'
} >> "$file" 2>/dev/null

exit 0
