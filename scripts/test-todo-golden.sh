#!/usr/bin/env bash
# devbrain — todo golden parity. Replays the exact verb sequence from
# scripts/capture-goldens.sh against the GO binary and diffs the normalized
# CLI output + task tree against testdata/golden/ (generated from the legacy
# bash todo.sh). Byte-for-byte: any drift between the port and the frozen
# contract fails here.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ROOT="$HERE/.."
BIN="${DEVBRAIN_BIN:-$ROOT/devbrain}"
GOLD="$ROOT/testdata/golden"
[ -x "$BIN" ] || { echo "skip: devbrain binary not built (go build -o devbrain ./cmd/devbrain)"; exit 0; }
[ -f "$GOLD/todo-cli-output.txt" ] && [ -d "$GOLD/todo-tree" ] || { echo "skip: todo goldens missing under testdata/golden/"; exit 0; }
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

TDATA="$(mktemp -d)"; TMPS="$(mktemp -d)"
trap 'rm -rf "$TDATA" "$TMPS"' EXIT
PRSTUB="$TMPS/prstub"; printf '#!/bin/sh\ncase "$1" in *81*) echo MERGED;; *) echo OPEN;; esac\n' > "$PRSTUB"; chmod +x "$PRSTUB"
TOUT="$TMPS/todo-cli-output.txt"; : > "$TOUT"
tstep() { # label, then todo args; records normalized stdout+stderr+exit code
  local label="$1"; shift
  local rc=0 out
  out="$( DEVBRAIN_DATA="$TDATA" DEVBRAIN_PROJECT=fix__demo DEVBRAIN_PR_STATE_CMD="$PRSTUB" \
          "$BIN" todo "$@" 2>&1 )" || rc=$?
  printf -- '--- %s (rc=%s)\n%s\n' "$label" "$rc" "$out" \
    | sed -E 's/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}Z/<TS>/g' >> "$TOUT"
}
tstep add1        add "Add retry queue" -p 80 -b "Failed jobs should retry with backoff."
tstep add2        add "Fix Mobile FOOTER overlap!!" -p 5
tstep add3        add "Third task" -b "body only"
tstep list-open   list
tstep next        next
tstep claim2      claim 0002-fix-mobile-footer-overlap
tstep claim2-again claim 0002-fix-mobile-footer-overlap
tstep review1     review 0001-add-retry-queue https://github.com/fix/demo/pull/81
tstep hold3       hold 0003-third-task waiting on design
tstep note3       note 0003-third-task gate failed twice
tstep list-all    list all
tstep prio3       prio 0003-third-task 99
tstep edit3       edit 0003-third-task -t "Third task (renamed)" -b "new body line"
tstep approve3    approve 0003-third-task
tstep done2       done 0002-fix-mobile-footer-overlap
tstep release2-done release 0002-fix-mobile-footer-overlap
tstep selfheal    self-heal open taken review
tstep reopen2     reopen 0002-fix-mobile-footer-overlap verified absent
tstep list-final  list all
tstep show1       show 0001-add-retry-queue
printf '%s\n' "--- context3 (rc=0)" >> "$TOUT"
printf 'Synthesized context from the brain.\nSecond line.\n' \
  | DEVBRAIN_DATA="$TDATA" DEVBRAIN_PROJECT=fix__demo "$BIN" todo context 0003-third-task >> "$TOUT"

TREE="$TMPS/todo-tree"; mkdir -p "$TREE"
for f in "$TDATA/projects/fix__demo/todo/"*.md; do
  sed -E "s/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}Z/<TS>/g; s/^claimed_by: .+/claimed_by: <WHO>/" \
    "$f" > "$TREE/$(basename "$f")"
done

check "cli output matches golden byte-for-byte" 'diff -u "$GOLD/todo-cli-output.txt" "$TOUT"'
check "task tree matches golden byte-for-byte"  'diff -ru "$GOLD/todo-tree" "$TREE"'

echo "== todo-golden: $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
