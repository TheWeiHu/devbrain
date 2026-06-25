#!/usr/bin/env python3
"""Throwaway: how many agent sessions ran in parallel over time, across all repos.

Reads the Stage-A prompt logs (projects/<proj>/log/<date>/<worktree>.<session>.md).
Each prompt is a UTC timestamp; a session counts as "live" for --ttl minutes after
each of its prompts (overlapping windows merge). Concurrency in a 15-min bucket =
distinct sessions live during it, stacked by project. Tune the flags and re-run.

  python3 scripts/concurrency.py --ttl 15 --days 7 --kind all
"""
import os, re, glob, argparse, datetime, collections
import matplotlib; matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.dates as mdates

_PROMPT_RE = re.compile(r"^## (\d{2}:\d{2}:\d{2})\s*$")
_HEADER_RE = re.compile(r"worktree:\s*(\S+).*?cwd:\s*(\S+)")
_NS = re.compile(r"/(?:nightshift|drain)/|-w\d+$")   # autonomous worker by cwd/worktree


def sessions(data, days, kind):
    """Yield (project, session_id, [datetime,...]) per session file in the window."""
    cutoff = (datetime.date.today() - datetime.timedelta(days=days)).isoformat() if days else "0"
    for md in glob.glob(os.path.join(data, "projects", "*", "log", "*", "*.md")):
        parts = md.split(os.sep)
        date, proj, sess = parts[-2], parts[-4], parts[-1][:-3]
        if date < cutoff:
            continue
        lines = open(md, encoding="utf-8", errors="replace").read().splitlines()
        auton = False
        for l in lines[:6]:
            h = _HEADER_RE.search(l)
            if h: auton = bool(_NS.search(h.group(1)) or _NS.search(h.group(2))); break
        if kind == "bot" and not auton: continue
        if kind == "typed" and auton: continue
        ts = []
        for l in lines:
            m = _PROMPT_RE.match(l)
            if m:
                try: ts.append(datetime.datetime.strptime(f"{date} {m.group(1)}", "%Y-%m-%d %H:%M:%S"))
                except ValueError: pass
        if ts: yield proj, sess, sorted(ts)


def main():
    ap = argparse.ArgumentParser(description="cross-repo agent concurrency over time")
    ap.add_argument("--ttl", type=int, default=15, help="minutes a session stays 'live' after a prompt")
    ap.add_argument("--bucket", type=int, default=15, help="timeline bucket size, minutes")
    ap.add_argument("--days", type=int, default=7, help="lookback window (0 = all history)")
    ap.add_argument("--kind", choices=("all", "typed", "bot"), default="all")
    ap.add_argument("--top", type=int, default=8, help="projects to show; rest fold into 'other'")
    ap.add_argument("--data", default=os.environ.get("DEVBRAIN_DATA", os.path.expanduser("~/devbrain-data")))
    ap.add_argument("--out", default="/tmp/concurrency.png")
    a = ap.parse_args()

    ttl, step = datetime.timedelta(minutes=a.ttl), datetime.timedelta(minutes=a.bucket)
    # Per session, merge prompt times into live windows [start, end].
    wins, lo, hi = [], None, None
    for proj, sess, ts in sessions(a.data, a.days, a.kind):
        s, e = ts[0], ts[0] + ttl
        for t in ts[1:]:
            if t <= e: e = t + ttl
            else: wins.append((proj, sess, s, e)); s, e = t, t + ttl
        wins.append((proj, sess, s, e))
        lo = s if lo is None else min(lo, s); hi = e if hi is None else max(hi, e)
    if not wins:
        return print("no sessions in window")

    # Bucket grid; per bucket, distinct sessions per project.
    n = int((hi - lo) / step) + 1
    grid = [collections.defaultdict(set) for _ in range(n)]
    for proj, sess, s, e in wins:
        for b in range(int((s - lo) / step), int((e - lo) / step) + 1):
            if 0 <= b < n: grid[b][proj].add(sess)
    x = [lo + i * step for i in range(n)]

    totals = collections.Counter()
    for b in grid:
        for proj, ss in b.items(): totals[proj] += len(ss)
    top = [p for p, _ in totals.most_common(a.top)]
    keys = top + (["other"] if len(totals) > len(top) else [])
    series = {k: [0] * n for k in keys}
    for i, b in enumerate(grid):
        for proj, ss in b.items():
            series[proj if proj in top else "other"][i] += len(ss)

    peak = max(sum(series[k][i] for k in keys) for i in range(n))
    peak_at = x[max(range(n), key=lambda i: sum(series[k][i] for k in keys))]
    print(f"peak {peak} concurrent sessions at {peak_at:%Y-%m-%d %H:%M} UTC "
          f"(ttl={a.ttl}m bucket={a.bucket}m days={a.days} kind={a.kind})")

    fig, ax = plt.subplots(figsize=(14, 5))
    ax.stackplot(x, *[series[k] for k in keys], labels=keys, alpha=0.85)
    ax.set_title(f"agents running in parallel · ttl={a.ttl}m bucket={a.bucket}m kind={a.kind}")
    ax.set_ylabel("concurrent sessions"); ax.margins(x=0)
    ax.xaxis.set_major_formatter(mdates.DateFormatter("%m-%d %H:%M"))
    fig.autofmt_xdate(); ax.legend(loc="upper left", fontsize=8, ncol=2)
    fig.tight_layout(); fig.savefig(a.out, dpi=120)
    print("wrote", a.out)


if __name__ == "__main__":
    main()
