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
codex_fire(){ # <transcript> <turn-id>
  python3 -c 'import json,sys;print(json.dumps({"transcript_path":sys.argv[1],"cwd":sys.argv[2],"turn_id":sys.argv[3],"last_assistant_message":"fallback recap."}))' "$1" "$workdir" "$2" \
    | DEVBRAIN_HARNESS=codex bash "$HOOK"
}

## --- Case 1: short response, two assistant blocks (kept whole) ---
t1="$workdir/t1.jsonl"
{
  printf '%s\n' '{"type":"user","message":{"content":[{"type":"text","text":"please refactor foo"}]}}'
  printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"Let me look."},{"type":"tool_use","name":"Read","input":{"file_path":"/x/foo.py"}},{"type":"tool_use","name":"Skill","input":{"skill":"distill"}}]}}'
  printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"Did the refactor.\n\nDecided to keep it simple, no extra config. Token sk-abcdefghijklmnopqrstuvwxyz0123 leaked.\n\nRefactored foo.py to remove the duplicate loop."}]}}'
} > "$t1"

# Guard: no pre-existing log file -> hook is a no-op (nothing to attach to).
fire "$t1" "absent"
check "no-op when log file absent"  '[ ! -e "$DEVBRAIN_DATA/projects/$DEVBRAIN_PROJECT/log/$day/$(basename "$workdir" | tr "[:upper:] " "[:lower:]-").absent.md" ]'

L1="$(mklog short)"; fire "$t1" short
check "appends recap arrow"         'grep -q "↳ .* — " "$L1"'
check "recap = closing sentence"    'grep -q "Refactored foo.py to remove the duplicate loop." "$L1"'
check "meta records tool"           'grep -q "tools: Read" "$L1"'
check "meta names the skill run"     'grep -q "Skill:distill×1" "$L1"'   # autonomous invocation, attributable by name
check "meta records touched file"   'grep -q "touched: foo.py" "$L1"'
check "labels response sample"      'grep -q "response sample:" "$L1"'
check "captures whole turn (head)"  'grep -q "   > Let me look." "$L1"'   # intermediate block, not just final msg
check "body includes reasoning"     'grep -q "Decided to keep it simple" "$L1"'
check "short response not sampled"  '! grep -q "\[…\]" "$L1"'             # under cap -> stored whole, no gap markers
check "secret redacted in body"     'grep -q "REDACTED" "$L1" && ! grep -q "sk-abcdefghijklmnopqrstuvwxyz0123" "$L1"'
check "no tokens field w/o usage"   '! grep -q "tokens: " "$L1"'          # t1 has no message.usage -> field omitted, hook still clean

## --- Case 3: transcript WITH per-message usage + model ---
# Two DISTINCT responses (ids msg_aaa, msg_bbb) summed; msg_aaa is written across two
# transcript lines (one per content block) that REPEAT its message-level usage — exactly
# how Claude Code logs a multi-block turn. The hook dedups by message.id, so msg_aaa's
# usage counts once: without that dedup the repeated block would double it to 200/400/600/800
# and the 105/242/300/400 assertion below would fail. So this case guards #114's id-dedup too.
SIDE="$DEVBRAIN_DATA/projects/$DEVBRAIN_PROJECT/tokens.jsonl"
t3="$workdir/t3.jsonl"
{
  printf '%s\n' '{"type":"user","message":{"content":[{"type":"text","text":"do work"}]}}'
  printf '%s\n' '{"type":"assistant","message":{"id":"msg_aaa","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":300,"cache_read_input_tokens":400},"content":[{"type":"text","text":"first."}]}}'
  printf '%s\n' '{"type":"assistant","message":{"id":"msg_aaa","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":300,"cache_read_input_tokens":400},"content":[{"type":"text","text":"first, block two."}]}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-06-23T10:01:00.000Z","message":{"id":"msg_bbb","model":"claude-opus-4-8","usage":{"input_tokens":5,"output_tokens":42,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Summed the turn cleanly."}]}}'
} > "$t3"
L3="$(mklog tok)"; fire "$t3" tok
check "meta emits summed tokens"    'grep -q "tokens: 105/242/300/400" "$L3"'   # msg_aaa (deduped) + msg_bbb, in/out/cc/cr
check "meta records model"          'grep -q "model: claude-opus-4-8" "$L3"'
check "sidecar tokens.jsonl written" '[ -s "$SIDE" ]'
check "sidecar has summed record"   'grep -q "\"in\": 105" "$SIDE" && grep -q "\"out\": 242" "$SIDE" && grep -q "claude-opus-4-8" "$SIDE"'
check "sidecar marks interactive"   'grep -q "\"auto\": false" "$SIDE"'   # workdir is not a nightshift worker
# Record is keyed on the turn's RESPONSE timestamp (last assistant event), normalized to
# seconds+Z — the SAME key import.py writes, so the two writers dedup per (session, ts).
check "sidecar ts = normalized turn ts" 'grep -q "\"ts\": \"2026-06-23T10:01:00Z\"" "$SIDE"'

