#!/usr/bin/env bash
# devbrain — gbrain call wrapper that reaps orphaned `gbrain serve` daemons.
#
# Why: gbrain's MCP server is stdio, and the SDK's StdioServerTransport has no
# stdin-EOF listener — so when a Conductor workspace closes, its `gbrain serve`
# child is NOT told the client left. It gets reparented to launchd (PID 1) and
# runs forever, holding a PGLite connection and periodically grabbing the
# single-writer lock. Those orphans are what starve everyone else's writes.
#
# We can't fix that in gbrain (out of scope), so devbrain cleans up at the moment
# it matters: every time *we* shell out to gbrain, first kill any `gbrain serve`
# whose parent is PID 1. That heuristic is deliberately conservative — an orphan
# has lost its owning workspace, so no live session can be using it; a daemon with
# a live parent (a real open workspace, including ours) has ppid != 1 and is left
# strictly alone. Killing an orphan also frees the lock it may be holding (gbrain
# reclaims dead-holder locks), so this doubles as contention relief.
#
# Reaping is best-effort and must never block or fail the real call.
#
# Usage: drop-in for `gbrain` — `gbrain.sh put project/x < x.md`, `gbrain.sh query …`

reap_orphan_serves() {
  local pid ppid
  for pid in $(pgrep -f 'gbrain serve' 2>/dev/null); do
    ppid="$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d ' ')"
    [ "$ppid" = "1" ] && kill "$pid" 2>/dev/null   # orphaned → safe to terminate
  done
}
reap_orphan_serves 2>/dev/null || true

# Hand off to the real gbrain. This wrapper isn't named `gbrain` and isn't on
# $PATH, so `command -v gbrain` resolves the real binary (no recursion).
real="$(command -v gbrain 2>/dev/null)"
[ -n "$real" ] || { echo "gbrain not found on PATH" >&2; exit 127; }
exec "$real" "$@"
