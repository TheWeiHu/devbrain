#!/usr/bin/env bash
# devbrain — todo.sh test suite. Runs against a throwaway DEVBRAIN_DATA so it
# never touches your real brain. Exercises CRUD, priority ordering, dependency
# gating, and the claim race (only one of N parallel claimers may win).
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TODO="$HERE/todo.sh"

export DEVBRAIN_DATA="$(mktemp -d)"
export DEVBRAIN_PROJECT="testproj"
trap 'rm -rf "$DEVBRAIN_DATA"' EXIT

pass=0; fail=0
ok()   { pass=$((pass+1)); echo "  ok   — $1"; }
bad()  { fail=$((fail+1)); echo "  FAIL — $1"; }
check(){ if eval "$2"; then ok "$1"; else bad "$1 [ $2 ]"; fi; }

t() { bash "$TODO" "$@"; }
# Capture-then-grep: piping a producer into `grep -q` lets grep close the pipe on
# first match, killing the producer with SIGPIPE — which pipefail then reports as a
# failed pipeline (flaky). Buffer the output first, then match.
has()  { local out; out="$(t "${@:2}")"; grep -q "$1" <<<"$out"; }
hasnt(){ local out; out="$(t "${@:2}")"; ! grep -q "$1" <<<"$out"; }

echo "== add + list =="
a="$(t add "redact secrets in capture" -p 90 -t security)"
b="$(t add "lower-priority chore" -p 10)"
c="$(t add "mid task" -p 50)"
check "add returns ids"            '[ -n "$a" ] && [ -n "$b" ] && [ -n "$c" ]'
check "three files on disk"        '[ "$(ls "$DEVBRAIN_DATA/projects/testproj/todo"/*.md | wc -l)" -eq 3 ]'

echo "== priority ordering =="
firstid="$(t next --json | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
check "next is highest priority"   '[ "$firstid" = "$a" ]'
# parse ids in listed order from --json (robust; the text table splits on spaces)
listids="$(t list --json | grep -o '"id":"[^"]*"' | sed 's/"id":"//; s/"//' | tr "\n" " ")"
check "list sorted p90,p50,p10"    '[ "$listids" = "$a $c $b " ]'

echo "== claim / done / release =="
t claim "$a" --by tester >/dev/null
st="$(t show "$a" | sed -n "s/^status: //p")"
check "claim sets status=taken"    '[ "$st" = "taken" ]'
cb="$(t show "$a" | sed -n "s/^claimed_by: //p")"
check "claim records claimant"     '[ "$cb" = "tester" ]'
# next must now skip the taken one and return the next-highest (c)
nx="$(t next --json | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
check "next skips taken todo"      '[ "$nx" = "$c" ]'
# double-claim must fail with exit 2
t claim "$a" --by other >/dev/null 2>&1; rc=$?
check "double-claim fails (rc=2)"  '[ "$rc" -eq 2 ]'
t release "$a" >/dev/null
st="$(t show "$a" | sed -n "s/^status: //p")"
check "release -> open"            '[ "$st" = "open" ]'
t done "$a" >/dev/null
st="$(t show "$a" | sed -n "s/^status: //p")"
check "done -> done"               '[ "$st" = "done" ]'
check "list hides done by default" 'hasnt "$a" list'
check "list --all shows done"      'has "$a" list --all'

echo "== dependency gating =="
dep="$(t add "build base" -p 70)"
blk="$(t add "needs base" -p 99 -d "$dep")"
check "blocked shows dependent"    'has "$blk" blocked'
check "ready hides blocked"        'hasnt "$blk" ready'
# even though blk is p99, next must not return it while dep is open
nx="$(t next --json | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
check "next respects deps"         '[ "$nx" != "$blk" ]'
t done "$dep" >/dev/null
check "ready unblocks after dep"   'has "$blk" ready'
nx="$(t next --json | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
check "next returns unblocked p99" '[ "$nx" = "$blk" ]'

echo "== claim race (10 parallel claimers, exactly 1 winner) =="
race="$(t add "contended" -p 100)"
winfile="$(mktemp)"
pids=()
for i in $(seq 1 10); do
  ( if t claim "$race" --by "racer$i" >/dev/null 2>&1; then echo "racer$i" >> "$winfile"; fi ) &
  pids+=("$!")
done
for p in "${pids[@]}"; do wait "$p"; done
winners="$(wc -l < "$winfile" | tr -d ' ')"
rm -f "$winfile"
check "exactly one claim winner"   '[ "$winners" -eq 1 ]'
st="$(t show "$race" | sed -n "s/^status: //p")"
check "contended todo is taken"    '[ "$st" = "taken" ]'

echo
echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
