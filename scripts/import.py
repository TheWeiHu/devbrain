#!/usr/bin/env python3
"""devbrain import — one-time backfill so a fresh install has VALUE on day one.

Claude Code already holds months of history on the machine. This seeds the devbrain
data repo from it, so `gbrain search` returns hits immediately instead of after weeks
of forward capture. It is the batch counterpart to the live capture hooks:

  live  : capture.sh + capture-response.sh + capture-memory.sh  (one turn / session)
  batch : THIS                                                   (everything so far)

Three sources, harvested into the same layout the hooks write:
  1. ~/.claude/projects/<slug>/<session>.jsonl  — transcripts (prompts AND responses)
       -> projects/<key>/log/<day>/<worktree>.<session>.md   (PRIMARY; has ↳ recaps)
  2. ~/.claude/history.jsonl                     — typed prompts (fallback for sessions
       whose transcript was pruned)              -> same log layout, prompt-only
  3. ~/.claude/projects/<slug>/memory/*.md       — Claude's curated memory store, the
       longest-lived/highest-fidelity source     -> projects/<key>/memory/*.md

Safe by construction: redacts secrets, skips sessions already captured live, is
idempotent, and DRY-RUNS by default (prints a manifest; --apply to write).

Routing: a cache only records the cwd. Identity is the git remote of a still-present
dir (the same harness-agnostic rule as project-key.sh), else a user-declared alias for
the trailing dir name, else miscellaneous. No path parsing, no basename guessing.

Shared rules (redaction, synthetic-prompt filter, the merged-#15 recap, remote_to_key)
are NOT re-implemented here — they live once in hooks/devbrain_lib.py and are imported
below, the same definitions the live bash hooks call (via its CLI). So the produced logs
are byte-compatible with live capture by construction, with no copy to keep in sync.
"""
import argparse, json, os, re, glob, subprocess, datetime, collections, sys

# The shared rules (redaction, synthetic-prompt filter, summarizer, remote_to_key) live
# in hooks/devbrain_lib.py — ONE definition used by both the live bash hooks and this
# batch importer. Find it whether co-installed in ~/.claude/hooks (installed) or in the
# sibling hooks/ dir (repo checkout).
sys.path[:0] = [os.path.dirname(os.path.abspath(__file__)),
                os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "hooks"),
                os.path.expanduser("~/.claude/hooks")]
from devbrain_lib import redact, is_synthetic, recap, remote_to_key  # noqa: E402

def sanitize(s):
    return re.sub(r"[^a-z0-9._-]", "", s.lower().replace(" ", "-"))

def git_remote(path):
    try:
        return subprocess.run(["git", "-C", path, "remote", "get-url", "origin"],
                              capture_output=True, text=True, timeout=5).stdout.strip()
    except Exception:
        return ""

def route(cwd, aliases):
    """Identity from the git remote only — the same harness-agnostic rule as
    project-key.sh: no path parsing, no basename guessing. Returns (key, confidence)."""
    # 1. live path with a remote (still-present repo / worktree) — exact.
    if os.path.isdir(cwd):
        k = remote_to_key(git_remote(cwd))
        if k:
            return (k, "high")
    # 2. explicit alias for the trailing dir name (renames the git remote can't show,
    #    e.g. RedditPages -> redlens). The only non-remote routing, and it's user-declared.
    seg = os.path.basename(cwd.rstrip("/"))
    if seg in aliases:
        return (aliases[seg], "high")
    # 3. unresolved (dead worktree, no remote, no alias) -> shared bucket. Data is kept,
    #    just unrouted; add an alias if a project deserves its own folder.
    return ("miscellaneous", "low")

# --------------------------------------------------- prompt / response ---------
def text_of(content):
    """User-prompt text, or None if missing or a synthetic/injected prompt."""
    if isinstance(content, str):
        text = content
    elif isinstance(content, list):
        text = "".join(b.get("text", "") for b in content
                       if isinstance(b, dict) and b.get("type") == "text")
    else:
        return None
    text = text.strip()
    return None if (not text or is_synthetic(text)) else text

def iso(s):
    return datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))

