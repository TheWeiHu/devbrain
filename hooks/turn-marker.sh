#!/usr/bin/env bash
# devbrain/nightshift — turn-completion marker (Stop hook).
#
# Fires when the agent finishes a turn. Appends ONE line per finished turn to the
# file named by $NIGHTSHIFT_MARKER (the driver exports it before launching the worker),
# else <cwd>/.nightshift/turns.log. The driver watches this file's line count to know a
# turn finished — so it NEVER scrapes the interactive TUI pane. The pane is for
# humans to watch; this marker is the machine signal. Out-of-band by design.
#
# Model-free, never blocks, always exit 0 — a signal, not the source of truth.
# Mirrors capture-response.sh's discipline (also a Stop hook).

# Only active for nightshift workers (the orchestrator exports NIGHTSHIFT_MARKER per worker).
# This makes the hook safe to register GLOBALLY: ordinary sessions no-op instantly,
# so we never litter .nightshift/ into unrelated repos, and the marker survives
# /continue's `git stash -u` (a worktree-local hook config would get stashed away).
[ -n "${NIGHTSHIFT_MARKER:-}" ] || exit 0

payload="$(cat 2>/dev/null)" || exit 0

# Read fields via the shared python shim (no jq) — same resolver capture.sh uses.
_lib="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)/devbrain_lib.py"
[ -f "$_lib" ] || _lib="$HOME/.claude/hooks/devbrain_lib.py"
cwd=""; session=""; stop_active=""
if command -v python3 >/dev/null 2>&1 && [ -f "$_lib" ]; then
  ev() { printf '%s' "$payload" | python3 "$_lib" read-event "$1" 2>/dev/null; }
  cwd="$(ev cwd)"; session="$(ev session)"; stop_active="$(ev stop-active)"
fi
[ -n "$cwd" ] || cwd="$PWD"

# Resolve the marker path: driver-chosen if present, else a per-cwd default.
marker="${NIGHTSHIFT_MARKER:-$cwd/.nightshift/turns.log}"
mkdir -p "$(dirname "$marker")" 2>/dev/null || exit 0

# One tab-separated record per turn: UTC ts, session, stop_hook_active flag.
printf '%s\t%s\t%s\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  "${session:-nosession}" \
  "stop_active=${stop_active:-false}" \
  >> "$marker" 2>/dev/null

exit 0
