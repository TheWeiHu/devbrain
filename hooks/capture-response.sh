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
# Token capture must NOT depend on a logged prompt. Nightshift workers (and any session
# whose first prompt capture.sh filtered as synthetic) have no log file, yet burn real
# tokens. This live Stop is the fast path but can't fire for a SIGKILLed worker; the
# orchestrator's teardown backfills those via import.py. Run the harvest regardless; gate
# ONLY the human-readable log-append (below) on the file existing.
log_exists=1; [ -e "$file" ] || log_exists=0

# Build the recap + a bounded response sample via the ONE summarizer in
# devbrain_lib.py (merged-#15: closing sentence + head/middle body). The heredoc only
# parses the transcript into the turn's text/tool/file lists; recap/sample/redact are
# the shared rules. _libdir reuses the dir the project-key resolver already found.
# It ALSO sums this turn's token usage + model from the transcript and (a) adds a
# parseable `tokens: in/out/cache_create/cache_read · model: …` field to the meta line,
# (b) appends one machine record to projects/<proj>/tokens.jsonl — the sidecar the
# dashboard's cost view reads (same per-project JSONL shape as gbrain-queries.log).
_libdir="$_pk"; [ -f "$_libdir/devbrain_lib.py" ] || _libdir="$HOME/.claude/hooks"
sidecar="$DATA/projects/$project/tokens.jsonl"
mkdir -p "$DATA/projects/$project" 2>/dev/null   # no-log sessions: capture.sh never made the dir
rec_ts="$(date -u +%FT%TZ)"   # UTC instant for the token record (matches capture.sh tz)
# auto = this is an autonomous (nightshift/drain worker) session, not interactive — so the
# cost view's typed/bot toggle can split your spend from the fleet's. Same rule as the
# queue's session_is_autonomous: cwd under nightshift/drain, or a -w<N> worktree.
auto=0
case "$cwd" in */nightshift/*|*/drain/*) auto=1;; esac
[ "$auto" = 1 ] || { [[ "$worktree" =~ -w[0-9]+$ ]] && auto=1; }
out="$(python3 - "$transcript" "$_libdir" "$sidecar" "$session" "$rec_ts" "$auto" <<'PY' 2>/dev/null
import json, sys, re, datetime
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
tin = tout = tcc = tcr = 0; model = ""    # token usage summed across the turn; model = last seen
seen_ids = set()                          # message ids already counted (see dedup note below)
turn_ts = ""                              # the turn's response timestamp (last assistant event)
for e in segment:
    if e.get("type") != "assistant": continue
    msg = e.get("message", {}) or {}
    # Claude Code writes one transcript line PER content block (thinking/text/tool_use),
    # each repeating the SAME message-level usage. Count each response once, keyed by
    # message id, or we inflate by the block count (often 2-3x, mostly cache_read).
    mid = msg.get("id")
    if mid not in seen_ids:
        seen_ids.add(mid)
        u = msg.get("usage") or {}
        tin += u.get("input_tokens") or 0
        tout += u.get("output_tokens") or 0
        tcc += u.get("cache_creation_input_tokens") or 0
        tcr += u.get("cache_read_input_tokens") or 0
    if msg.get("model"): model = msg["model"]
    if e.get("timestamp"): turn_ts = e["timestamp"]
    for b in msg.get("content", []):
        if not isinstance(b, dict): continue
        if b.get("type") == "text":
            texts.append(b.get("text", ""))
        elif b.get("type") == "tool_use":
            n = b.get("name", "?")
            inp = b.get("input", {}) or {}
            # Name the skill the model actually ran (Skill:distill), not a bare Skill×N —
            # autonomous invocations carry no leading slash in the prompt, so this meta
            # field is the only record of WHICH skill fired (the dashboard reads it).
            if n == "Skill":
                sk = inp.get("skill") or inp.get("name")
                if sk: n = "Skill:" + str(sk)
            tools[n] = tools.get(n, 0) + 1
            fp = inp.get("file_path") or inp.get("path")
            if fp: files[fp.rsplit("/", 1)[-1]] = True

summary = devbrain_lib.recap(texts)        # the closing sentence (the tail)
meta = []
if files: meta.append("touched: " + ", ".join(files))
if tools: meta.append("tools: " + ", ".join(f"{k}×{v}" for k, v in tools.items()))
if tin or tout or tcc or tcr:              # usage present (older transcripts may lack it)
    meta.append(f"tokens: {tin}/{tout}/{tcc}/{tcr}" + (f" · model: {model}" if model else ""))
    sidecar = sys.argv[3] if len(sys.argv) > 3 else ""
    if sidecar:                            # best-effort sidecar append; never block the hook
        try:
            # Key the record on the turn's RESPONSE timestamp (normalized to seconds+Z), the
            # same value import.py writes — so the two writers share one per-(session, ts) key
            # and dedup exactly, no double-count, no missed turns. Fall back to the hook-fire
            # time (argv[5]) for older transcripts that carry no per-event timestamp.
            try:
                ts = datetime.datetime.fromisoformat(turn_ts.replace("Z", "+00:00")).strftime("%Y-%m-%dT%H:%M:%SZ")
            except Exception:
                ts = sys.argv[5]
            rec = {"ts": ts, "session": sys.argv[4], "model": model,
                   "in": tin, "out": tout, "cache_create": tcc, "cache_read": tcr,
                   "auto": (len(sys.argv) > 6 and sys.argv[6] == "1")}
            with open(sidecar, "a", encoding="utf-8") as fh:
                fh.write(json.dumps(rec) + "\n")
        except Exception:
            pass
body = devbrain_lib.sample(texts)          # head + middle of the whole turn
if not summary and not meta and not body: sys.exit(0)
print(devbrain_lib.redact(summary))               # line 1: recap sentence
print(devbrain_lib.redact("  ·  ".join(meta)))    # line 2: touched/tools (may be blank)
print(devbrain_lib.redact(body))                  # line 3+: response sample
PY
)"

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
