#!/usr/bin/env python3
"""Tests for annotate-token-logs.py — the migration that marks pre-dedup `tokens:` lines
in historical prompt logs. Covers: only pre-fix days marked, post-fix left alone,
idempotency, dry-run writes nothing, --since override."""
import importlib.util, os, sys, tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("annotate", os.path.join(HERE, "annotate-token-logs.py"))
mod = importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)

pass_ = fail = 0
def check(name, cond):
    global pass_, fail
    if cond: pass_ += 1; print(f"  ok   - {name}")
    else: fail += 1; print(f"  FAIL - {name}")

TOK = "   tokens: 94856/1286/0/94208 · model: claude-opus-4-8\n"

def write_log(data, proj, day, name, body):
    d = os.path.join(data, "projects", proj, "log", day); os.makedirs(d, exist_ok=True)
    p = os.path.join(d, name)
    open(p, "w").write(body); return p

with tempfile.TemporaryDirectory() as data:
    pre = write_log(data, "p", "2026-06-19", "wt.s1.md", "# log\n↳ 07:51:53 — done\n" + TOK)
    post = write_log(data, "p", "2026-06-28", "wt.s2.md", "# log\n↳ 09:00:00 — done\n" + TOK)

    # dry run: reports but writes nothing
    scanned, touched, marked = mod.annotate(data, mod.FIX_DATE, apply=False)
    check("dry-run finds the pre-fix line", marked == 1 and touched == 1)
    check("dry-run writes nothing", mod.MARKER not in open(pre).read())

    # apply: pre-fix line marked, post-fix untouched
    mod.annotate(data, mod.FIX_DATE, apply=True)
    check("pre-fix line marked", mod.MARKER in open(pre).read())
    check("post-fix line untouched", mod.MARKER not in open(post).read())
    check("only the tokens line changed", open(pre).read().count("↳ 07:51:53 — done") == 1)

    # idempotent: a second apply adds no second marker
    mod.annotate(data, mod.FIX_DATE, apply=True)
    check("idempotent (one marker only)", open(pre).read().count(mod.MARKER) == 1)

    # --since override: pushing the cutoff past the post-fix day marks it too
    _, _, marked2 = mod.annotate(data, "2026-06-30", apply=False)
    check("--since widens the pre-fix window", marked2 == 1)  # post now in-window (pre already marked)

print(f"\n== {pass_} passed, {fail} failed ==")
sys.exit(1 if fail else 0)
