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

Routing: a cache only records the cwd, and most of those dirs are gone. We recover the
<owner>__<repo> identity with a cascade (live git remote -> alias -> basename matched
against live clones on disk -> miscellaneous). Identity is the git remote — the same
harness-agnostic rule as project-key.sh — so there is no per-harness path parsing.

Shared rules (redaction, synthetic-prompt filter, the merged-#15 recap, remote_to_key)
are NOT re-implemented here — they live once in hooks/devbrain_lib.py and are imported
below, the same definitions the live bash hooks call (via its CLI). So the produced logs
are byte-compatible with live capture by construction, with no copy to keep in sync.
"""
import argparse, json, os, re, glob, shutil, subprocess, datetime, collections, sys

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

def build_remote_index(roots, depth=4):
    """basename -> <owner>__<repo>, from a scan of live clones on disk. This is how a
    DELETED worktree's <project> segment / basename is recovered to a real identity."""
    index = {}
    for root in roots:
        root = os.path.expanduser(root)
        if not os.path.isdir(root):
            continue
        for dirpath, dirs, _ in os.walk(root):
            d = dirpath[len(root):].count(os.sep)
            if d >= depth:
                dirs[:] = []
                continue
            if ".git" in dirs:
                key = remote_to_key(git_remote(dirpath))
                if key:
                    index.setdefault(os.path.basename(dirpath), key)
                dirs[:] = [x for x in dirs if x != ".git"]
    return index

def gh_remote_index():
    """basename -> <owner>__<repo> from the user's GitHub repos (own + orgs), to recover
    repos that are DELETED/uncloned but still exist — which the on-disk scan can't see.
    Best-effort: needs `gh` authed; skipped silently otherwise. Lists CURRENT names, so a
    *renamed* repo (RedditPages -> redlens) still needs an alias, not this."""
    index = {}
    if not shutil.which("gh"):
        return index
    targets = [None]   # None = the authenticated user's own repos
    try:
        orgs = subprocess.run(["gh", "api", "user/orgs", "--jq", ".[].login"],
                              capture_output=True, text=True, timeout=10).stdout.split()
        targets += orgs
    except Exception:
        pass
    for owner in targets:
        try:
            args = ["gh", "repo", "list"] + ([owner] if owner else []) + \
                   ["--limit", "1000", "--json", "nameWithOwner"]
            out = subprocess.run(args, capture_output=True, text=True, timeout=20).stdout
            for r in json.loads(out or "[]"):
                nwo = r.get("nameWithOwner", "")
                if "/" in nwo:
                    o, repo = nwo.split("/", 1)
                    index.setdefault(repo, remote_to_key(f"https://github.com/{o}/{repo}"))
        except Exception:
            pass
    return index

def route(cwd, remote_index, aliases):
    """Resolution cascade -> (key, confidence)."""
    # 1. live path with a remote (still-present repo / worktree) — exact. Identity
    #    comes from the git remote only, the same harness-agnostic rule as project-key.sh.
    if os.path.isdir(cwd):
        k = remote_to_key(git_remote(cwd))
        if k:
            return (k, "high")
    # 2. dead path (deleted worktree) — best-effort from the trailing dir name.
    seg = os.path.basename(cwd.rstrip("/"))
    # 3. alias (renames a scan can't infer, e.g. RedditPages -> redlens).
    if seg in aliases:
        return (aliases[seg], "high")
    # 4. basename matched against live clones on disk.
    if seg in remote_index:
        return (remote_index[seg], "medium")
    # 5. unresolved -> shared bucket.
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
                   "texts": [], "tools": {}, "files": {}, "resp_dt": None}
        elif t == "assistant" and cur is not None:
            cur["resp_dt"] = iso(e["timestamp"])
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
                    "meta": redact("  ·  ".join(meta))})
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
    ap.add_argument("--roots", default="~,~/Desktop,~/Downloads,~/Dropbox,~/conductor",
                    help="comma-separated roots to scan for live clones (routing)")
    ap.add_argument("--no-memory", action="store_true", help="skip the memory/ harvest")
    ap.add_argument("--no-gh", action="store_true", help="skip the `gh repo list` routing fallback")
    args = ap.parse_args()

    data, claude = args.data, args.claude
    exclude = {x for x in args.exclude.split(",") if x}

    # Aliases for renames a scan/gh can't infer (e.g. RedditPages -> redlens). Persistent
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

    # Routing index: live clones on disk first (ground truth), then GitHub fills gaps for
    # repos that exist but aren't cloned here (deleted/abandoned worktrees).
    remote_index = build_remote_index(args.roots.split(","))
    if not args.no_gh:
        for name, key in gh_remote_index().items():
            remote_index.setdefault(name, key)
    live = live_sessions(data)
    existing = set(os.listdir(os.path.join(data, "projects"))) if os.path.isdir(os.path.join(data, "projects")) else set()

    # ---- harvest logs (transcripts primary, history.jsonl fallback) ----
    transcripts = {os.path.basename(f)[:-6]: f for f in glob.glob(os.path.join(claude, "projects", "*", "*.jsonl"))}
    transcripts = {s: p for s, p in transcripts.items() if s not in live}
    groups = {}
    n_prompts = collections.defaultdict(int)
    n_resp = collections.defaultdict(int)
    n_mem = collections.defaultdict(int)
    conf_of = collections.defaultdict(lambda: "low")
    ORDER = {"high": 3, "medium": 2, "low": 1}
    done_sessions = set()

    def add_entry(cwd, sid, dt, prompt, resp_dt=None, summary=None, meta=None):
        key, conf = route(cwd, remote_index, aliases)
        wt = sanitize(os.path.basename(cwd.rstrip("/"))) or "unknown"
        gk = (key, wt, sanitize(sid) or "nosession", dt.strftime("%Y-%m-%d"), cwd)
        groups.setdefault(gk, []).append((dt, prompt, resp_dt, summary, meta))
        n_prompts[key] += 1
        if summary:
            n_resp[key] += 1
        if ORDER[conf] > ORDER[conf_of[key]]:
            conf_of[key] = conf

    for sid, path in transcripts.items():
        try:
            turns = parse_transcript(path)
        except Exception:
            continue
        if not turns:
            continue
        done_sessions.add(sid)
        for t in turns:
            add_entry(t["cwd"], sid, t["dt"], t["prompt"], t["resp_dt"], t["summary"], t["meta"])

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
            key, kconf = route(cwd, remote_index, aliases)
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
    print(f"\nApplied. Wrote logs for {len([k for k in keys if k not in exclude])} projects + memory stores into {data}.")
    print("Next: run /distill (or /continue) per project to fold this into searchable brain pages.")

if __name__ == "__main__":
    main()