## --- Case 4: sidechain/sub-agent transcript entries do not truncate the parent turn ---
t4="$workdir/t4.jsonl"
{
  printf '%s\n' '{"type":"user","message":{"content":[{"type":"text","text":"run parent task"}]}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-06-23T11:00:10.000Z","message":{"id":"msg_parent_a","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Started parent work."}]}}'
  printf '%s\n' '{"type":"user","isSidechain":true,"message":{"content":[{"type":"text","text":"sub-agent prompt"}]}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-06-23T11:00:20.000Z","message":{"id":"msg_side","model":"claude-opus-4-8","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Sub-agent investigated."}]}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-06-23T11:00:30.000Z","message":{"id":"msg_parent_b","model":"claude-opus-4-8","usage":{"input_tokens":3,"output_tokens":4,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Finished parent turn."}]}}'
} > "$t4"
L4="$(mklog side)"; fire "$t4" side
check "sidechain keeps parent head"       'grep -q "Started parent work" "$L4"'
check "sidechain keeps sub-agent text"    'grep -q "Sub-agent investigated" "$L4"'
check "sidechain recap is parent final"   'grep -q "Finished parent turn." "$L4"'
check "sidechain tokens include whole turn" 'grep -q "tokens: 14/26/0/0" "$L4"'
check "sidechain sidecar ts is final parent response" 'grep -q "\"ts\": \"2026-06-23T11:00:30Z\".*\"session\": \"side\".*\"in\": 14.*\"out\": 26" "$SIDE"'

## --- Case 5: Codex transcript shape -> same response log shape ---
tc="$workdir/tc.jsonl"
{
  printf '%s\n' '{"timestamp":"2026-06-29T19:32:05.000Z","type":"session_meta","payload":{"id":"codex-session","cwd":"/tmp/repo","source":"exec"}}'
  printf '%s\n' '{"timestamp":"2026-06-29T19:32:05.500Z","type":"turn_context","payload":{"turn_id":"codexturn","model":"gpt-5.5","cwd":"/tmp/repo"}}'
  printf '%s\n' '{"timestamp":"2026-06-29T19:32:06.000Z","type":"event_msg","payload":{"type":"user_message","message":"do codex work"}}'
  # Codex injects the invoked skill as a role=user response_item opening with <skill><name>… —
  # its equivalent of Claude's Skill tool_use; capture must credit it as Skill:<name>.
  printf '%s\n' '{"timestamp":"2026-06-29T19:32:06.500Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<skill>\n<name>continue</name>\n<path>/x/continue/SKILL.md</path>\n---\nname: continue\n"}]}}'
  printf '%s\n' '{"timestamp":"2026-06-29T19:32:07.000Z","type":"event_msg","payload":{"type":"exec_command_begin"}}'
  printf '%s\n' '{"timestamp":"2026-06-29T19:32:08.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Checked the repo and made the change."}],"phase":"final_answer"}}'
  printf '%s\n' '{"timestamp":"2026-06-29T19:32:09.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15}}}}'
  printf '%s\n' '{"timestamp":"2026-06-29T19:32:09.500Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":11,"cached_input_tokens":6,"output_tokens":7,"reasoning_output_tokens":2,"total_tokens":18}}}}'
  printf '%s\n' '{"timestamp":"2026-06-29T19:32:10.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"codexturn","last_agent_message":"Checked the repo and made the change.","completed_at":1782761530}}'
} > "$tc"
LC="$(mklog codexturn)"; codex_fire "$tc" codexturn
check "codex recap appended"        'grep -q "Checked the repo and made the change." "$LC"'
check "codex tool meta recorded"    'grep -q "Bash×1" "$LC"'
check "codex names the skill run"   'grep -q "Skill:continue×1" "$LC"'   # <skill><name> marker → Skill:<name>
check "codex tokens recorded"       'grep -q "tokens: 11/12/0/10" "$LC"'
check "codex model recorded"        'grep -q "model: gpt-5.5" "$LC"'
check "codex sidecar written"       'grep -q "\"session\": \"codexturn\"" "$SIDE" && grep -q "\"in\": 11" "$SIDE" && grep -q "gpt-5.5" "$SIDE"'

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
