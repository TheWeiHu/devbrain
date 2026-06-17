#!/usr/bin/env python3
"""nightshift — emit .nightshift/status.json for the browser dashboard.

Standalone: reconstructs live state from tmux + git + the TODO queue + the
orchestrator log, so the dashboard works regardless of the orchestrator version.
Usage: nightshift-status.py <repo>
"""
import json, os, re, subprocess, sys, datetime
from collections import deque

repo = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/drain/chess-equity")
HERE = os.path.dirname(os.path.abspath(__file__))
TODO = os.path.expanduser("~/.claude/hooks/devbrain-todo.sh")
if not os.access(TODO, os.X_OK):
    TODO = os.path.join(HERE, "todo.sh")
ANSI = re.compile(r"\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07")

def sh(*a, cwd=None):
    try:
        return subprocess.run(a, cwd=cwd, capture_output=True, text=True, timeout=12).stdout
    except Exception:
        return ""

def todo_list(status=""):
    return sh(TODO, "list", *( [status] if status else [] ), cwd=repo)

def count(status=""):
    return sum(1 for l in todo_list(status).splitlines() if re.match(r"\s*\[", l))

def strip(s):
    return ANSI.sub("", s).replace("\r", "")

def token_rate(wt, window=60):
    """New (non-cached) input/output tokens this worker billed in the last `window`s,
    read from its Claude Code transcript. Output/min is the clearest progress signal."""
    slug = os.path.abspath(wt).replace("/", "-")
    d = os.path.expanduser("~/.claude/projects/" + slug)
    try:
        files = [os.path.join(d, f) for f in os.listdir(d) if f.endswith(".jsonl")]
    except Exception:
        return 0, 0
    if not files:
        return 0, 0
    cutoff = datetime.datetime.now(datetime.timezone.utc).timestamp() - window
    tin = tout = 0
    try:
        lines = deque(open(max(files, key=os.path.getmtime), errors="replace"), maxlen=1500)
    except Exception:
        return 0, 0
    for ln in lines:
        try:
            e = json.loads(ln)
        except Exception:
            continue
        u = (e.get("message") or {}).get("usage"); ts = e.get("timestamp")
        if not u or not ts:
            continue
        try:
            t = datetime.datetime.fromisoformat(ts.replace("Z", "+00:00")).timestamp()
        except Exception:
            continue
        if t >= cutoff:
            tin += u.get("input_tokens") or 0
            tout += u.get("output_tokens") or 0
    return tin, tout

# workers (ns-w0, ns-w1, … while sessions exist)
sessions = sh("tmux", "ls")
workers = []
i = 0
tin_total = tout_total = 0
while f"ns-w{i}" in sessions:
    s, wt = f"ns-w{i}", f"{repo}-w{i}"
    pane = sh("tmux", "capture-pane", "-t", s, "-p")
    branch = sh("git", "-C", wt, "branch", "--show-current").strip()
    tin, tout = token_rate(wt)
    tin_total += tin; tout_total += tout
    workers.append({
        "i": i,
        "state": "working" if "esc to interrupt" in pane else "idle",
        "task": branch[5:] if branch.startswith("todo/") else (branch or "—"),
        "tin": tin, "tout": tout,
        "pane": "\n".join(strip(pane).splitlines()[-28:]).rstrip(),
    })
    i += 1

sh("git", "-C", repo, "fetch", "-q", "origin")
staging = [l for l in sh("git", "-C", repo, "log", "--oneline",
                         "origin/main..origin/staging").splitlines()
           if "merge" in l.lower()][:14]

logp = os.path.join(repo, ".nightshift", "orchestrator.log")
log = open(logp, errors="replace").read().splitlines()[-16:] if os.path.exists(logp) else []

parkedp = os.path.join(repo, ".nightshift", "parked")
parked = []
if os.path.exists(parkedp):
    seen = set()
    for l in open(parkedp, errors="replace").read().splitlines():
        l = l.strip()
        if l and l not in seen:
            seen.add(l); parked.append(l)

running = bool(sh("pgrep", "-f", f"nightshift-orchestrate.sh --repo {repo}").strip())
data = {
    "updated": sh("date", "-u", "+%Y-%m-%dT%H:%M:%SZ").strip(),
    "project": os.path.basename(repo),
    "running": running,
    "queue": {"open": count(), "done": count("done"), "review": count("review")},
    "tokens_min": {"in": tin_total, "out": tout_total},   # new (non-cached) tokens, last 60s
    "parked": parked,
    "workers": workers,
    "staging": staging,
    "log": log,
}
os.makedirs(os.path.join(repo, ".nightshift"), exist_ok=True)
tmp = os.path.join(repo, ".nightshift", "status.json.tmp")
with open(tmp, "w") as f:
    json.dump(data, f)
os.replace(tmp, os.path.join(repo, ".nightshift", "status.json"))
