#!/usr/bin/env bash
# devbrain — capture-gbrain.sh tests. Feeds synthetic PostToolUse payloads on
# stdin (no real gbrain or brain needed) and checks the directional trace it
# writes: one line per command, {ts, project, cmd, modes, hits, slugs}.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; HOOK="$HERE/../hooks/capture-gbrain.sh"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not installed"; exit 0; }
export DEVBRAIN_DATA="$(mktemp -d)"; export DEVBRAIN_PROJECT="testproj"
trap 'rm -rf "$DEVBRAIN_DATA"' EXIT
LOG="$DEVBRAIN_DATA/projects/testproj/gbrain-queries.log"
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# jq-free read of one field from a JSON line on stdin (the trace is {ts,project,cmd,
# modes,hits,slugs}). `jget .modes -c` -> compact JSON ; `jget .cmd` -> raw scalar ;
# `jget .slugs --join` -> comma-joined array.
jget(){ python3 -c '
import json,sys
v=json.load(sys.stdin).get(sys.argv[1].lstrip("."))
mode=sys.argv[2] if len(sys.argv)>2 else ""
if   mode=="--join": print(",".join(v or []))
elif mode=="-c":     print(json.dumps(v,separators=(",",":")))
else:                print(v if isinstance(v,str) else (v if isinstance(v,(int,float)) else json.dumps(v)))
' "$@"; }

# Build a PostToolUse payload: $1=command, $2=stdout, $3=tool_name(default Bash).
payload(){ python3 -c 'import json,sys;print(json.dumps({"tool_name":sys.argv[3],"cwd":".","tool_input":{"command":sys.argv[1]},"tool_response":{"stdout":sys.argv[2]}}))' "$1" "$2" "${3:-Bash}"; }
fire(){ payload "$@" | bash "$HOOK"; }

HITS=$'[0.86] testproj/alpha -- first hit\n[0.40] testproj/beta -- second hit'

# 1. A single query logs one line: modes, hits, slugs, project, and a cmd snippet.
fire 'gbrain query "todo lifecycle"' "$HITS"
line="$(tail -1 "$LOG")"
check "one line written"   '[ "$(wc -l < "$LOG")" -eq 1 ]'
check "modes=[query]"      '[ "$(jget .modes -c <<<"$line")" = "[\"query\"]" ]'
check "hits=2"             '[ "$(jget .hits <<<"$line")" = "2" ]'
check "slugs parsed"       '[ "$(jget .slugs --join <<<"$line")" = "testproj/alpha,testproj/beta" ]'
check "project key"        '[ "$(jget .project <<<"$line")" = "testproj" ]'
check "cmd snippet kept"   'jget .cmd <<<"$line" | grep -q "todo lifecycle"'

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
check "loop modes=[search]" '[ "$(jget .modes -c <<<"$loop")" = "[\"search\"]" ]'
check "loop cmd has topics" 'c="$(jget .cmd <<<"$loop")"; grep -q "todo queue" <<<"$c" && grep -q "concurrency" <<<"$c"'

# 5. "gbrain <word>" inside a string is filtered by the whitelist (no fake mode).
fire 't="log gbrain queries"; gbrain search "$t"' "$HITS"
fp="$(tail -1 "$LOG")"
check "whitelist drops 'queries'" '[ "$(jget .modes -c <<<"$fp")" = "[\"search\"]" ]'

# 6. A filename containing gbrain (no real subcommand) logs NOTHING.
fire 'cat hooks/capture-gbrain.sh | head -1' "#!/usr/bin/env bash"
check "filename ref ignored" '[ "$(wc -l < "$LOG")" -eq 3 ]'

# 7. Write subcommands are logged too; no result lines -> hits 0.
fire 'printf hi | gbrain put theproj/page' ""
w="$(tail -1 "$LOG")"
check "put logged"         '[ "$(jget .modes -c <<<"$w")" = "[\"put\"]" ]'
check "put hits 0"         '[ "$(jget .hits <<<"$w")" = "0" ]'

# 8. Path-prefixed binary still matches.
fire '/home/u/.bun/bin/gbrain ask "deep question"' "$HITS"
check "path-prefixed matched" '[ "$(jget .modes -c <<<"$(tail -1 "$LOG")")" = "[\"ask\"]" ]'

# 9. A secret in the command is redacted out of the logged cmd snippet.
fire 'gbrain search "key sk-abcdefghijklmnopqrstuvwxyz0123"' ""
c="$(jget .cmd <<<"$(tail -1 "$LOG")")"
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
pl(){ python3 -c 'import json,sys;print(json.dumps({"tool_name":"Bash","cwd":sys.argv[3],"tool_input":{"command":sys.argv[1]},"tool_response":{"stdout":sys.argv[2]}}))' "$1" "$2" "$PARENT"; }
MISC="$DEVBRAIN_DATA/projects/miscellaneous/gbrain-queries.log"
ACME="$DEVBRAIN_DATA/projects/acme__widget/gbrain-queries.log"

pl "cd $REPO && gbrain search \"x\"" "$HITS" | bash "$HOOK"
check "inline cd -> hosted repo, not miscellaneous" '[ -f "$ACME" ] && [ "$(jget .project <<<"$(tail -1 "$ACME")")" = "acme__widget" ]'
check "miscellaneous NOT written"                   '[ ! -f "$MISC" ]'

# var="<repo>" (cd "$var" && gbrain …) — the nightshift pattern from the bug.
pl "main=\"$REPO\" (cd \"\$main\" && gbrain get acme__widget/page)" "" | bash "$HOOK"
check "var+subshell cd -> hosted repo"   '[ "$(jget .project <<<"$(tail -1 "$ACME")")" = "acme__widget" ]'

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
check "slug prefix routes (no cd needed)" '[ -f "$G" ] && [ "$(jget .project <<<"$(tail -1 "$G")")" = "beta__gizmo" ]'

# 12. Slug beats an inline cd that points elsewhere — gbrain's own output is truth.
pl "cd $REPO && gbrain search \"z\"" "$OWNED" | bash "$HOOK"
check "slug beats cd target" '[ "$(jget .project <<<"$(tail -1 "$G")")" = "beta__gizmo" ]'

# 13. A slug-less line (no owner__repo) does NOT hijack routing; cd/cwd still decide.
NOSLUG=$'[0.91] localpage -- no owner prefix'
pl "cd $REPO && gbrain search \"q\"" "$NOSLUG" | bash "$HOOK"
check "slug-less output ignored -> cd wins" '[ "$(jget .project <<<"$(tail -1 "$ACME")")" = "acme__widget" ]'

echo "  --- $pass passed, $fail failed ---"
[ "$fail" -eq 0 ]
