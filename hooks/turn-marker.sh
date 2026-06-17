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

cwd=""; session=""; stop_active=""
if command -v jq >/dev/null 2>&1; then
  cwd="$(printf '%s'         "$payload" | jq -r '.cwd // empty'                 2>/dev/null)"
  session="$(printf '%s'     "$payload" | jq -r '.session_id // empty'          2>/dev/null)"
  stop_active="$(printf '%s' "$payload" | jq -r '.stop_hook_active // empty'    2>/dev/null)"
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
