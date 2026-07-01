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
from devbrain_lib import (redact, is_synthetic, recap, remote_to_key,  # noqa: E402
                          transcript_turns, codex_session_id)

def sanitize(s):
    return re.sub(r"[^a-z0-9._-]", "", s.lower().replace(" ", "-"))

def git_remote(path):
    try:
        return subprocess.run(["git", "-C", path, "remote", "get-url", "origin"],
                              capture_output=True, text=True, timeout=5).stdout.strip()
    except Exception:
        return ""

def match_known(cwd, known, renames=None):
    """Route a DEAD worktree (no live remote) to an existing project by matching its
    path segments against the projects you already have. `known` is {repo-name: key}.
    `renames` is the user's alias map ({old-dir-name: project-key}, from $DATA/.import-
    aliases) — applied at the segment level so a renamed-then-deleted repo still routes.
    Strict — exact or `repo-` prefix only (so a short dir name never matches a longer
    repo that merely starts the same); strips nightshift/drain `-w<N>` + `-v<N>`
    suffixes; longest repo wins. Confidence is `medium`, never overrides a live remote."""
    renames = renames or {}
    best_key, best_len = None, -1
    for segment in cwd.strip("/").split("/"):
        bare = re.sub(r"-(w\d+|v\d+)$", "", segment)   # drop worker/variant suffix
        # a user rename maps an old dir name straight to a project key
        renamed = renames.get(segment) or renames.get(bare)
        if renamed:
            if len(segment) > best_len:
                best_key, best_len = renamed, len(segment)
            continue
        # otherwise match the segment against an existing repo-name (longest wins)
        for repo in sorted(known, key=len, reverse=True):
            if segment == repo or bare == repo or segment.startswith(repo + "-") or bare.startswith(repo + "-"):
                if len(repo) > best_len:
                    best_key, best_len = known[repo], len(repo)
                break
    return best_key

def route(cwd, aliases, known=None):
    """Identity from the git remote first; then a user alias; then (for dead worktrees)
    a strict match against existing projects. Returns (key, confidence)."""
    # 1. live path with a remote (still-present repo / worktree) — exact.
    if os.path.isdir(cwd):
        k = remote_to_key(git_remote(cwd))
        if k:
            return (k, "high")
    # 2. explicit alias for the trailing dir name (user-declared renames).
    seg = os.path.basename(cwd.rstrip("/"))
    if seg in aliases:
        return (aliases[seg], "high")
    # 3. dead worktree with no remote/alias — match its path against known projects,
    #    so a nightshift/drain/conductor worktree lands in its real project, not the
    #    shared bucket. Skipped when `known` is not supplied (back-compat).
    if known:
        k = match_known(cwd, known, aliases)
        if k:
            return (k, "medium")
    # 4. truly unresolved -> shared bucket. Data is kept; add an alias to file it.
    return ("miscellaneous", "low")

def iso(s):
    return datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))

def iso_or(s, fallback=None):
    try:
        return iso(s)
    except Exception:
        return fallback or datetime.datetime.fromtimestamp(0, datetime.timezone.utc)

