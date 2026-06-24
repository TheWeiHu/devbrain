#!/usr/bin/env python3
"""nightshift — emit .nightshift/status.json for the browser dashboard.

Standalone: reconstructs live state from tmux + git + the TODO queue + the
orchestrator log, so the dashboard works regardless of the orchestrator version.
Usage: nightshift-status.py <repo>
"""
import json, os, re, subprocess, sys, datetime
from collections import deque

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)                 # the sibling model_pricing module (installed alongside)
from model_pricing import cost_usd       # pricing table + cache-aware cost, kept out of code

# Require an explicit repo — no hardcoded default. A fallback path here got
# re-materialized every tick (makedirs below) by orphaned emit loops, ghosting a dir.
if len(sys.argv) < 2:
    sys.exit("usage: nightshift-status.py <repo>")
repo = sys.argv[1]
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
    input_total = output_total = 0
    try:
        lines = deque(open(max(files, key=os.path.getmtime), errors="replace"), maxlen=1500)
    except Exception:
        return 0, 0
    seen = set()      # (message.id, requestId) — Claude Code replays prior turns into the
    for ln in lines:  # JSONL on resume/compaction; dedup like ccusage so we don't double-bill.
        try:
            e = json.loads(ln)
        except Exception:
            continue
        msg = e.get("message") or {}
        u = msg.get("usage"); ts = e.get("timestamp")
        if not u or not ts:
            continue
        key = (msg.get("id"), e.get("requestId"))
        if key[0] and key in seen:
            continue
        seen.add(key)
        try:
            t = datetime.datetime.fromisoformat(ts.replace("Z", "+00:00")).timestamp()
        except Exception:
            continue
        if t >= cutoff:
            input_total += u.get("input_tokens") or 0
            output_total += u.get("output_tokens") or 0
    return input_total, output_total

def token_total(wt):
    """CUMULATIVE tokens this worker has billed across ALL of its turns — the whole run,
    not a sliding window (cf. token_rate, which caps at the last 60s for the throughput
    chart). Reads EVERY transcript in the worker's dir, which nightshift reuses across
    runs, so the total carries over restarts. Returns (input, output, {model: [input,
    output, cache_create, cache_read]}) — input/output are non-cached (the Σ-tokens
    headline); the per-model split carries all four kinds so the caller can price cache."""
    slug = os.path.abspath(wt).replace("/", "-")
    transcript_dir = os.path.expanduser("~/.claude/projects/" + slug)
    try:
        files = [os.path.join(transcript_dir, f) for f in os.listdir(transcript_dir) if f.endswith(".jsonl")]
    except Exception:
        return 0, 0, {}
    total_input = total_output = 0
    tokens_by_model = {}
    seen = set()       # (message.id, requestId) dedup — see token_rate: Claude Code replays
    for path in files: # earlier turns into the JSONL (resume/compaction), so the same usage
        try:           # record recurs. ccusage keys on these two ids; matching it keeps the
            fh = open(path, errors="replace")  # cumulative Σ from inflating ~2× on long runs.
        except Exception:
            continue
        with fh:
            for line in fh:
                try:
                    event = json.loads(line)
                except Exception:
                    continue
                message = event.get("message") or {}
                usage = message.get("usage")
                model = message.get("model") or ""
                if not usage or model == "<synthetic>":   # synthetic = local, non-API turn → no spend
                    continue
                key = (message.get("id"), event.get("requestId"))
                if key[0] and key in seen:
                    continue
                seen.add(key)
                input_tokens = usage.get("input_tokens") or 0
                output_tokens = usage.get("output_tokens") or 0
                cache_create = usage.get("cache_creation_input_tokens") or 0
                cache_read = usage.get("cache_read_input_tokens") or 0
                total_input += input_tokens
                total_output += output_tokens
                row = tokens_by_model.setdefault(model, [0, 0, 0, 0])
                row[0] += input_tokens; row[1] += output_tokens
                row[2] += cache_create; row[3] += cache_read
    return total_input, total_output, tokens_by_model

