#!/usr/bin/env bash
# devbrain — pre-push gate tests. Drives scripts/git-hooks/pre-push with crafted
# pre-push stdin (one "<local ref> <local oid> <remote ref> <remote oid>" line per
# pushed ref) and a STUB gate command, so nothing real runs: we only check WHICH
# pushes trigger the suite and that a red gate blocks the push.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; HOOK="$HERE/git-hooks/pre-push"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
RAN="$TMP/ran"   # the stub touches this so we can tell the gate actually fired

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

ZERO=0000000000000000000000000000000000000000
SHA=1111111111111111111111111111111111111111
# fire <stdin-lines> ; sets $? = hook exit, leaves $RAN iff the gate ran. $GATE = green|red.
fire(){ rm -f "$RAN"; local cmd="touch '$RAN'"; [ "${GATE:-green}" = red ] && cmd="touch '$RAN'; false"
  printf '%b' "$1" | DEVBRAIN_GATE_CMD="$cmd" DEVBRAIN_GATE_SKIP=0 bash "$HOOK" origin git@example:r 2>/dev/null; }

# ── protected branches gate; everything else passes through untouched ──────────
GATE=green
fire "refs/heads/main $SHA refs/heads/main $ZERO\n";           check "push to main gates (green→allow)"       '[ "$?" -eq 0 ] && [ -f "$RAN" ]'
fire "refs/heads/x $SHA refs/heads/nightshift $ZERO\n";        check "push to nightshift gates"               '[ -f "$RAN" ]'
fire "refs/heads/x $SHA refs/heads/feature-x $ZERO\n";         check "push to a feature branch is ungated"    '[ "$?" -eq 0 ] && [ ! -f "$RAN" ]'
fire "refs/heads/x $SHA refs/heads/maintenance $ZERO\n";       check "substring 'main' in 'maintenance' ≠ gated" '[ ! -f "$RAN" ]'

# ── a red gate blocks the push; a deletion needs no gate ──────────────────────
GATE=red
fire "refs/heads/main $SHA refs/heads/main $ZERO\n";           check "red gate blocks push to main (exit 1)"  '[ "$?" -eq 1 ]'
GATE=green
fire "(delete) $ZERO refs/heads/main $SHA\n";                  check "deleting main needs no gate"            '[ "$?" -eq 0 ] && [ ! -f "$RAN" ]'

# ── mixed push: any protected ref in the batch triggers the gate ──────────────
fire "refs/heads/a $SHA refs/heads/feature-a $ZERO\nrefs/heads/b $SHA refs/heads/main $ZERO\n"
check "mixed batch with main gates"            '[ -f "$RAN" ]'

# ── explicit bypass ───────────────────────────────────────────────────────────
rm -f "$RAN"; printf 'refs/heads/main %s refs/heads/main %s\n' "$SHA" "$ZERO" \
  | DEVBRAIN_GATE_SKIP=1 DEVBRAIN_GATE_CMD="touch '$RAN'" bash "$HOOK" origin git@example:r 2>/dev/null
check "DEVBRAIN_GATE_SKIP=1 bypasses the gate"   '[ "$?" -eq 0 ] && [ ! -f "$RAN" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
