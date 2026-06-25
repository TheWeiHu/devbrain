#!/usr/bin/env bash
# devbrain — aggregate test runner. Runs every sibling scripts/test-*.sh (bash) AND
# scripts/test-*.py (python3), reports PASS/FAIL/SKIP per script and a final summary,
# exits non-zero if any failed. Prereq for CI.
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
#
# DEVBRAIN_TEST_REQUIRE: optional regex of test basenames that MUST actually run+pass —
# a matching test that would otherwise be SKIPPED (its own `skip:`/`docker …` bail OR an
# explicit DEVBRAIN_TEST_SKIP) is upgraded to a FAIL instead. CI sets this to the docker
# clean-room install test so a runner without docker can't silently green-light an
# install regression (task 0032). The nightshift fast gate leaves it unset, so it keeps
# skipping docker for speed — REQUIRE and SKIP never both name docker in practice.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
self="$(basename "${BASH_SOURCE[0]}")"

SKIP_RE='^(skip:|docker required \(not found\)|docker daemon not running)'
SKIP_FILTER="${DEVBRAIN_TEST_SKIP:-}"
REQUIRE_FILTER="${DEVBRAIN_TEST_REQUIRE:-}"
required() { [ -n "$REQUIRE_FILTER" ] && printf '%s' "$1" | grep -qE "$REQUIRE_FILTER"; }

pass=(); fail=(); skip=()
run_one() {  # $1 test path ; $2.. runner (bash | python3)
  local t="$1"; shift
  local name; name="$(basename "$t")"
  [ "$name" = "$self" ] && return
  if [ -n "$SKIP_FILTER" ] && printf '%s' "$name" | grep -qE "$SKIP_FILTER"; then
    if required "$name"; then                      # required wins over an explicit skip
      fail+=("$name"); printf '  FAIL  %s (DEVBRAIN_TEST_REQUIRE: skip-filtered but required)\n' "$name"; return
    fi
    skip+=("$name"); printf '  SKIP  %s (DEVBRAIN_TEST_SKIP)\n' "$name"; return
  fi
  local out rc
  out="$("$@" "$t" 2>&1)"; rc=$?
  if [ "$rc" -ne 0 ]; then                       # exit code wins: a fail is a fail
    fail+=("$name"); printf '  FAIL  %s (exit %d)\n' "$name" "$rc"
    printf '%s\n' "$out" | sed 's/^/        | /'   # echo the failing output, indented
  elif printf '%s\n' "$out" | grep -qE "$SKIP_RE"; then
    if required "$name"; then                      # a required test that bails is a regression
      fail+=("$name"); printf '  FAIL  %s (DEVBRAIN_TEST_REQUIRE: must run, but skipped)\n' "$name"
      printf '%s\n' "$out" | sed 's/^/        | /'
    else
      skip+=("$name"); printf '  SKIP  %s\n' "$name"
    fi
  else
    pass+=("$name"); printf '  PASS  %s\n' "$name"
  fi
}

shopt -s nullglob
all=(); for t in "$HERE"/test-*.sh "$HERE"/test-*.py; do all+=("$(basename "$t")"); done
for t in "$HERE"/test-*.sh; do run_one "$t" bash; done
for t in "$HERE"/test-*.py; do run_one "$t" python3; done
shopt -u nullglob

# A required test that no longer exists (renamed/deleted) would let REQUIRE match nothing
# and pass green — defeating the guard. Fail loudly if REQUIRE names a test we never saw.
if [ -n "$REQUIRE_FILTER" ]; then
  matched=0
  for name in "${all[@]}"; do required "$name" && matched=1; done
  if [ "$matched" -eq 0 ]; then
    fail+=("DEVBRAIN_TEST_REQUIRE"); printf '  FAIL  no test matches DEVBRAIN_TEST_REQUIRE=%s (renamed/deleted?)\n' "$REQUIRE_FILTER"
  fi
fi

echo
echo "== ${#pass[@]} passed, ${#fail[@]} failed, ${#skip[@]} skipped =="
[ "${#skip[@]}" -gt 0 ] && echo "   skipped: ${skip[*]}"
[ "${#fail[@]}" -eq 0 ]