def recent_responses(wt, limit=40, files=8):
    """The agent's actual text messages (what it's saying/doing) pulled from this
    worker's Claude Code transcript — the 'responses' feed the headless shell can't
    provide: `claude -p` buffers stdout until the turn EXITS, so turn.log is empty
    the whole time a worker is busy, but the transcript streams live.

    Crucially this is keyed on the WORKTREE PATH (`{repo}-w{i}`), which nightshift
    reuses across runs, so the transcript dir accumulates EVERY turn this worker
    slot has ever run. Reading the newest `files` transcripts (each headless turn is
    one .jsonl) lets a worker window carry its history across a restart — start a
    fresh nightshift session and worker i still shows the same worker's earlier work.
    Each message carries a `sid` (the turn/session id) so the dashboard can draw a
    light divider between turns. Newest last."""
    slug = os.path.abspath(wt).replace("/", "-")
    d = os.path.expanduser("~/.claude/projects/" + slug)
    try:
        fps = sorted((os.path.join(d, f) for f in os.listdir(d) if f.endswith(".jsonl")),
                     key=os.path.getmtime)[-files:]
    except Exception:
        return []
    msgs = []
    for fp in fps:
        sid = os.path.basename(fp).split("-")[0]   # short turn/session tag
        try:
            lines = deque(open(fp, errors="replace"), maxlen=4000)
        except Exception:
            continue
        for ln in lines:
            try:
                e = json.loads(ln)
            except Exception:
                continue
            if e.get("type") != "assistant":
                continue
            msg = e.get("message") or {}
            txt = "".join(b.get("text", "") for b in (msg.get("content") or [])
                          if isinstance(b, dict) and b.get("type") == "text").strip()
            if not txt:
                continue
            t = ""
            ts = e.get("timestamp")
            if ts:
                try:
                    t = datetime.datetime.fromisoformat(ts.replace("Z", "+00:00")).astimezone().strftime("%H:%M:%S")
                except Exception:
                    t = ""
            msgs.append({"t": t, "sid": sid, "text": txt[:700]})
    return msgs[-limit:]

# workers (ns-w0, ns-w1, … while sessions exist)
sessions = sh("tmux", "ls")
workers = []
i = 0
rate_input_total = rate_output_total = 0   # last-60s rate, summed across workers (the chart)
cumulative_input = cumulative_output = 0    # CUMULATIVE non-cached in/out across the whole run
cumulative_by_model = {}                    # {model: [in, out, cache_create, cache_read]} for pricing
def add_cumulative(wt):
    global cumulative_input, cumulative_output
    run_input, run_output, by_model = token_total(wt)
    cumulative_input += run_input; cumulative_output += run_output
    for model, counts in by_model.items():
        row = cumulative_by_model.setdefault(model, [0, 0, 0, 0])
        for idx in range(4):
            row[idx] += counts[idx]
while f"ns-w{i}" in sessions:
    s, wt = f"ns-w{i}", f"{repo}-w{i}"
    pane = sh("tmux", "capture-pane", "-t", s, "-p")
    branch = sh("git", "-C", wt, "branch", "--show-current").strip()
    rate_in, rate_out = token_rate(wt)
    rate_input_total += rate_in; rate_output_total += rate_out
    add_cumulative(wt)
    workers.append({
        "i": i,
        "state": "working" if "esc to interrupt" in pane else "idle",
        "task": branch[5:] if branch.startswith("todo/") else (branch or "—"),
        "tin": rate_in, "tout": rate_out,
        "pane": "\n".join(strip(pane).splitlines()[-45:]).rstrip(),
        "responses": recent_responses(wt),
    })
    i += 1

# Headless backend (claude -p, the default): no tmux sessions exist. Reconstruct
# workers from the per-worker worktrees + their turn.log. "working" = the worker is
# billing tokens right now (a claude -p turn is mid-flight); the pane is the last
# turn's output (headless has no live keystroke mirror — that's a --tmux feature).
if not workers:
    j = 0
    while os.path.isdir(f"{repo}-w{j}"):
        wt = f"{repo}-w{j}"
        branch = sh("git", "-C", wt, "branch", "--show-current").strip()
        rate_in, rate_out = token_rate(wt)
        rate_input_total += rate_in; rate_output_total += rate_out
        add_cumulative(wt)
        logf = os.path.join(wt, ".nightshift", "turn.log")
        pane = ""
        if os.path.exists(logf):
            try:
                pane = "\n".join(strip(open(logf, errors="replace").read()).splitlines()[-45:]).rstrip()
            except Exception:
                pane = ""
        workers.append({
            "i": j,
            "state": "working" if rate_out > 0 else "idle",
            "task": branch[5:] if branch.startswith("todo/") else (branch or "—"),
            "tin": rate_in, "tout": rate_out,
            "pane": pane or "(headless — the last turn's output appears here)",
            "responses": recent_responses(wt),
        })
        j += 1

