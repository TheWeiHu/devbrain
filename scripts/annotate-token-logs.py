#!/usr/bin/env python3
"""Mark the pre-dedup `tokens:` lines in historical prompt logs so no reader trusts them.

The prompt logs (projects/<proj>/log/<day>/<worktree>.<session>.md) carry a per-turn
`tokens: in/out/cache_create/cache_read · model: …` meta line. The 2026-06-25 dedup fix
(per-content-block double-count, ~2.85x too high) corrected the tokens.jsonl sidecar and
re-derived history from transcripts — but NOT these inline lines. So every entry logged
before the fix still shows the inflated number, and nothing on the line says the
authoritative source is tokens.jsonl. An agent skimming the logs grabs the wrong figure
(this cost a whole wrong cost analysis once).

This appends a one-time ` ⚠ pre-dedup (see tokens.jsonl)` marker to each inline `tokens:`
line in logs dated before the fix. It does NOT rewrite the numbers: the sidecar's ts drifted
through the history re-derive, so a per-line join back to the corrected value is unreliable;
a visible marker on the number itself is the honest, safe fix. Idempotent (skips already-
marked lines) and dry-run by default — pass --apply to write.

  ./annotate-token-logs.py                 # dry run against $DEVBRAIN_DATA (or ~/devbrain-data)
  ./annotate-token-logs.py --apply         # write the markers
  ./annotate-token-logs.py --data DIR --since 2026-06-25 --apply
"""
import argparse, glob, os, re, sys

MARKER = " ⚠ pre-dedup (see tokens.jsonl)"
FIX_DATE = "2026-06-25"                       # dedup fix landed this UTC day; earlier lines are inflated
DAY_RE = re.compile(r"(\d{4}-\d{2}-\d{2})")   # the log/<YYYY-MM-DD>/ folder
TOK_RE = re.compile(r"^\s*tokens:\s")         # the inline meta line (indented under a ↳ recap)


def log_day(path):
    """The UTC day of a log file, from its .../log/<YYYY-MM-DD>/<file>.md folder."""
    m = DAY_RE.search(os.path.basename(os.path.dirname(path)))
    return m.group(1) if m else ""


def annotate(data, since, apply):
    files = sorted(glob.glob(os.path.join(data, "projects", "*", "log", "*", "*.md")))
    scanned = marked = touched = 0
    for f in files:
        day = log_day(f)
        if not day or day >= since:           # only pre-fix days carry the inflated numbers
            continue
        scanned += 1
        try:
            lines = open(f, encoding="utf-8", errors="replace").read().split("\n")
        except OSError:
            continue
        hits, out = 0, []
        for ln in lines:
            if TOK_RE.match(ln) and MARKER not in ln:
                ln = ln.rstrip() + MARKER
                hits += 1
            out.append(ln)
        if not hits:
            continue
        marked += hits
        touched += 1
        if apply:
            try:
                open(f, "w", encoding="utf-8").write("\n".join(out))
            except OSError as e:
                print(f"  ! could not write {f}: {e}", file=sys.stderr)
    return scanned, touched, marked


def main():
    ap = argparse.ArgumentParser(description="Mark pre-dedup tokens: lines in historical prompt logs.")
    ap.add_argument("--data", default=os.environ.get("DEVBRAIN_DATA", os.path.expanduser("~/devbrain-data")),
                    help="devbrain-data dir (default $DEVBRAIN_DATA or ~/devbrain-data)")
    ap.add_argument("--since", default=FIX_DATE, help=f"mark logs dated before this UTC day (default {FIX_DATE})")
    ap.add_argument("--apply", action="store_true", help="write the markers (default: dry run)")
    args = ap.parse_args()
    scanned, touched, marked = annotate(args.data, args.since, args.apply)
    verb = "marked" if args.apply else "would mark"
    print(f"{'APPLIED' if args.apply else 'DRY RUN'} · pre-{args.since} logs scanned: {scanned} · "
          f"files to touch: {touched} · lines {verb}: {marked}")
    if not args.apply and marked:
        print("re-run with --apply to write.")


if __name__ == "__main__":
    main()
