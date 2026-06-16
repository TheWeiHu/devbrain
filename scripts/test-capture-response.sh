#!/usr/bin/env bash
# devbrain — capture-response.sh integration tests. Feeds a fake transcript +
# Stop-hook payload and checks what gets appended to the session log. Guards the
# path that silently regressed in #12 and the full-response capture from task 0013.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; HOOK="$HERE/../hooks/capture-response.sh"
command -v jq >/dev/null 2>&1 || { echo "skip: jq not installed"; exit 0; }
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not installed"; exit 0; }

export DEVBRAIN_DATA="$(mktemp -d)"
export DEVBRAIN_PROJECT="testproj"     # deterministic project key (resolver honors this)
workdir="$(mktemp -d)"
trap 'rm -rf "$DEVBRAIN_DATA" "$workdir"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

session="testsession"
worktree="$(printf '%s' "$(basename "$workdir")" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-')"
day="$(date -u +%F)"
logdir="$DEVBRAIN_DATA/projects/$DEVBRAIN_PROJECT/log/$day"
logfile="$logdir/$worktree.$session.md"

# A transcript: user prompt, an intermediate assistant turn (text + tool_use), and
# a final assistant message whose closing line is the recap. Body has a fake secret.
transcript="$workdir/transcript.jsonl"
{
  printf '%s\n' '{"type":"user","message":{"content":[{"type":"text","text":"please refactor foo"}]}}'
  printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"Let me look."},{"type":"tool_use","name":"Read","input":{"file_path":"/x/foo.py"}}]}}'
  printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"Did the refactor.\n\nDecided to keep it simple, no extra config. Token sk-abcdefghijklmnopqrstuvwxyz0123 leaked.\n\nRefactored foo.py to remove the duplicate loop.","type":"text"}]}}'
} > "$transcript"

payload="$(jq -n --arg t "$transcript" --arg c "$workdir" --arg s "$session" \
  '{transcript_path:$t, cwd:$c, session_id:$s}')"

run(){ printf '%s' "$payload" | bash "$HOOK"; }

# Guard 1: no pre-existing log file -> hook is a no-op (nothing to attach to).
run
check "no-op when log file absent" '[ ! -e "$logfile" ]'

# Now create the log file (a prompt was captured) and run for real.
mkdir -p "$logdir"
printf '# testproj log\n\n## 00:00:00\n\nplease refactor foo\n\n' > "$logfile"
run

check "appends recap arrow"        'grep -q "↳ .* — " "$logfile"'
check "recap = closing sentence"   'grep -q "Refactored foo.py to remove the duplicate loop." "$logfile"'
check "meta records tool"          'grep -q "tools: Read" "$logfile"'
check "meta records touched file"  'grep -q "touched: foo.py" "$logfile"'
check "captures full response"     'grep -q "full response:" "$logfile"'
check "body includes reasoning"    'grep -q "Decided to keep it simple" "$logfile"'
check "body is quoted block"       'grep -q "   > Did the refactor." "$logfile"'
check "secret redacted in body"    'grep -q "REDACTED" "$logfile" && ! grep -q "sk-abcdefghijklmnopqrstuvwxyz0123" "$logfile"'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
