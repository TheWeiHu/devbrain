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
_lib="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)/devbrain_lib.py"
[ -f "$_lib" ] || _lib="$HOME/.claude/hooks/devbrain_lib.py"
ev() { printf '%s' "$payload" | python3 "$_lib" read-event "$1" 2>/dev/null; }

transcript="$(ev transcript)"
cwd="$(ev cwd)"
session="$(ev session)"
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

# Build the recap + a bounded response sample via the ONE summarizer in
# devbrain_lib.py (merged-#15: closing sentence + head/middle body). The heredoc only
# parses the transcript into the turn's text/tool/file lists; recap/sample/redact are
# the shared rules. _libdir reuses the dir the project-key resolver already found.
_libdir="$_pk"; [ -f "$_libdir/devbrain_lib.py" ] || _libdir="$HOME/.claude/hooks"
out="$(python3 - "$transcript" "$_libdir" <<'PY' 2>/dev/null
import json, sys, re
sys.path.insert(0, sys.argv[2]); import devbrain_lib
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

summary = devbrain_lib.recap(texts)        # the closing sentence (the tail)
meta = []
if files: meta.append("touched: " + ", ".join(files))
if tools: meta.append("tools: " + ", ".join(f"{k}×{v}" for k, v in tools.items()))
body = devbrain_lib.sample(texts)          # head + middle of the whole turn
if not summary and not meta and not body: sys.exit(0)
print(devbrain_lib.redact(summary))               # line 1: recap sentence
print(devbrain_lib.redact("  ·  ".join(meta)))    # line 2: touched/tools (may be blank)
print(devbrain_lib.redact(body))                  # line 3+: response sample
PY
)"

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
