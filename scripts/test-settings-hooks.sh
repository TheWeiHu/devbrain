#!/usr/bin/env bash
# devbrain — devbrain_lib.py settings.json hook register/unregister tests (plus the
# session-start-context emitter and the stop-active field). These replaced the `jq`
# merges in install/uninstall/nightshift, so jq is no longer a runtime dependency —
# this test needs ONLY python3 (it never shells out to jq).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; LIB="$HERE/../hooks/devbrain_lib.py"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not installed"; exit 0; }

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
S="$TMP/settings.json"

# read a value from settings.json with python (no jq) — `get '<py-expr on `d`>'`
get(){ python3 - "$S" "$1" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))
print(eval(sys.argv[2]))
PY
}

# Pre-seed an unrelated user hook + a top-level key we must never clobber.
printf '%s' '{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"/usr/bin/other"}]}]}}' > "$S"

python3 "$LIB" register-hook "$S" UserPromptSubmit "" /bin/cap.sh
check "UserPromptSubmit entry added"      '[ "$(get "d[\"hooks\"][\"UserPromptSubmit\"][0][\"hooks\"][0][\"command\"]")" = "/bin/cap.sh" ]'
check "unrelated top-level key preserved" '[ "$(get "d[\"model\"]")" = "opus" ]'
check "pre-existing Stop hook preserved"  '[ "$(get "d[\"hooks\"][\"Stop\"][0][\"hooks\"][0][\"command\"]")" = "/usr/bin/other" ]'

# Idempotent: a second identical register must NOT duplicate.
python3 "$LIB" register-hook "$S" UserPromptSubmit "" /bin/cap.sh
check "register is idempotent"            '[ "$(get "len(d[\"hooks\"][\"UserPromptSubmit\"])")" = "1" ]'

# Matcher is carried through when supplied.
python3 "$LIB" register-hook "$S" PostToolUse Bash /bin/gb.sh
check "matcher recorded"                  '[ "$(get "d[\"hooks\"][\"PostToolUse\"][0][\"matcher\"]")" = "Bash" ]'

# Unregister strips only the named commands, across every event.
python3 "$LIB" unregister-hook "$S" /bin/cap.sh /bin/gb.sh
check "named command removed"             '[ "$(get "len(d[\"hooks\"][\"UserPromptSubmit\"])")" = "0" ]'
check "other command removed"            '[ "$(get "len(d[\"hooks\"][\"PostToolUse\"])")" = "0" ]'
check "unrelated Stop hook survives"      '[ "$(get "d[\"hooks\"][\"Stop\"][0][\"hooks\"][0][\"command\"]")" = "/usr/bin/other" ]'

# A user-GROUPED entry ({devbrain-cmd, their-cmd} in one hooks array) must keep the
# sibling command — unregister strips only the named command, not the whole entry.
printf '%s' '{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/bin/cap.sh"},{"type":"command","command":"/usr/local/mine"}]}]}}' > "$S"
python3 "$LIB" unregister-hook "$S" /bin/cap.sh
check "grouped: entry kept (not dropped)"  '[ "$(get "len(d[\"hooks\"][\"Stop\"])")" = "1" ]'
check "grouped: sibling command survives"  '[ "$(get "d[\"hooks\"][\"Stop\"][0][\"hooks\"][0][\"command\"]")" = "/usr/local/mine" ]'
check "grouped: devbrain command gone"     '[ "$(get "len(d[\"hooks\"][\"Stop\"][0][\"hooks\"])")" = "1" ]'

# A malformed settings.json must ABORT (non-zero), never silently overwrite.
printf '%s' 'not json' > "$S.bad"
if python3 "$LIB" register-hook "$S.bad" Stop "" /bin/x.sh 2>/dev/null; then rc=0; else rc=1; fi
check "malformed settings -> non-zero"    '[ "$rc" -ne 0 ]'
check "malformed settings untouched"      '[ "$(cat "$S.bad")" = "not json" ]'

# session-start-context wraps a tricky message (quotes/backticks) into valid JSON.
ctx="$(printf '%s' 'say `gbrain search "x"` first' | python3 "$LIB" session-start-context)"
check "context is valid JSON w/ message" \
  'printf "%s" "$ctx" | python3 -c "import json,sys; d=json.load(sys.stdin); assert d[\"hookSpecificOutput\"][\"hookEventName\"]==\"SessionStart\"; assert \"gbrain search\" in d[\"hookSpecificOutput\"][\"additionalContext\"]"'

# stop-active field: boolean coerces to true/false, absent -> empty.
check "stop-active true"  '[ "$(printf "%s" "{\"stop_hook_active\":true}"  | python3 "$LIB" read-event stop-active)" = "true" ]'
check "stop-active false" '[ "$(printf "%s" "{\"stop_hook_active\":false}" | python3 "$LIB" read-event stop-active)" = "false" ]'
check "stop-active absent" '[ -z "$(printf "%s" "{}" | python3 "$LIB" read-event stop-active)" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
