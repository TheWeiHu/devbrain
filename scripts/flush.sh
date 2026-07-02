#!/usr/bin/env bash
# Shim: the flusher lives in the Go binary now (`devbrain flush`).
# The legacy bash implementation is scripts/legacy/flush.sh (golden generator until cutover).
HERE="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
BIN="${DEVBRAIN_BIN:-$HERE/../devbrain}"
[ -x "$BIN" ] || BIN="$(command -v devbrain)" || exit 1
exec "$BIN" flush "$@"
