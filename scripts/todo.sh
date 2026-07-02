#!/usr/bin/env bash
# Shim: the TODO queue lives in the Go binary now (`devbrain todo`).
HERE="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
BIN="${DEVBRAIN_BIN:-$HERE/../devbrain}"
[ -x "$BIN" ] || BIN="$(command -v devbrain)" || exit 1
exec "$BIN" todo "$@"
