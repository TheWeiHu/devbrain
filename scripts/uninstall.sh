#!/usr/bin/env bash
# Shim: the uninstaller lives in the Go binary now (`devbrain uninstall`).
HERE="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
BIN="${DEVBRAIN_BIN:-$HERE/../devbrain}"
[ -x "$BIN" ] || BIN="$(command -v devbrain)" || { echo "devbrain binary not found — build it: go build -o devbrain ./cmd/devbrain" >&2; exit 1; }
exec "$BIN" uninstall "$@"