sh("git", "-C", repo, "fetch", "-q", "origin")
nightshift = [l for l in sh("git", "-C", repo, "log", "--oneline",
                         "origin/main..origin/nightshift").splitlines()
           if "merge" in l.lower()][:14]

logp = os.path.join(repo, ".nightshift", "orchestrator.log")
log = open(logp, errors="replace").read().splitlines()[-16:] if os.path.exists(logp) else []

# "needs you" = tasks in the `held` status, each with its reason AND a link to the
# diff to review (the recorded PR, else a nightshift...branch compare) so the dashboard
# lets you actually look at what failed — not just a bare id. A reason that starts with
# `parked` marks a DELIBERATE backlog park (focus-hold), not a block — nothing needs a
# human there, so it's excluded from the banner to keep it to true blocks/failures.
slug = re.sub(r"(\.git)?\s*$", "", sh("git", "-C", repo, "remote", "get-url", "origin").strip())
slug = re.sub(r".*[:/]([^/]+/[^/]+)$", r"\1", slug)
parked = []          # genuine blocks → the "needs you" banner
parked_count = 0     # deliberately parked (focus-holds) → a count only, no banner row
for hid in re.findall(r"[0-9]{4}-[a-z0-9-]+", todo_list("held")):
    show = sh(TODO, "show", hid, cwd=repo)
    rm = re.search(r"^reason:\s*(.+)$", show, re.M)
    reason = rm.group(1).strip() if rm else ""
    if re.match(r"(?i)\s*parked\b", reason):   # deliberate focus-park, not a "needs you"
        parked_count += 1
        continue
    pm = re.search(r"^pr:\s*(https?://\S+)", show, re.M)
    url = pm.group(1) if pm else ""
    if not url and slug and sh("git", "-C", repo, "ls-remote", "--heads", "origin", f"todo/{hid}").strip():
        url = f"https://github.com/{slug}/compare/nightshift...todo/{hid}?expand=1"
    parked.append({"id": hid, "reason": reason, "url": url})

running = bool(sh("pgrep", "-f", f"nightshift-orchestrate.sh --repo {repo}").strip())

# Per-minute throughput history: read the prior status.json, update the current
# minute's sample (out/in tokens/min), trim to the last 90 minutes. Survives ticks
# and restarts since status.json persists.
status_path = os.path.join(repo, ".nightshift", "status.json")
try:
    hist = json.load(open(status_path)).get("history", [])
except Exception:
    hist = []
minute = datetime.datetime.now().strftime("%H:%M")
point = {"t": minute, "out": rate_output_total, "in": rate_input_total}
if hist and hist[-1].get("t") == minute:
    hist[-1] = point          # same clock-minute → keep the latest sample
else:
    hist.append(point)
hist = hist[-90:]

data = {
    "updated": sh("date", "-u", "+%Y-%m-%dT%H:%M:%SZ").strip(),
    "project": os.path.basename(repo),
    "running": running,
    "queue": {"open": count(), "done": count("done"), "review": count("review")},
    "tokens_min": {"in": rate_input_total, "out": rate_output_total},   # new (non-cached) tokens, last 60s
    # CUMULATIVE tokens across the whole run (all workers, all turns). tokens_total is
    # new (non-cached) in/out — apples-to-apples with tokens_min/the chart. cost_total is
    # TRUE billed spend: output + input + cache (write 1.25× input, read 0.1× input).
    "tokens_total": {"in": cumulative_input, "out": cumulative_output},
    "cost_total": cost_usd(cumulative_by_model),
    "history": hist,
    "parked": parked,
    "parked_count": parked_count,   # deliberately-parked focus-holds (shown as a count, not the banner)
    "workers": workers,
    "nightshift": nightshift,
    "log": log,
}
os.makedirs(os.path.join(repo, ".nightshift"), exist_ok=True)
tmp = os.path.join(repo, ".nightshift", "status.json.tmp")
with open(tmp, "w") as f:
    json.dump(data, f)
os.replace(tmp, os.path.join(repo, ".nightshift", "status.json"))
