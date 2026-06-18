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

# 10. Inline `cd <repo>` attributes the call to the repo it actually queried, not
#     the (non-repo) payload cwd that would otherwise fall into "miscellaneous".
#     Drop the project override so identity is resolved from cwd / inline cd.
unset DEVBRAIN_PROJECT
REPO="$DEVBRAIN_DATA/acme-widget"; PARENT="$DEVBRAIN_DATA/no-repo-parent"
mkdir -p "$REPO" "$PARENT"
git -C "$REPO" init -q
git -C "$REPO" remote add origin https://github.com/Acme/Widget.git
# payload with cwd = the non-repo parent (the orchestrator's real cwd).
pl(){ jq -cn --arg cmd "$1" --arg out "$2" --arg cwd "$PARENT" \
  '{tool_name:"Bash", cwd:$cwd, tool_input:{command:$cmd}, tool_response:{stdout:$out}}'; }
MISC="$DEVBRAIN_DATA/projects/miscellaneous/gbrain-queries.log"
ACME="$DEVBRAIN_DATA/projects/acme__widget/gbrain-queries.log"

pl "cd $REPO && gbrain search \"x\"" "$HITS" | bash "$HOOK"
check "inline cd -> hosted repo, not miscellaneous" '[ -f "$ACME" ] && [ "$(jq -r .project <<<"$(tail -1 "$ACME")")" = "acme__widget" ]'
check "miscellaneous NOT written"                   '[ ! -f "$MISC" ]'

# var="<repo>" (cd "$var" && gbrain …) — the nightshift pattern from the bug.
pl "main=\"$REPO\" (cd \"\$main\" && gbrain get acme__widget/page)" "" | bash "$HOOK"
check "var+subshell cd -> hosted repo"   '[ "$(jq -r .project <<<"$(tail -1 "$ACME")")" = "acme__widget" ]'

# cd to a non-repo dir falls back to the payload cwd identity (miscellaneous here).
pl "cd $PARENT && gbrain search \"y\"" "$HITS" | bash "$HOOK"
check "cd non-repo -> falls back to cwd"  '[ -f "$MISC" ]'

# 13b. A command that only MENTIONS gbrain (here, a filename) but runs no real
#      subcommand must touch nothing — no empty projects/<repo>/ folder — even when
#      cd-routing resolves a hosted repo. Fresh repo so we can assert it stays absent.
NEW="$DEVBRAIN_DATA/zeta-repo"; mkdir -p "$NEW"; git -C "$NEW" init -q
git -C "$NEW" remote add origin https://github.com/Zeta/Repo.git
pl "cd $NEW && cat gbrain-notes.md" "some notes" | bash "$HOOK"
check "mention-only cd creates no folder" '[ ! -e "$DEVBRAIN_DATA/projects/zeta__repo" ]'

# 11. Slug prefix wins outright: no cd at all, cwd is the non-repo parent, but the
#     output names owner__repo/page — the authoritative signal routes it there.
OWNED=$'[0.91] beta__gizmo/page-one -- hit\n[0.40] beta__gizmo/page-two -- hit'
pl 'gbrain search "z"' "$OWNED" | bash "$HOOK"
G="$DEVBRAIN_DATA/projects/beta__gizmo/gbrain-queries.log"
check "slug prefix routes (no cd needed)" '[ -f "$G" ] && [ "$(jq -r .project <<<"$(tail -1 "$G")")" = "beta__gizmo" ]'

# 12. Slug beats an inline cd that points elsewhere — gbrain's own output is truth.
pl "cd $REPO && gbrain search \"z\"" "$OWNED" | bash "$HOOK"
check "slug beats cd target" '[ "$(jq -r .project <<<"$(tail -1 "$G")")" = "beta__gizmo" ]'

# 13. A slug-less line (no owner__repo) does NOT hijack routing; cd/cwd still decide.
NOSLUG=$'[0.91] localpage -- no owner prefix'
pl "cd $REPO && gbrain search \"q\"" "$NOSLUG" | bash "$HOOK"
check "slug-less output ignored -> cd wins" '[ "$(jq -r .project <<<"$(tail -1 "$ACME")")" = "acme__widget" ]'

echo "  --- $pass passed, $fail failed ---"
[ "$fail" -eq 0 ]
