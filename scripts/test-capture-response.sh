#!/usr/bin/env bash
# devbrain — capture-response.sh integration tests. Feeds a fake transcript +
# Stop-hook payload and checks what gets appended to the session log. Guards the
# path that silently regressed once and the response-sample capture.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; HOOK="$HERE/../hooks/capture-response.sh"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not installed"; exit 0; }

export DEVBRAIN_DATA="$(mktemp -d)"
export DEVBRAIN_PROJECT="testproj"     # deterministic project key (resolver honors this)
workdir="$(mktemp -d)"
trap 'rm -rf "$DEVBRAIN_DATA" "$workdir"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

day="$(date -u +%F)"
mklog(){ # <session> -> echoes the log path and pre-creates it with a prompt line
  local s="$1" wt logdir logfile
  wt="$(printf '%s' "$(basename "$workdir")" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-')"
  logdir="$DEVBRAIN_DATA/projects/$DEVBRAIN_PROJECT/log/$day"; mkdir -p "$logdir"
  logfile="$logdir/$wt.$s.md"; printf '# testproj log\n\n## 00:00:00\n\nprompt\n\n' > "$logfile"
  printf '%s' "$logfile"
}
fire(){ # <transcript> <session>
  python3 -c 'import json,sys;print(json.dumps({"transcript_path":sys.argv[1],"cwd":sys.argv[2],"session_id":sys.argv[3]}))' "$1" "$workdir" "$2" | bash "$HOOK"
}

## --- Case 1: short response, two assistant blocks (kept whole) ---
t1="$workdir/t1.jsonl"
{
  printf '%s\n' '{"type":"user","message":{"content":[{"type":"text","text":"please refactor foo"}]}}'
  printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"Let me look."},{"type":"tool_use","name":"Read","input":{"file_path":"/x/foo.py"}}]}}'
  printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"Did the refactor.\n\nDecided to keep it simple, no extra config. Token sk-abcdefghijklmnopqrstuvwxyz0123 leaked.\n\nRefactored foo.py to remove the duplicate loop."}]}}'
} > "$t1"

# Guard: no pre-existing log file -> hook is a no-op (nothing to attach to).
fire "$t1" "absent"
check "no-op when log file absent"  '[ ! -e "$DEVBRAIN_DATA/projects/$DEVBRAIN_PROJECT/log/$day/$(basename "$workdir" | tr "[:upper:] " "[:lower:]-").absent.md" ]'

L1="$(mklog short)"; fire "$t1" short
check "appends recap arrow"         'grep -q "↳ .* — " "$L1"'
check "recap = closing sentence"    'grep -q "Refactored foo.py to remove the duplicate loop." "$L1"'
check "meta records tool"           'grep -q "tools: Read" "$L1"'
check "meta records touched file"   'grep -q "touched: foo.py" "$L1"'
check "labels response sample"      'grep -q "response sample:" "$L1"'
check "captures whole turn (head)"  'grep -q "   > Let me look." "$L1"'   # intermediate block, not just final msg
check "body includes reasoning"     'grep -q "Decided to keep it simple" "$L1"'
check "short response not sampled"  '! grep -q "\[…\]" "$L1"'             # under cap -> stored whole, no gap markers
check "secret redacted in body"     'grep -q "REDACTED" "$L1" && ! grep -q "sk-abcdefghijklmnopqrstuvwxyz0123" "$L1"'
check "no tokens field w/o usage"   '! grep -q "tokens: " "$L1"'          # t1 has no message.usage -> field omitted, hook still clean

## --- Case 3: transcript WITH per-message usage + model ---
SIDE="$DEVBRAIN_DATA/projects/$DEVBRAIN_PROJECT/tokens.jsonl"
t3="$workdir/t3.jsonl"
{
  printf '%s\n' '{"type":"user","message":{"content":[{"type":"text","text":"do work"}]}}'
  printf '%s\n' '{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":300,"cache_read_input_tokens":400},"content":[{"type":"text","text":"first."}]}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-06-23T10:01:00.000Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":5,"output_tokens":42,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Summed the turn cleanly."}]}}'
} > "$t3"
L3="$(mklog tok)"; fire "$t3" tok
check "meta emits summed tokens"    'grep -q "tokens: 105/242/300/400" "$L3"'   # in/out/cc/cr summed across both blocks
check "meta records model"          'grep -q "model: claude-opus-4-8" "$L3"'
check "sidecar tokens.jsonl written" '[ -s "$SIDE" ]'
check "sidecar has summed record"   'grep -q "\"in\": 105" "$SIDE" && grep -q "\"out\": 242" "$SIDE" && grep -q "claude-opus-4-8" "$SIDE"'
check "sidecar marks interactive"   'grep -q "\"auto\": false" "$SIDE"'   # workdir is not a nightshift worker
# Record is keyed on the turn's RESPONSE timestamp (last assistant event), normalized to
# seconds+Z — the SAME key import.py writes, so the two writers dedup per (session, ts).
check "sidecar ts = normalized turn ts" 'grep -q "\"ts\": \"2026-06-23T10:01:00Z\"" "$SIDE"'

## --- Case 2: long response (> cap) -> head + middle sampled, tail dropped ---
big="$(yes 'lorem ipsum dolor sit amet' | head -c 6000 | tr '\n' ' ')"
longtext="$(printf 'HEADMARKER opening framing. %s\n\nENDMARKER_DROPPED in its own paragraph near the end.\n\nFinal sampling sentence here.' "$big")"
t2="$workdir/t2.jsonl"
{
  printf '%s\n' '{"type":"user","message":{"content":[{"type":"text","text":"do a big thing"}]}}'
  python3 -c 'import json,sys;print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":sys.argv[1]}]}}))' "$longtext"
} > "$t2"

L2="$(mklog long)"; fire "$t2" long
check "samples long response"       'grep -q "\[…\]" "$L2"'                       # gap markers present
check "sample keeps head"           'grep -q "HEADMARKER opening framing" "$L2"'
check "sample drops tail region"    '! grep -q "ENDMARKER_DROPPED" "$L2"'         # past middle window -> not stored
check "recap still final sentence"  'grep -q "Final sampling sentence here." "$L2"'
check "sample is bounded (<5k)"     '[ "$(wc -c < "$L2")" -lt 5500 ]'            # whole 6k+ response would blow this

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
