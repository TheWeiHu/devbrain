#!/usr/bin/env bash
# devbrain — install.sh global-gbrain-MCP warning tests. Drives the hidden
# `install.sh --warn-gbrain-mcp <conf>` detector (no install side effects) to prove
# it fires only when a GLOBAL `gbrain` MCP server is present. Durable fix for the
# PGLite single-writer lock contention across parallel workspaces (task 0010).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; INSTALL="$HERE/install.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }
warn(){ bash "$INSTALL" --warn-gbrain-mcp "$1"; }   # echoes warning + exit 0 if present, exit 1 if clean

# config WITH a global gbrain MCP server → warns (exit 0) + names the remove command
printf '{"mcpServers":{"gbrain":{"command":"gbrain","args":["serve"]}}}' > "$TMP/has.json"
out_has="$(warn "$TMP/has.json")"; rc_has=$?
check "fires when global gbrain present (exit 0)" '[ "$rc_has" -eq 0 ]'
check "names the fix command"                     'printf "%s" "$out_has" | grep -q "claude mcp remove gbrain"'

# config WITHOUT it → silent (exit 1), no warning text
printf '{"mcpServers":{"other":{"command":"x"}}}' > "$TMP/clean.json"
out_clean="$(warn "$TMP/clean.json")"; rc_clean=$?
check "silent when gbrain absent (exit 1)"  '[ "$rc_clean" -eq 1 ]'
check "no warning text when absent"         '[ -z "$out_clean" ]'

# missing config file → silent (exit 1), never errors
out_missing="$(warn "$TMP/nope.json")"; rc_missing=$?
check "silent on missing config (exit 1)"   '[ "$rc_missing" -eq 1 ]'

# the hidden flag must NOT run the installer (no real wiring touched)
check "detect-only: no 'devbrain install' banner" '! printf "%s" "$out_has" | grep -q "devbrain install"'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
