#!/usr/bin/env python3
import os
import sys

sys.path.insert(0, os.path.dirname(__file__))
from model_pricing import cost_usd, rate


fail = 0


def check(name, ok):
    global fail
    if ok:
        print(f"  ok   - {name}")
    else:
        fail += 1
        print(f"  FAIL - {name}")


# Keep the legacy public helper shape stable for nightshift callers.
check("rate returns input/output pair", rate("claude-opus-4-8") == (5.0, 25.0))

# Claude cache semantics remain the existing ccusage-compatible 1.25x write and 0.1x read.
check(
    "claude cache-aware cost",
    cost_usd({"claude-opus-4-8": [1_000_000, 1_000_000, 1_000_000, 1_000_000]})
    == 36.75,
)

# Matches: npx ccusage@20.0.14 codex daily --json --since 2026-06-25 --until 2026-06-25
# for the local Codex gpt-5.5 sample: $5 input, $0.50 cached input, $30 output per 1M.
check(
    "codex gpt-5.5 cost matches ccusage",
    cost_usd({"gpt-5.5": [1_487_880, 162_033, 0, 7_266_304]}) == 15.9335,
)

check(
    "codex model-specific cached-input rate",
    cost_usd({"gpt-5.3-codex": [1_000_000, 1_000_000, 0, 1_000_000]})
    == 15.925,
)

print(f"== {4 - fail} passed, {fail} failed ==")
sys.exit(1 if fail else 0)
