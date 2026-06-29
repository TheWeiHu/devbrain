#!/usr/bin/env python3
"""devbrain — nightshift run identity. The dashboard goes stale on every restart because a
restart is a NEW run the loaded page can't tell from the old one. nightshift-status.py now
stamps each run with an id (the orchestrator PID) + start time and resets the throughput chart
when a fresh run begins, so the dashboard can show run identity + a staleness badge.

We exercise the pure `run_identity()` helper without running the heavy emit body: exec the
script with a 1-arg argv so it hits its `usage` SystemExit right after the helper is defined,
then pull the function out of the namespace. No subprocess, no tmux/git/pgrep, no real services."""
import os
import sys

HERE = os.path.realpath(os.path.dirname(os.path.abspath(__file__)))
SCRIPT = os.path.join(HERE, "nightshift-status.py")

# Load run_identity without executing the emit body (argv len 1 -> the usage sys.exit fires
# AFTER the def). model_pricing imports at module top, so HERE must be importable.
sys.path.insert(0, HERE)
ns: dict = {"__file__": SCRIPT, "__name__": "_runid_under_test"}
_argv = sys.argv
sys.argv = ["nightshift-status.py"]
try:
    exec(compile(open(SCRIPT).read(), SCRIPT, "exec"), ns)
except SystemExit:
    pass
finally:
    sys.argv = _argv
run_identity = ns["run_identity"]

NOW = "2026-06-27T12:00:00Z"
LATER = "2026-06-27T13:30:00Z"
pass_, fail = 0, 0


def check(name, cond):
    global pass_, fail
    if cond:
        pass_ += 1; print(f"  ok   — {name}")
    else:
        fail += 1; print(f"  FAIL — {name}")


# fresh start (no prior status on disk) — mint identity, stamp start, history starts empty
rid, started, reset = run_identity({}, True, "4242", NOW)
check("first run gets the orchestrator pid as run id", rid == "4242")
check("first run stamps started = now", started == NOW)
check("first run resets history", reset is True)

# same run, a later tick — identity + start are stable, history is KEPT (no reset)
prior = {"run_id": "4242", "started": NOW, "history": [{"t": "12:00"}]}
rid, started, reset = run_identity(prior, True, "4242", LATER)
check("continuing run keeps its run id", rid == "4242")
check("continuing run keeps its original start time", started == NOW)
check("continuing run does NOT reset history", reset is False)

# RESTART — orchestrator came back with a new pid. New id, new start, fresh chart.
rid, started, reset = run_identity(prior, True, "9999", LATER)
check("restart adopts the new pid as run id", rid == "9999")
check("restart re-stamps started = now", started == LATER)
check("restart resets history (no chart bleed from the old run)", reset is True)

# stopped — keep the last known identity + start so a just-ended run still shows which run it was
rid, started, reset = run_identity(prior, False, "", LATER)
check("stopped run keeps the last run id", rid == "4242")
check("stopped run keeps the last start time", started == NOW)
check("stopped run never resets history", reset is False)

# stopped with no prior at all — empty identity, never crashes
rid, started, reset = run_identity({}, False, "", LATER)
check("stopped + no prior yields empty identity", rid == "" and started == "")

print(f"\n== {pass_} passed, {fail} failed ==")
sys.exit(1 if fail else 0)
