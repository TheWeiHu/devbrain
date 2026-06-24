#!/usr/bin/env bash
# devbrain — capture.sh integration test. Feeds a UserPromptSubmit payload and checks
# that the prompt hook (now delegating to devbrain_lib.py) skips synthetic prompts and
# redacts secrets before writing the log.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; HOOK="$HERE/../hooks/capture.sh"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not installed"; exit 0; }

export DEVBRAIN_DATA="$(mktemp -d)"
export DEVBRAIN_PROJECT="testproj"     # deterministic project key (resolver honors this)
workdir="$(mktemp -d)"
trap 'rm -rf "$DEVBRAIN_DATA" "$workdir"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

mk(){ python3 -c 'import json,sys;print(json.dumps({"prompt":sys.argv[1],"cwd":sys.argv[2],"session_id":"sess1"}))' "$1" "$workdir"; }
run(){ printf '%s' "$1" | bash "$HOOK"; }

# A synthetic (injected) prompt with zero user content -> skipped entirely.
run "$(mk '<system-reminder>
injected host noise, no user authorship</system-reminder>')"
log_after_synthetic="$(find "$DEVBRAIN_DATA" -name '*.md' 2>/dev/null)"
check "synthetic prompt writes nothing" '[ -z "$log_after_synthetic" ]'

# A real prompt carrying a fake secret -> captured, secret redacted.
run "$(mk 'fix the bug; key sk-abcdefghijklmnopqrstuvwxyz0123 here')"
log="$(find "$DEVBRAIN_DATA" -name '*.md' 2>/dev/null | head -1)"
check "real prompt captured"        '[ -n "$log" ] && grep -q "fix the bug" "$log"'
check "no synthetic leaked"          '! grep -q "system-reminder" "$log"'
check "secret redacted"              'grep -q "REDACTED" "$log" && ! grep -q "sk-abcdefghijklmnopqrstuvwxyz0123" "$log"'

# A prompt that merely embeds the user's text inside a harness wrapper is still a real
# prompt: capture it WHOLE (no per-harness special-casing; bias toward keeping).
run "$(mk '<system_instruction>
You are working inside some harness
</system_instruction>

ship the wrapped feature')"
check "wrapped prompt captured whole" 'grep -q "ship the wrapped feature" "$log" && grep -q "system_instruction" "$log"'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