def parse_transcript(path):
    recs = []
    for ln in open(path, encoding="utf-8", errors="replace"):
        ln = ln.strip()
        if ln:
            try:
                recs.append(json.loads(ln))
            except Exception:
                pass
    turns, cur = [], None
    for e in recs:
        t = e.get("type")
        if t == "user" and not e.get("isSidechain"):
            p = text_of(e.get("message", {}).get("content"))
            if p is None:
                continue
            if cur:
                turns.append(cur)
            cur = {"dt": iso(e["timestamp"]), "cwd": e.get("cwd") or "", "prompt": p,
                   "texts": [], "tools": {}, "files": {}, "resp_dt": None,
                   "tin": 0, "tout": 0, "tcc": 0, "tcr": 0, "model": ""}
        elif t == "assistant" and cur is not None:
            cur["resp_dt"] = iso(e["timestamp"])
            msg = e.get("message", {}) or {}
            u = msg.get("usage") or {}      # same usage join as the live capture-response hook
            cur["tin"] += u.get("input_tokens") or 0
            cur["tout"] += u.get("output_tokens") or 0
            cur["tcc"] += u.get("cache_creation_input_tokens") or 0
            cur["tcr"] += u.get("cache_read_input_tokens") or 0
            if msg.get("model"):
                cur["model"] = msg["model"]
            for b in e.get("message", {}).get("content", []):
                if not isinstance(b, dict):
                    continue
                if b.get("type") == "text":
                    cur["texts"].append(b.get("text", ""))
                elif b.get("type") == "tool_use":
                    n = b.get("name", "?")
                    cur["tools"][n] = cur["tools"].get(n, 0) + 1
                    fp = (b.get("input") or {}).get("file_path") or (b.get("input") or {}).get("path")
                    if fp:
                        cur["files"][fp.rsplit("/", 1)[-1]] = True
    if cur:
        turns.append(cur)
    out = []
    for c in turns:
        meta = []
        if c["files"]:
            meta.append("touched: " + ", ".join(c["files"]))
        if c["tools"]:
            meta.append("tools: " + ", ".join(f"{k}×{v}" for k, v in c["tools"].items()))
        out.append({"dt": c["dt"], "cwd": c["cwd"], "prompt": redact(c["prompt"]),
                    "resp_dt": c["resp_dt"] or c["dt"], "summary": redact(recap(c["texts"])),
                    "meta": redact("  ·  ".join(meta)),
                    "tin": c["tin"], "tout": c["tout"], "tcc": c["tcc"], "tcr": c["tcr"],
                    "model": c["model"]})
    return out

# ------------------------------------------------------------ already-live -----
def live_sessions(data):
    live = set()
    for f in glob.glob(os.path.join(data, "projects", "*", "log", "*", "*.md")):
        stem = os.path.basename(f)[:-3]
        sid = stem.split(".", 1)[1] if "." in stem else stem
        try:
            if "BACKFILLED" not in open(f, encoding="utf-8", errors="replace").read(600):
                live.add(sid)
        except Exception:
            pass
    return live

BANNER = "> ⚠️ BACKFILLED from ~/.claude (history.jsonl + transcripts); not captured live.\n"

