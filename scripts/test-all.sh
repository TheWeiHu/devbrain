#!/usr/bin/env bash
# devbrain — aggregate test runner. Runs every sibling scripts/test-*.sh, reports
# PASS/FAIL/SKIP per script and a final summary, and exits non-zero if any failed.
# Prereq for CI (task 0066).
#
# Skip convention: a test with an unmet external dependency prints a line starting
# with `skip:` and exits 0; the docker clean-room test's own bail messages are also
# recognized. Such tests are reported SKIP, not FAIL. (Patterns are anchored so an
# assertion name merely containing "skip" doesn't get misclassified.)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
self="$(basename "${BASH_SOURCE[0]}")"

SKIP_RE='^(skip:|docker required \(not found\)|docker daemon not running)'

pass=(); fail=(); skip=()
for t in "$HERE"/test-*.sh; do
  name="$(basename "$t")"
  [ "$name" = "$self" ] && continue
  out="$(bash "$t" 2>&1)"; rc=$?
  if printf '%s\n' "$out" | grep -qE "$SKIP_RE"; then
    skip+=("$name"); printf '  SKIP  %s\n' "$name"
  elif [ "$rc" -eq 0 ]; then
    pass+=("$name"); printf '  PASS  %s\n' "$name"
  else
    fail+=("$name"); printf '  FAIL  %s (exit %d)\n' "$name" "$rc"
    printf '%s\n' "$out" | sed 's/^/        | /'   # echo the failing test's output, indented
  fi
done

echo
echo "== ${#pass[@]} passed, ${#fail[@]} failed, ${#skip[@]} skipped =="
[ "${#skip[@]}" -gt 0 ] && echo "   skipped: ${skip[*]}"
[ "${#fail[@]}" -eq 0 ]