def parse_transcript(path):
    out = []
    for c in transcript_turns(path):
        meta = []
        if c["files"]:
            meta.append("touched: " + ", ".join(c["files"]))
        if c["tools"]:
            meta.append("tools: " + ", ".join(f"{k}×{v}" for k, v in c["tools"].items()))
        dt = iso_or(c["dt"])
        out.append({"dt": dt, "cwd": c["cwd"], "prompt": redact(c["prompt"]),
                    "resp_dt": iso_or(c["turn_ts"], dt), "summary": redact(recap(c["texts"])),
                    "meta": redact("  ·  ".join(meta)),
                    "input": c["input"], "output": c["output"],
                    "cache_create": c["cache_create"], "cache_read": c["cache_read"],
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
    ap.add_argument("--codex", default=os.environ.get("CODEX_HOME", os.path.expanduser("~/.codex")))
    ap.add_argument("--exclude", default="", help="comma-separated project keys to skip")
    ap.add_argument("--alias", action="append", default=[], help="OLD=key rename (repeatable)")
    ap.add_argument("--no-memory", action="store_true", help="skip the memory/ harvest")
    ap.add_argument("--tokens-only", action="store_true",
                    help="only write the token sidecars (no prompt logs / memory) — for "
                         "backfilling cost history on an existing install without re-adding logs")
    args = ap.parse_args()

    data, claude, codex = args.data, args.claude, args.codex
    exclude = {x for x in args.exclude.split(",") if x}

    # Aliases for renames the git remote can't show (an old dir name → a project key).
    # Persistent ones live in $DATA/import-aliases (OLD=key per line); --alias wins.
    aliases = {}
    alias_file = os.path.join(data, "import-aliases")
    if not os.path.exists(alias_file):                       # back-compat: the file used to be hidden
        legacy = os.path.join(data, ".import-aliases")
        if os.path.exists(legacy):
            alias_file = legacy
    if os.path.exists(alias_file):
        for line in open(alias_file, encoding="utf-8", errors="replace"):
            line = line.split("#", 1)[0].strip()
            if "=" in line:
                o, k = line.split("=", 1)
                aliases[o.strip()] = k.strip()
    aliases.update(a.split("=", 1) for a in args.alias if "=" in a)

    live = live_sessions(data)
    existing = set(os.listdir(os.path.join(data, "projects"))) if os.path.isdir(os.path.join(data, "projects")) else set()
    # Vocabulary for routing dead worktrees: {repo-name: <owner>__<repo>} from the
    # projects you already have. Lets match_known() file a no-remote worktree into its
    # real project instead of miscellaneous.
    known = {d.split("__", 1)[1]: d for d in existing if "__" in d}

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
        key, conf = route(cwd, aliases, known)
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
            if t["input"] or t["output"] or t["cache_create"] or t["cache_read"]:
                key, _ = route(t["cwd"], aliases, known)
                cwd = t["cwd"]
                auto = bool(re.search(r"/(nightshift|drain)/", cwd)
                            or re.search(r"-w\d+(/|$)", cwd))   # autonomous worker session
                token_recs[key].append({
                    "ts": t["resp_dt"].strftime("%Y-%m-%dT%H:%M:%SZ"), "session": sid,
                    "model": t["model"], "in": t["input"], "out": t["output"],
                    "cache_create": t["cache_create"], "cache_read": t["cache_read"], "auto": auto})

    # Codex stores token usage as one event per model request in ~/.codex/sessions.
    # The sidecar's public shape is still one row per user turn, matching Claude Code
    # and the live Stop hook; transcript_turns() owns that aggregation for both paths.
    codex_replace_sessions = set()
    codex_glob = os.path.join(codex, "sessions", "*", "*", "*", "*.jsonl")
    for path in glob.glob(codex_glob):
        sid = codex_session_id(path)
        try:
            turns = parse_transcript(path)
        except Exception:
            continue
        # Log harvest mirrors the Claude loop: Codex sessions were never captured live
        # (their UserPromptSubmit hook is newer) and no other path imports their prompts,
        # so the prompt/response/tools (incl. Skill:<name>) live only in the transcript.
        is_live = sid in live          # a BACKFILLED-marked log is NOT live -> re-import it
        if turns and not is_live:
            done_sessions.add(sid)
        for t in turns:
            if not is_live:
                add_entry(t["cwd"], sid, t["dt"], t["prompt"], t["resp_dt"], t["summary"], t["meta"])
            if not (t["input"] or t["output"] or t["cache_create"] or t["cache_read"]):
                continue
            model = t["model"] or ""
            if not (model.startswith("gpt-") or "codex" in model):
                continue
            key, _ = route(t["cwd"], aliases, known)
            auto = bool(re.search(r"/(nightshift|drain)/", t["cwd"])
                        or re.search(r"-w\d+(/|$)", t["cwd"]))
            token_recs[key].append({
                "ts": t["resp_dt"].strftime("%Y-%m-%dT%H:%M:%SZ"), "session": sid,
                "model": model, "in": t["input"], "out": t["output"],
                "cache_create": t["cache_create"], "cache_read": t["cache_read"],
                "auto": auto})
            if key not in exclude:
                codex_replace_sessions.add(sid)

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
            key, kconf = route(cwd, aliases, known)
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

    # --tokens-only skips the prompt-log + memory writes (only the token sidecars below),
    # so an existing install can backfill cost history without re-adding BACKFILLED logs.
    if not args.tokens_only:
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
    # Dedup per (session, ts) GLOBALLY, across every project's sidecar — not just the target
    # one. A session's routing can change between live capture and this backfill (its worktree
    # was deleted, or its remote now resolves differently), so a turn already recorded under
    # project A must NOT be re-added under project B. Both writers stamp a turn with its
    # RESPONSE timestamp, so a session captured live partway through still backfills its
    # earlier turns here — wherever they were first filed.
    if codex_replace_sessions:
        for sc in glob.glob(os.path.join(data, "projects", "*", "tokens.jsonl")):
            try:
                rows = open(sc, encoding="utf-8", errors="replace").read().splitlines()
            except OSError:
                continue
            kept = []
            changed = False
            for line in rows:
                try:
                    e = json.loads(line)
                except Exception:
                    kept.append(line)
                    continue
                model = e.get("model") or ""
                if e.get("session") in codex_replace_sessions and (model.startswith("gpt-") or "codex" in model):
                    changed = True
                    continue
                kept.append(line)
            if changed:
                with open(sc, "w", encoding="utf-8") as fh:
                    if kept:
                        fh.write("\n".join(kept) + "\n")

    seen = set()
    for sc in glob.glob(os.path.join(data, "projects", "*", "tokens.jsonl")):
        for line in open(sc, encoding="utf-8", errors="replace"):
            try:
                e = json.loads(line)
                seen.add((e.get("session"), e.get("ts")))
            except Exception:
                pass
    for key, recs in token_recs.items():
        if key in exclude:
            continue
        d = os.path.join(data, "projects", key)
        os.makedirs(d, exist_ok=True)
        sidecar = os.path.join(d, "tokens.jsonl")
        fresh = [r for r in recs if (r["session"], r["ts"]) not in seen]
        if fresh:
            with open(sidecar, "a") as fh:
                for r in sorted(fresh, key=lambda r: r["ts"]):
                    fh.write(json.dumps(r) + "\n")
                    seen.add((r["session"], r["ts"]))   # guard against intra-run cross-route dups
    tok_keys = sorted(k for k in token_recs if k not in exclude)
    if args.tokens_only:
        print(f"\nApplied (tokens-only). Wrote token sidecars for {len(tok_keys)} projects: "
              f"{', '.join(tok_keys)}")
    else:
        print(f"\nApplied. Wrote logs for {len([k for k in keys if k not in exclude])} projects + memory stores into {data}.")
        print("Next: run /distill (or /continue) per project to fold this into searchable brain pages.")

if __name__ == "__main__":
    main()
