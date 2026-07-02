#!/usr/bin/env bash
# devbrain — todo.sh tests. Runs against a throwaway DEVBRAIN_DATA.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; TODO="$HERE/todo.sh"
export DEVBRAIN_DATA="$(mktemp -d)"; export DEVBRAIN_PROJECT="testproj"
trap 'rm -rf "$DEVBRAIN_DATA"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }
t(){ bash "$TODO" "$@"; }

a="$(t add "high priority task" -p 90)"; b="$(t add "low chore" -p 10)"; c="$(t add "mid task" -p 50)"
check "add returns ids"        '[ -n "$a" ] && [ -n "$b" ] && [ -n "$c" ]'
check "next = highest priority" '[ "$(t next)" = "$a" ]'
ids="$(t list | sed -n "s/^  \[.*\] \([^ ]*\).*/\1/p" | tr "\n" " ")"
check "list sorted p90,p50,p10" '[ "$ids" = "$a $c $b " ]'

# DEVBRAIN_TODO_ONLY — fixed-set scoping (nightshift --only). a>c>b by priority; all open here.
check "ONLY slug next = top in set"  '[ "$(DEVBRAIN_TODO_ONLY="$c,$b" t next)" = "$c" ]'
check "ONLY scopes list to set"      'out="$(DEVBRAIN_TODO_ONLY="$c,$b" t list)"; grep -q "$c" <<<"$out" && grep -q "$b" <<<"$out" && ! grep -q "$a" <<<"$out"'
check "ONLY bare 4-digit num works"  '[ "$(DEVBRAIN_TODO_ONLY="${b%%-*}" t next)" = "$b" ]'
check "ONLY space-separated works"   '[ "$(DEVBRAIN_TODO_ONLY="$b $c" t next)" = "$c" ]'
check "ONLY no-match -> empty next"  '[ -z "$(DEVBRAIN_TODO_ONLY=9999 t next)" ]'
check "ONLY empty == unfiltered"     '[ "$(DEVBRAIN_TODO_ONLY= t next)" = "$a" ]'

t claim "$a" >/dev/null
check "claim -> taken"          '[ "$(t show "$a" | sed -n "s/^status: //p")" = "taken" ]'
check "claim stamps claimed_at" '[ -n "$(t show "$a" | sed -n "s/^claimed_at: //p")" ]'
check "next skips taken"        '[ "$(t next)" = "$c" ]'
t claim "$a" >/dev/null 2>&1; rc=$?
check "re-claim taken fails(2)" '[ "$rc" -eq 2 ]'
t release "$a" >/dev/null
check "release -> open"         '[ "$(t show "$a" | sed -n "s/^status: //p")" = "open" ]'
check "release clears claimed_at" '[ -z "$(t show "$a" | sed -n "s/^claimed_at: //p")" ]'
t done "$a" >/dev/null
check "done -> done"            '[ "$(t show "$a" | sed -n "s/^status: //p")" = "done" ]'
check "done stamps done_at"     '[ -n "$(t show "$a" | sed -n "s/^done_at: //p")" ]'
check "done_at is UTC ISO-8601" 't show "$a" | sed -n "s/^done_at: //p" | grep -qE "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"'
check "open task has no done_at" '[ -z "$(t show "$c" | sed -n "s/^done_at: //p")" ]'
check "done drops from next"    '[ "$(t next)" = "$c" ]'
check "list hides done"         'out="$(t list)"; ! grep -q "$a" <<<"$out"'
# `done` is terminal: release must NOT reopen it (nightshift watchdog-requeue race)
t release "$a" >/dev/null 2>&1
check "release won't reopen done" '[ "$(t show "$a" | sed -n "s/^status: //p")" = "done" ]'

# review status: open->taken->review->done, records pr, hidden from next/list
t claim "$c" >/dev/null
t review "$c" 42 >/dev/null
check "review -> review"         '[ "$(t show "$c" | sed -n "s/^status: //p")" = "review" ]'
check "review records pr"        '[ "$(t show "$c" | sed -n "s/^pr: //p")" = "42" ]'
check "next skips review"        '[ "$(t next)" = "$b" ]'
check "list hides review"        'out="$(t list)"; ! grep -q "$c" <<<"$out"'
t release "$c" >/dev/null
check "release review -> open"   '[ "$(t show "$c" | sed -n "s/^status: //p")" = "open" ]'

