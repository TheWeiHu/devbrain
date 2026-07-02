#!/usr/bin/env bash
# Shim: this hook's body lives in the Go binary now (`devbrain hook turn-marker`).
HERE="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
BIN="${DEVBRAIN_BIN:-$HERE/../devbrain}"
[ -x "$BIN" ] || BIN="$(command -v devbrain)" || exit 0
exec "$BIN" hook turn-marker
