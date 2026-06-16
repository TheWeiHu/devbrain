#!/usr/bin/env bash
# devbrain — resilient gbrain write.
#
# Why this exists: gbrain's PGLite engine is a single-writer embedded Postgres.
# Every Conductor workspace runs its own `gbrain serve` (stdio MCP) against the
# one shared ~/.gbrain/brain.pglite, and a `gbrain embed` can hold the write lock
# for minutes. When devbrain's skills write during that window, the bare CLI call
# waits 30s, times out ("Timed out waiting for PGLite lock"), and the page is
# silently left only-on-disk until the next full rebuild.
#
# This wrapper retries the write with backoff so it simply *waits its turn* and
# serializes behind the long writer, instead of dropping. The brain is a
# rebuildable projection, so we still exit 0 even if every attempt is contended —
# a hook/skill must never be broken by the brain being momentarily busy.
#
# It does NOT reduce the number of daemons (that's inherent to per-workspace stdio
# MCP); the architectural cure for heavy multi-workspace use is migrating the brain
# to Supabase (concurrent connections, no file lock). See DESIGN decision #7.
#
# Usage:  gbrain-write put project/foo < foo.md
#         gbrain-write tag project/foo devbrain
#         gbrain-write embed --stale
# Pass exactly what you'd pass to `gbrain`. Reads stdin only for `put`/`import`.

retries="${DEVBRAIN_GBRAIN_RETRIES:-5}"

# Buffer stdin once (only for commands that consume it) so it can be replayed on
# each retry. Guarding on the subcommand avoids a blocking `cat` on a stdin that
# the wrapped command never reads.
input=""
case "${1:-}" in
  put|import) input="$(cat)" ;;
esac

attempt=1
while [ "$attempt" -le "$retries" ]; do
  if [ -n "$input" ]; then
    out="$(printf '%s' "$input" | gbrain "$@" 2>&1)"; rc=$?
  else
    out="$(gbrain "$@" 2>&1)"; rc=$?
  fi

  if [ "$rc" -eq 0 ]; then
    [ -n "$out" ] && printf '%s\n' "$out"
    exit 0
  fi

  case "$out" in
    *"Timed out waiting for PGLite lock"*|*"Aborted"*)
      sleep "$((attempt * 3))" ;;   # 3,6,9,12,15s — rides out a long embed
    *)
      printf '%s\n' "$out" >&2       # real error (bad slug, etc.) — surface it
      exit "$rc" ;;
  esac
  attempt="$((attempt + 1))"
done

printf 'gbrain-write: "%s" still lock-contended after %s tries; page is on disk, next rebuild ingests it\n' \
  "${2:-$1}" "$retries" >&2
exit 0
