#!/usr/bin/env bash
# devbrain — pre-push gate tests. Drives scripts/git-hooks/pre-push inside a real
# throwaway repo (the hook now stages the pushed OID in a temp worktree, so it needs
# real commits) with crafted pre-push stdin and a STUB gate command. We check WHICH
# pushes trigger the suite, that a red gate blocks, and that an unstageable OID fails.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; HOOK="$HERE/git-hooks/pre-push"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
RAN="$TMP/ran"   # the stub touches this (absolute path) so we can tell the gate fired

REPO="$TMP/repo"; git init -q "$REPO"
git -C "$REPO" config user.email t@t; git -C "$REPO" config user.name t
echo a > "$REPO/f"; git -C "$REPO" add f; git -C "$REPO" commit -qm c1
OID="$(git -C "$REPO" rev-parse HEAD)"
ZERO=0000000000000000000000000000000000000000
GONE=ffffffffffffffffffffffffffffffffffffffff   # valid shape, not a real object

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }
# fire <stdin> : runs the hook with CWD in the repo; $? = hook exit, $RAN present iff gate ran. $GATE=green|red.
fire(){ rm -f "$RAN"; local cmd="touch '$RAN'"; [ "${GATE:-green}" = red ] && cmd="touch '$RAN'; false"
  ( cd "$REPO" && printf '%b' "$1" | DEVBRAIN_GATE_CMD="$cmd" DEVBRAIN_GATE_SKIP=0 bash "$HOOK" origin git@x:r ) 2>/dev/null; }

# ── protected branches gate the pushed OID; everything else passes through ─────
GATE=green
fire "refs/heads/main $OID refs/heads/main $ZERO\n";           check "push to main gates (green→allow)"        '[ "$?" -eq 0 ] && [ -f "$RAN" ]'
fire "refs/heads/x $OID refs/heads/nightshift $ZERO\n";        check "push to nightshift gates"                '[ "$?" -eq 0 ] && [ -f "$RAN" ]'
fire "refs/heads/x $OID refs/heads/feature-x $ZERO\n";         check "push to a feature branch is ungated"     '[ "$?" -eq 0 ] && [ ! -f "$RAN" ]'
fire "refs/heads/x $OID refs/heads/maintenance $ZERO\n";       check "substring 'main' in 'maintenance' ≠ gated" '[ "$?" -eq 0 ] && [ ! -f "$RAN" ]'

# ── red gate blocks; an unstageable OID is treated as a failure, not skipped ───
GATE=red
fire "refs/heads/main $OID refs/heads/main $ZERO\n";           check "red gate blocks push to main (exit 1)"   '[ "$?" -eq 1 ]'
GATE=green
fire "refs/heads/main $GONE refs/heads/main $ZERO\n";          check "unstageable OID blocks push (exit 1)"    '[ "$?" -eq 1 ]'
fire "(delete) $ZERO refs/heads/main $OID\n";                  check "deleting main needs no gate"             '[ "$?" -eq 0 ] && [ ! -f "$RAN" ]'

# ── mixed push: any protected ref in the batch triggers the gate ──────────────
fire "refs/heads/a $OID refs/heads/feature-a $ZERO\nrefs/heads/b $OID refs/heads/main $ZERO\n"
check "mixed batch with main gates"            '[ "$?" -eq 0 ] && [ -f "$RAN" ]'

# ── same commit to both protected refs gates ONCE (dedup) ─────────────────────
RUNS="$TMP/runs"; : > "$RUNS"
( cd "$REPO" && printf 'refs/heads/main %s refs/heads/main %s\nrefs/heads/main %s refs/heads/nightshift %s\n' "$OID" "$ZERO" "$OID" "$ZERO" \
  | DEVBRAIN_GATE_CMD="echo x >> '$RUNS'" bash "$HOOK" origin git@x:r ) 2>/dev/null
check "same OID to main+nightshift gates once" '[ "$(wc -l < "$RUNS" | tr -d " ")" -eq 1 ]'

# ── explicit bypass ───────────────────────────────────────────────────────────
rm -f "$RAN"; ( cd "$REPO" && printf 'refs/heads/main %s refs/heads/main %s\n' "$OID" "$ZERO" \
  | DEVBRAIN_GATE_SKIP=1 DEVBRAIN_GATE_CMD="touch '$RAN'" bash "$HOOK" origin git@x:r ) 2>/dev/null
check "DEVBRAIN_GATE_SKIP=1 bypasses the gate"   '[ "$?" -eq 0 ] && [ ! -f "$RAN" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