# ------------------------------------------------------------------ main -------
def main():
    ap = argparse.ArgumentParser(description="Seed devbrain from existing Claude Code caches.")
    ap.add_argument("--apply", action="store_true", help="write into the data repo (default: dry-run)")
    ap.add_argument("--data", default=os.environ.get("DEVBRAIN_DATA", os.path.expanduser("~/devbrain-data")))
    ap.add_argument("--claude", default=os.path.expanduser("~/.claude"))
    ap.add_argument("--exclude", default="", help="comma-separated project keys to skip")
    ap.add_argument("--alias", action="append", default=[], help="OLD=key rename (repeatable)")
    ap.add_argument("--no-memory", action="store_true", help="skip the memory/ harvest")
    args = ap.parse_args()

    data, claude = args.data, args.claude
    exclude = {x for x in args.exclude.split(",") if x}

    # Aliases for renames the git remote can't show (e.g. RedditPages -> redlens). Persistent
    # ones live in $DATA/.import-aliases (OLD=key per line); --alias on the CLI wins.
    aliases = {}
    alias_file = os.path.join(data, ".import-aliases")
    if os.path.exists(alias_file):
        for line in open(alias_file, encoding="utf-8", errors="replace"):
            line = line.split("#", 1)[0].strip()
            if "=" in line:
                o, k = line.split("=", 1)
                aliases[o.strip()] = k.strip()
    aliases.update(a.split("=", 1) for a in args.alias if "=" in a)

    live = live_sessions(data)
    existing = set(os.listdir(os.path.join(data, "projects"))) if os.path.isdir(os.path.join(data, "projects")) else set()

    # ---- harvest logs (transcripts primary, history.jsonl fallback) ----
    # Iterate EVERY transcript on disk. The LOG harvest is gated per-session on live-ness
    # (a live session already has a prompt log, so re-importing would duplicate it). The
    # TOKEN harvest is NOT gated: token logging is brand-new, so even a live-captured
    # session has no token data — its prompt log existing says nothing about whether its
    # tokens were recorded. Gating the sidecar on live-ness too would leave an existing
    # install's whole live history with no cost data.
    all_transcripts = {os.path.basename(f)[:-6]: f for f in glob.glob(os.path.join(claude, "projects", "*", "*.jsonl"))}
    groups = {}
    n_prompts = collections.defaultdict(int)
    n_resp = collections.defaultdict(int)
    n_mem = collections.defaultdict(int)
    conf_of = collections.defaultdict(lambda: "low")
    ORDER = {"high": 3, "medium": 2, "low": 1}
    done_sessions = set()

    def add_entry(cwd, sid, dt, prompt, resp_dt=None, summary=None, meta=None):
        key, conf = route(cwd, aliases)
        wt = sanitize(os.path.basename(cwd.rstrip("/"))) or "unknown"
        gk = (key, wt, sanitize(sid) or "nosession", dt.strftime("%Y-%m-%d"), cwd)
        groups.setdefault(gk, []).append((dt, prompt, resp_dt, summary, meta))
        n_prompts[key] += 1
        if summary:
            n_resp[key] += 1
        if ORDER[conf] > ORDER[conf_of[key]]:
            conf_of[key] = conf

    # Per-turn token records harvested alongside the logs, keyed by project. Written to
    # projects/<key>/tokens.jsonl on --apply — the historical counterpart to the live
    # capture-response sidecar, so the cost view has data for sessions captured before
    # this feature existed (only transcripts still on disk; pruned ones are forward-only).
    token_recs = collections.defaultdict(list)
    for sid, path in all_transcripts.items():
        try:
            turns = parse_transcript(path)
        except Exception:
            continue
        if not turns:
            continue
        is_live = sid in live          # live = already has a prompt log; skip the LOG harvest only
        if not is_live:
            done_sessions.add(sid)
        for t in turns:
            if not is_live:
                add_entry(t["cwd"], sid, t["dt"], t["prompt"], t["resp_dt"], t["summary"], t["meta"])
            if t["tin"] or t["tout"] or t["tcc"] or t["tcr"]:
                key, _ = route(t["cwd"], aliases)
                token_recs[key].append({
                    "ts": t["resp_dt"].strftime("%Y-%m-%dT%H:%M:%SZ"), "session": sid,
                    "model": t["model"], "in": t["tin"], "out": t["tout"],
                    "cache_create": t["tcc"], "cache_read": t["tcr"]})

    hist = os.path.join(claude, "history.jsonl")
    if os.path.exists(hist):
        for l in open(hist, encoding="utf-8", errors="replace"):
            try:
                r = json.loads(l)
            except Exception:
                continue
            p = (r.get("display") or "").strip()
            sid = r.get("sessionId") or "nosession"
            if not p or sid in done_sessions or sid in live or is_synthetic(p):
                continue
            dt = datetime.datetime.fromtimestamp(r["timestamp"] / 1000, datetime.timezone.utc)
            add_entry(r.get("project") or "", sid, dt, redact(p))

    # ---- harvest memory stores ----
    memory = collections.defaultdict(dict)   # key -> {filename: redacted_text}
    if not args.no_memory:
        for md in glob.glob(os.path.join(claude, "projects", "*", "memory")):
            # the project dir's transcript tells us the cwd; fall back to slug guess
            cwd = ""
            for tf in glob.glob(os.path.join(os.path.dirname(md), "*.jsonl")):
                try:
                    for ln in open(tf, encoding="utf-8", errors="replace"):
                        c = json.loads(ln).get("cwd")
                        if c:
                            cwd = c; break
                except Exception:
                    pass
                if cwd:
                    break
            if not cwd:   # no transcript left: reconstruct from the slug (best effort)
                cwd = "/" + os.path.basename(os.path.dirname(md)).lstrip("-").replace("-", "/")
            key, kconf = route(cwd, aliases)
            if ORDER[kconf] > ORDER[conf_of[key]]:
                conf_of[key] = kconf
            for f in glob.glob(os.path.join(md, "*.md")):
                memory[key][os.path.basename(f)] = redact(open(f, encoding="utf-8", errors="replace").read())
                n_mem[key] += 1

    # ---- manifest ----
    keys = sorted(set(n_prompts) | set(memory), key=lambda k: -(n_prompts[k] + n_mem[k]))
    print(f"{'PROMPTS':>7} {'RESP':>5} {'MEM':>4}  CONF    KEY")
    print("-" * 64)
    total_files = 0
    for k in keys:
        if k in exclude:
            print(f"{'—':>7} {'—':>5} {'—':>4}  skip    {k}  (excluded)")
            continue
        tag = "" if k in existing else "  (NEW)"
        print(f"{n_prompts[k]:7} {n_resp[k]:5} {n_mem[k]:4}  {conf_of[k]:6}  {k}{tag}")

    # ---- write ----
    if not args.apply:
        print(f"\nDRY-RUN. {len(keys)} projects. Re-run with --apply to write into {data}.")
        print("Opt out of a project:  --exclude <key>[,<key>...]   ·   fix routing:  --alias OLD=key")
        # Routing is the git remote only — no path/basename guessing in code. History from
        # worktrees that no longer exist has no live remote to identify it, so it pools in
        # `miscellaneous`. Deciding which dead dir was which repo is a judgment call, so we
        # PROMPT the setting-up agent to do it (with aliases) rather than guess here.
        misc = {}
        for (key, _wt, _sid, _day, cwd) in groups:
            if key == "miscellaneous":
                misc.setdefault(os.path.basename(cwd.rstrip("/")), cwd)
        if misc:
            print("\nAGENT: history below is in `miscellaneous` — deleted worktrees with no")
            print("live git remote to identify them. For each one you recognize as a real")
            print("project, find its <owner>__<repo> (run `git -C <a live clone of it> remote")
            print("get-url origin`, or `gh repo list`) and re-run with --alias <dir>=<owner>__<repo>")
            print("to file it there. Leaving the rest in miscellaneous is fine — data is kept.")
            for seg, cwd in sorted(misc.items()):
                print(f"  - {seg}\t(e.g. {cwd})")
        return

    for (key, wt, sid, day, cwd), entries in groups.items():
        if key in exclude:
            continue
        entries.sort(key=lambda x: x[0])
        d = os.path.join(data, "projects", key, "log", day)
        os.makedirs(d, exist_ok=True)
        with open(os.path.join(d, f"{wt}.{sid}.md"), "w") as fh:
            fh.write(f"# {key} — {day} — session {sid}\n\n")
            fh.write("> devbrain Stage A raw prompt log. Append-only, source of truth.\n")
            fh.write(f"> worktree: {wt} · cwd: {cwd} · times in UTC\n>\n{BANNER}\n")
            for dt, prompt, resp_dt, summary, meta in entries:
                fh.write(f"## {dt.strftime('%H:%M:%S')}\n\n{prompt}\n\n")
                if summary:
                    fh.write(f"↳ {resp_dt.strftime('%H:%M:%S')} — {summary}\n")
                    if meta:
                        fh.write(f"   {meta}\n")
                    fh.write("\n")
            total_files += 1
    for key, files in memory.items():
        if key in exclude:
            continue
        d = os.path.join(data, "projects", key, "memory")
        os.makedirs(d, exist_ok=True)
        for name, text in files.items():
            with open(os.path.join(d, name), "w") as fh:
                fh.write(text)
    # ---- token sidecars (append-only, idempotent: skip sessions already recorded) ----
    for key, recs in token_recs.items():
        if key in exclude:
            continue
        d = os.path.join(data, "projects", key)
        os.makedirs(d, exist_ok=True)
        sidecar = os.path.join(d, "tokens.jsonl")
        seen = set()
        if os.path.exists(sidecar):
            for line in open(sidecar, encoding="utf-8", errors="replace"):
                try:
                    seen.add(json.loads(line).get("session"))
                except Exception:
                    pass
        fresh = [r for r in recs if r["session"] not in seen]
        if fresh:
            with open(sidecar, "a") as fh:
                for r in sorted(fresh, key=lambda r: r["ts"]):
                    fh.write(json.dumps(r) + "\n")
    print(f"\nApplied. Wrote logs for {len([k for k in keys if k not in exclude])} projects + memory stores into {data}.")
    print("Next: run /distill (or /continue) per project to fold this into searchable brain pages.")

if __name__ == "__main__":
    main()