# set_field inserts pr: on a task created without it (backward compat)
old="$(t add "legacy task" -p 5)"
legacy_file="$DEVBRAIN_DATA/projects/"*"/todo/$old.md"
eval "sed -i.bak '/^pr:/d' $legacy_file" 2>/dev/null || true
t review "$old" 7 >/dev/null
check "review adds pr if missing" '[ "$(t show "$old" | sed -n "s/^pr: //p")" = "7" ]'

# list [status] — filter by status. State now: a=done, b=open, c=open, old=review.
t review "$b" 99 >/dev/null   # put b into review so we have a known review row
check "list (default) = open only" 'out="$(t list)"; grep -q "$c" <<<"$out" && ! grep -q "$b" <<<"$out" && ! grep -q "$a" <<<"$out"'
check "list review = review only"  'out="$(t list review)"; grep -q "$b" <<<"$out" && ! grep -q "$c" <<<"$out"'
check "list review shows status"   't list review | grep "$b" | grep "review" >/dev/null'
check "list done = done only"      'out="$(t list done)"; grep -q "$a" <<<"$out" && ! grep -q "$c" <<<"$out"'
check "list all = every status"    'out="$(t list all)"; grep -q "$a" <<<"$out" && grep -q "$b" <<<"$out" && grep -q "$c" <<<"$out"'
check "list bad status fails"      '! t list bogus >/dev/null 2>&1'
check "next still open-only"       '[ "$(t next)" = "$c" ]'

# context: attach a synthesized "## Context" body section from stdin, idempotently
printf 'line one\nline two\n' | t context "$b" >/dev/null
check "context adds ## Context"      't show "$b" | grep -q "^## Context (synthesized "'
check "context keeps body lines"     't show "$b" | grep -q "^line two$"'
printf 'fresh only\n' | t context "$b" >/dev/null
check "context replaces prior block" '[ "$(t show "$b" | grep -c "^## Context (synthesized ")" -eq 1 ]'
check "context drops old lines"      't show "$b" | grep -q "^fresh only$" && ! t show "$b" | grep -q "^line two$"'
check "context needs stdin body"     '! printf "" | t context "$b" >/dev/null 2>&1'

# self-heal: close open/taken tasks whose recorded PR has merged (zombie sweep).
# Fake the PR-state lookup (DEVBRAIN_PR_STATE_CMD) so the test stays offline: any
# pr ref containing "MERGED" reports merged, everything else open.
fake="$DEVBRAIN_DATA/fake-pr-state"
printf '#!/usr/bin/env bash\ncase "$1" in *MERGED*) echo MERGED;; *) echo OPEN;; esac\n' > "$fake"
chmod +x "$fake"; export DEVBRAIN_PR_STATE_CMD="$fake"
# A genuine zombie = a task left open/taken while carrying a MERGED pr: by a path that
# bypassed `todo done` (e.g. a manually-merged PR). Inject pr: directly — `release` now
# intentionally CLEARS pr (so the release path can't create a zombie; see fix below).
TD="$DEVBRAIN_DATA/projects/$DEVBRAIN_PROJECT/todo"
setpr(){ sed -i.bak "s|^pr:.*|pr: $2|" "$TD/$1.md" && rm -f "$TD/$1.md.bak"; }
z1="$(t add "merged open zombie")"; setpr "$z1" "PR-MERGED-1"
z2="$(t add "open with live PR")";  setpr "$z2" "PR-OPEN-2"
z3="$(t add "open no PR")"
z4="$(t add "merged taken zombie")"; setpr "$z4" "PR-MERGED-4"; t claim "$z4" >/dev/null
t self-heal >/dev/null
check "self-heal closes merged open"  '[ "$(t show "$z1" | sed -n "s/^status: //p")" = "done" ]'
check "self-heal stamps done_at"      '[ -n "$(t show "$z1" | sed -n "s/^done_at: //p")" ]'
check "self-heal closes merged taken" '[ "$(t show "$z4" | sed -n "s/^status: //p")" = "done" ]'
check "self-heal leaves live PR open" '[ "$(t show "$z2" | sed -n "s/^status: //p")" = "open" ]'
check "self-heal ignores no-pr task"  '[ "$(t show "$z3" | sed -n "s/^status: //p")" = "open" ]'
# Fix (finding #6): release/approve clear pr+done_at so a reopened task can't be re-zombied.
zr="$(t add "reopen clears pr")"; t review "$zr" "PR-MERGED-R" >/dev/null; t release "$zr" >/dev/null
check "release clears pr"             '[ -z "$(t show "$zr" | sed -n "s/^pr: //p")" ]'
zh="$(t add "release clears hold note")"; t hold "$zh" "parked for some reason" >/dev/null; t release "$zh" >/dev/null
check "release clears reason"         '[ -z "$(t show "$zh" | sed -n "s/^reason: //p" | head -1)" ]'
t self-heal >/dev/null
check "self-heal skips reopened task" '[ "$(t show "$zr" | sed -n "s/^status: //p")" = "open" ]'

