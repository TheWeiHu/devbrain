#!/usr/bin/env python3
# Shim: the queue server lives in the Go binary now (`devbrain queue`).
# The legacy python implementation is scripts/legacy/queue.py (golden generator until cutover).
import os, shutil, sys
HERE = os.path.dirname(os.path.abspath(__file__))
BIN = os.environ.get("DEVBRAIN_BIN") or os.path.join(HERE, "..", "devbrain")
if not os.access(BIN, os.X_OK):
    BIN = shutil.which("devbrain") or sys.exit("devbrain: go binary not found (build with `go build -o devbrain ./cmd/devbrain`)")
os.execv(BIN, [BIN, "queue"] + sys.argv[1:])
