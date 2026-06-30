#!/usr/bin/env bash
# devbrain — devbrain_lib.py `read-event` shim tests. The shim is the ONE place that
# knows a host harness's hook JSON shape (so codex/cursor plug in as one new mapping).
# Feeds Claude-shaped payloads on stdin and checks each normalized field, plus the
# unknown-harness fallback and multiline-safety.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; LIB="$HERE/../hooks/devbrain_lib.py"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not installed"; exit 0; }

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }
ev(){ printf '%s' "$1" | python3 "$LIB" read-event "$2" 2>/dev/null; }
# jq-free payload builder: keys+values as alternating args -> a JSON object (python3).
mkpayload(){ python3 -c 'import json,sys;a=sys.argv[1:];print(json.dumps(dict(zip(a[::2],a[1::2]))))' "$@"; }

# UserPromptSubmit-shaped payload, with a multiline prompt to prove line-safety.
P="$(mkpayload prompt $'fix the\nbug' cwd /tmp/wd session_id sess1)"
check "prompt extracted (multiline)" '[ "$(ev "$P" prompt)" = "$(printf "fix the\nbug")" ]'
check "cwd extracted"                '[ "$(ev "$P" cwd)" = "/tmp/wd" ]'
check "session_id -> session"        '[ "$(ev "$P" session)" = "sess1" ]'
check "absent field -> empty"        '[ -z "$(ev "$P" transcript)" ]'
check "unknown field -> empty"       '[ -z "$(ev "$P" bogus)" ]'

# PostToolUse-shaped payload: tool_name, nested tool_input.command, tool_response object.
T="$(python3 -c 'import json;print(json.dumps({"tool_name":"Bash","tool_input":{"command":"gbrain search x"},"tool_response":{"stdout":"[0.9] a/b -- hit","output":"ignored"}}))')"
check "tool_name -> tool"            '[ "$(ev "$T" tool)" = "Bash" ]'
check "nested command extracted"     '[ "$(ev "$T" command)" = "gbrain search x" ]'
check "tool_response object -> stdout text" '[ "$(ev "$T" tool-response)" = "[0.9] a/b -- hit" ]'

# tool_response as a bare string coerces to itself.
S="$(python3 -c 'import json;print(json.dumps({"tool_response":"plain text out"}))')"
check "tool_response string -> itself" '[ "$(ev "$S" tool-response)" = "plain text out" ]'

# Unknown harness falls back to the claude mapping (fail-open).
check "unknown harness falls back to claude" \
  '[ "$(DEVBRAIN_HARNESS=nope ev "$P" cwd)" = "/tmp/wd" ]'
# Malformed JSON yields empty, never an error.
check "malformed payload -> empty"   '[ -z "$(ev "not json" prompt)" ]'

# Codex-shaped payloads: same normalized fields, different hook JSON keys.
C="$(python3 -c 'import json;print(json.dumps({"prompt":"codex prompt","cwd":"/tmp/codex","turn_id":"turn1","transcript_path":"/tmp/codex/session.jsonl","last_assistant_message":"finished"}))')"
check "codex prompt extracted"       '[ "$(DEVBRAIN_HARNESS=codex ev "$C" prompt)" = "codex prompt" ]'
check "codex cwd extracted"          '[ "$(DEVBRAIN_HARNESS=codex ev "$C" cwd)" = "/tmp/codex" ]'
check "codex turn_id -> session"     '[ "$(DEVBRAIN_HARNESS=codex ev "$C" session)" = "turn1" ]'
check "codex transcript extracted"   '[ "$(DEVBRAIN_HARNESS=codex ev "$C" transcript)" = "/tmp/codex/session.jsonl" ]'
check "codex last assistant fallback" '[ "$(DEVBRAIN_HARNESS=codex ev "$C" last-assistant-message)" = "finished" ]'

CT="$(python3 -c 'import json;print(json.dumps({"tool":{"name":"Bash"},"input":{"command":"gbrain search codex"},"output":{"stdout":"[0.9] p/q -- hit"}}))')"
check "codex nested tool extracted"  '[ "$(DEVBRAIN_HARNESS=codex ev "$CT" tool)" = "Bash" ]'
check "codex command extracted"      '[ "$(DEVBRAIN_HARNESS=codex ev "$CT" command)" = "gbrain search codex" ]'
check "codex output coerced"         '[ "$(DEVBRAIN_HARNESS=codex ev "$CT" tool-response)" = "[0.9] p/q -- hit" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
