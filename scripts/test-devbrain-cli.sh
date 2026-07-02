#!/usr/bin/env bash
# devbrain — unified `devbrain` dispatcher tests. Drives the front-door command
# (not the underlying scripts) to prove subcommands route + preserve exit codes.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; DB="$HERE/devbrain"
export DEVBRAIN_DATA="$(mktemp -d)"; export DEVBRAIN_PROJECT="testproj"
trap 'rm -rf "$DEVBRAIN_DATA"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }
d(){ bash "$DB" "$@"; }

# meta subcommands
check "version matches VERSION file" '[ "$(d version)" = "$(cat "$HERE/../VERSION")" ]'
check "--version flag works"         '[ "$(d --version)" = "$(cat "$HERE/../VERSION")" ]'
check "help lists subcommands"       'd help | grep "devbrain todo" >/dev/null'
check "help lists queue subcommand"  'd help | grep "devbrain queue" >/dev/null'
check "help lists uninstall"         'd help | grep "devbrain uninstall" >/dev/null'
check "no args prints help"          'd | grep "devbrain todo" >/dev/null'
check "queue --help routes to py"    'd queue --help 2>&1 | grep "kanban" >/dev/null'
check "unknown command exits 1"      'd bogus >/dev/null 2>&1; [ "$?" -eq 1 ]'
check "nightshift routes to script"  'd nightshift help 2>&1 | grep "autonomous overnight loop" >/dev/null'   # only reachable as `devbrain nightshift`

# `devbrain todo` routes to the queue and preserves verbs + exit codes
a="$(d todo add "via dispatcher" -p 80)"
check "todo add returns id"          '[ -n "$a" ]'
check "todo next = the task"         '[ "$(d todo next)" = "$a" ]'
d todo claim "$a" >/dev/null
check "todo claim -> taken"          '[ "$(d todo show "$a" | sed -n "s/^status: //p")" = "taken" ]'
d todo claim "$a" >/dev/null 2>&1; rc=$?
check "todo re-claim exits 2"        '[ "$rc" -eq 2 ]'   # exact exit code survives the dispatch
d todo done "$a" >/dev/null
check "todo done -> done"            '[ "$(d todo show "$a" | sed -n "s/^status: //p")" = "done" ]'

# `devbrain import` routes to import.py (dry-run; just prove it runs + is read-only).
# Use a pristine data dir so prior `todo` writes don't masquerade as import output.
FRESH="$(mktemp -d)"; trap 'rm -rf "$DEVBRAIN_DATA" "$FRESH"' EXIT
check "import dry-run runs"          'd import --data "$FRESH" >/dev/null 2>&1'
check "import wrote nothing (dry)"   '[ -z "$(find "$FRESH" -name "*.md" 2>/dev/null)" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
