#!/usr/bin/env bash
# devbrain — aggregate test runner. Runs every sibling scripts/test-*.sh (bash) AND
# scripts/test-*.py (python3), reports PASS/FAIL/SKIP per script and a final summary,
# exits non-zero if any failed. Prereq for CI (task 0066).
#
# Skip convention: a test with an unmet external dependency prints a line starting
# with `skip:` and exits 0; the docker clean-room test's own bail messages are also
# recognized. Classification is EXIT-CODE FIRST: a non-zero exit is ALWAYS a FAIL,
# even if the output also contains a skip line — so a test that skips one sub-case
# and then fails an assertion can't masquerade as SKIP and slip a regression past the
# gate.
#
# DEVBRAIN_TEST_SKIP: optional regex of test basenames to skip entirely (reported
# SKIP). The fast per-turn nightshift merge gate sets it to drop the slow docker
# clean-room and browser-dogfood tests; CI runs the full set.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
self="$(basename "${BASH_SOURCE[0]}")"

SKIP_RE='^(skip:|docker required \(not found\)|docker daemon not running)'
SKIP_FILTER="${DEVBRAIN_TEST_SKIP:-}"

pass=(); fail=(); skip=()
run_one() {  # $1 test path ; $2.. runner (bash | python3)
  local t="$1"; shift
  local name; name="$(basename "$t")"
  [ "$name" = "$self" ] && return
  if [ -n "$SKIP_FILTER" ] && printf '%s' "$name" | grep -qE "$SKIP_FILTER"; then
    skip+=("$name"); printf '  SKIP  %s (DEVBRAIN_TEST_SKIP)\n' "$name"; return
  fi
  local out rc
  out="$("$@" "$t" 2>&1)"; rc=$?
  if [ "$rc" -ne 0 ]; then                       # exit code wins: a fail is a fail
    fail+=("$name"); printf '  FAIL  %s (exit %d)\n' "$name" "$rc"
    printf '%s\n' "$out" | sed 's/^/        | /'   # echo the failing output, indented
  elif printf '%s\n' "$out" | grep -qE "$SKIP_RE"; then
    skip+=("$name"); printf '  SKIP  %s\n' "$name"
  else
    pass+=("$name"); printf '  PASS  %s\n' "$name"
  fi
}

shopt -s nullglob
for t in "$HERE"/test-*.sh; do run_one "$t" bash; done
for t in "$HERE"/test-*.py; do run_one "$t" python3; done
shopt -u nullglob

echo
echo "== ${#pass[@]} passed, ${#fail[@]} failed, ${#skip[@]} skipped =="
[ "${#skip[@]}" -gt 0 ] && echo "   skipped: ${skip[*]}"
[ "${#fail[@]}" -eq 0 ]
