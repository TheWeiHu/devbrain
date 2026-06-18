#!/usr/bin/env bash
# devbrain — capture-gbrain.sh tests. Feeds synthetic PostToolUse payloads on
# stdin (no real gbrain or brain needed) and checks the directional trace it
# writes: one line per command, {ts, project, cmd, modes, hits, slugs}.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; HOOK="$HERE/../hooks/capture-gbrain.sh"
export DEVBRAIN_DATA="$(mktemp -d)"; export DEVBRAIN_PROJECT="testproj"
trap 'rm -rf "$DEVBRAIN_DATA"' EXIT
LOG="$DEVBRAIN_DATA/projects/testproj/gbrain-queries.log"
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# Build a PostToolUse payload: $1=command, $2=stdout, $3=tool_name(default Bash).
payload(){ jq -cn --arg cmd "$1" --arg out "$2" --arg tool "${3:-Bash}" \
  '{tool_name:$tool, cwd:".", tool_input:{command:$cmd}, tool_response:{stdout:$out}}'; }
fire(){ payload "$@" | bash "$HOOK"; }

HITS=$'[0.86] testproj/alpha -- first hit\n[0.40] testproj/beta -- second hit'

# 1. A single query logs one line: modes, hits, slugs, project, and a cmd snippet.
fire 'gbrain query "todo lifecycle"' "$HITS"
line="$(tail -1 "$LOG")"
check "one line written"   '[ "$(wc -l < "$LOG")" -eq 1 ]'
check "modes=[query]"      '[ "$(jq -rc .modes <<<"$line")" = "[\"query\"]" ]'
check "hits=2"             '[ "$(jq -r .hits <<<"$line")" = "2" ]'
check "slugs parsed"       '[ "$(jq -r ".slugs|join(\",\")" <<<"$line")" = "testproj/alpha,testproj/beta" ]'
check "project key"        '[ "$(jq -r .project <<<"$line")" = "testproj" ]'
check "cmd snippet kept"   'jq -r .cmd <<<"$line" | grep -q "todo lifecycle"'

# 2. A non-Bash tool is ignored.
fire 'gbrain query "ignored"' "$HITS" "Read"
check "non-Bash ignored"   '[ "$(wc -l < "$LOG")" -eq 1 ]'

# 3. A Bash command with no gbrain is ignored.
fire 'ls -la && echo hi' "stuff"
check "no-gbrain ignored"  '[ "$(wc -l < "$LOG")" -eq 1 ]'

# 4. A loop logs ONE line; the cmd snippet carries both topics; modes deduped.
fire 'for q in "todo queue" "concurrency"; do gbrain search "$q"; done' "$HITS"
loop="$(tail -1 "$LOG")"
check "loop -> one line"   '[ "$(wc -l < "$LOG")" -eq 2 ]'
check "loop modes=[search]" '[ "$(jq -rc .modes <<<"$loop")" = "[\"search\"]" ]'
check "loop cmd has topics" 'c="$(jq -r .cmd <<<"$loop")"; grep -q "todo queue" <<<"$c" && grep -q "concurrency" <<<"$c"'

# 5. "gbrain <word>" inside a string is filtered by the whitelist (no fake mode).
fire 't="log gbrain queries"; gbrain search "$t"' "$HITS"
fp="$(tail -1 "$LOG")"
check "whitelist drops 'queries'" '[ "$(jq -rc .modes <<<"$fp")" = "[\"search\"]" ]'

# 6. A filename containing gbrain (no real subcommand) logs NOTHING.
fire 'cat hooks/capture-gbrain.sh | head -1' "#!/usr/bin/env bash"
check "filename ref ignored" '[ "$(wc -l < "$LOG")" -eq 3 ]'

# 7. Write subcommands are logged too; no result lines -> hits 0.
fire 'printf hi | gbrain put theproj/page' ""
w="$(tail -1 "$LOG")"
check "put logged"         '[ "$(jq -rc .modes <<<"$w")" = "[\"put\"]" ]'
check "put hits 0"         '[ "$(jq -r .hits <<<"$w")" = "0" ]'

# 8. Path-prefixed binary still matches.
fire '/home/u/.bun/bin/gbrain ask "deep question"' "$HITS"
check "path-prefixed matched" '[ "$(jq -rc .modes <<<"$(tail -1 "$LOG")")" = "[\"ask\"]" ]'

# 9. A secret in the command is redacted out of the logged cmd snippet.
fire 'gbrain search "key sk-abcdefghijklmnopqrstuvwxyz0123"' ""
c="$(jq -r .cmd <<<"$(tail -1 "$LOG")")"
check "cmd snippet redacted" 'grep -q REDACTED <<<"$c" && ! grep -q "sk-abcdefghijklmnopqrstuvwxyz0123" <<<"$c"'

echo "  --- $pass passed, $fail failed ---"
[ "$fail" -eq 0 ]
