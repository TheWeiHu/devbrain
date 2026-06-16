#!/usr/bin/env bash
# devbrain — todo.sh test suite. Runs against a throwaway DEVBRAIN_DATA so it never
# touches your real brain. Covers the core verbs and the claim race (only one of N
# parallel claimers may win).
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

echo "== add =="
a="$(t add "high priority security task" -p 90)"
b="$(t add "low priority chore" -p 10)"
c="$(t add "mid task" -p 50)"
check "add returns ids"          '[ -n "$a" ] && [ -n "$b" ] && [ -n "$c" ]'
check "three files on disk"      '[ "$(ls "$DEVBRAIN_DATA/projects/testproj/todo"/*.md | wc -l)" -eq 3 ]'

echo "== priority ordering =="
check "next = highest priority"  '[ "$(t next)" = "$a" ]'
ids="$(t list | sed -n "s/^  \[.*\] \([^ ]*\).*/\1/p" | tr "\n" " ")"
check "list sorted p90,p50,p10"  '[ "$ids" = "$a $c $b " ]'

echo "== claim / done / release =="
t claim "$a" --by tester >/dev/null
check "claim -> taken"           '[ "$(t show "$a" | sed -n "s/^status: //p")" = "taken" ]'
check "claim records claimant"   '[ "$(t show "$a" | sed -n "s/^claimed_by: //p")" = "tester" ]'
check "next skips taken"         '[ "$(t next)" = "$c" ]'
t claim "$a" --by other >/dev/null 2>&1; rc=$?
check "double-claim fails (rc2)" '[ "$rc" -eq 2 ]'
t release "$a" >/dev/null
check "release -> open"          '[ "$(t show "$a" | sed -n "s/^status: //p")" = "open" ]'
t done "$a" >/dev/null
check "done -> done"             '[ "$(t show "$a" | sed -n "s/^status: //p")" = "done" ]'
check "done drops out of next"   '[ "$(t next)" = "$c" ]'
check "list hides done"          'out="$(t list)"; ! grep -q "$a" <<<"$out"'

echo "== claim race (10 parallel claimers, exactly 1 winner) =="
race="$(t add "contended" -p 100)"
win="$(mktemp)"; pids=()
for i in $(seq 1 10); do
  ( t claim "$race" --by "racer$i" >/dev/null 2>&1 && echo "racer$i" >> "$win" ) & pids+=("$!")
done
for p in "${pids[@]}"; do wait "$p"; done
check "exactly one claim winner" '[ "$(wc -l < "$win" | tr -d " ")" -eq 1 ]'
rm -f "$win"
check "contended task is taken"  '[ "$(t show "$race" | sed -n "s/^status: //p")" = "taken" ]'

echo
echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