echo "== derived git status (nightshift mode) =="
derive_project="deriveproj"
dt(){ DEVBRAIN_PROJECT="$derive_project" bash "$TODO" "$@"; }
ddone="$(dt add "derived done")"
dreview="$(dt add "derived review")"
dreset="$(dt add "derived reset")"; dt done "$dreset" >/dev/null
dtaken="$(dt add "derived taken")"; dt claim "$dtaken" >/dev/null
dheld="$(dt add "derived held")"; dt hold "$dheld" "human hold" >/dev/null
REM="$DEVBRAIN_DATA/derive-origin.git"; REPO="$DEVBRAIN_DATA/derive-repo"; GIT="git -c user.email=a@b.c -c user.name=t"
git init -q --bare "$REM"
git init -q "$REPO"; git -C "$REPO" remote add origin "$REM"
( cd "$REPO" && echo base > f && $GIT add f && $GIT commit -qm init && git push -q origin HEAD:main )
git -C "$REPO" checkout -q -b nightshift
$GIT -C "$REPO" commit --allow-empty -qm "nightshift: merge todo/$ddone into nightshift"
git -C "$REPO" push -q origin nightshift
git -C "$REPO" checkout -q -B "todo/$dreview" origin/main
$GIT -C "$REPO" commit --allow-empty -qm "work $dreview"
git -C "$REPO" push -q origin "todo/$dreview"
git -C "$REPO" checkout -q main
dlist(){ ( cd "$REPO" && DEVBRAIN_PROJECT="$derive_project" DEVBRAIN_TODO_DERIVE_GIT=1 bash "$TODO" "$@" ); }
check "derived mode treats nightshift merge as done"       'dlist list done | grep "$ddone" >/dev/null'
check "derived mode treats remote todo branch as review"   'dlist list review | grep "$dreview" >/dev/null'
check "derived mode reopens done with no merge evidence"   'dlist list | grep "$dreset" >/dev/null'
check "derived mode treats fresh claim lease as taken"     'dlist list taken | grep "$dtaken" >/dev/null'
check "derived mode keeps held stored state authoritative" 'dlist list held | grep "$dheld" >/dev/null'
check "normal mode still trusts stored done"               'DEVBRAIN_PROJECT="$derive_project" bash "$TODO" list done | grep "$dreset" >/dev/null'

# ── derive fetches ONCE per invocation, and DEVBRAIN_TODO_FETCH_TTL suppresses repeats ──
# effective_status runs in a $(...) subshell, so derive_init's DERIVE_READY guard can't
# persist from there — unprimed, derive re-ran `git fetch` once PER TASK (N network
# round-trips per `list`). rows() primes it; a git shim counts the actual fetch spawns.
REALGIT="$(command -v git)"; GITLOG="$DEVBRAIN_DATA/gitcalls"; mkdir -p "$DEVBRAIN_DATA/bin"
printf '#!/usr/bin/env bash\necho "$1" >> "%s"\nexec "%s" "$@"\n' "$GITLOG" "$REALGIT" > "$DEVBRAIN_DATA/bin/git"
chmod +x "$DEVBRAIN_DATA/bin/git"
: > "$GITLOG"; PATH="$DEVBRAIN_DATA/bin:$PATH" dlist list all >/dev/null
check "derive runs exactly one fetch per list" '[ "$(grep -cx fetch "$GITLOG")" = 1 ]'
: > "$GITLOG"; PATH="$DEVBRAIN_DATA/bin:$PATH" DEVBRAIN_TODO_FETCH_TTL=3600 dlist list all >/dev/null
check "fresh FETCH_HEAD + TTL skips the fetch"  '[ "$(grep -cx fetch "$GITLOG")" = 0 ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
