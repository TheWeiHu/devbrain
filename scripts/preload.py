#!/usr/bin/env python3
"""devbrain — preload (onboard your existing prompt history into the brain).

Replays the Claude Code transcripts already on this machine
(`~/.claude/projects/*/*.jsonl`) through the SAME log layout + redaction the live
`UserPromptSubmit` hook (hooks/capture.sh) uses — so a fresh devbrain install isn't
empty. preload writes raw prompt log (the source of truth); you then `/distill` (or
`/continue`) per project to fold it into brain pages.

Standalone and idempotent: re-run any time — entries already logged are skipped.

  devbrain-preload                      # import ALL history, then tells you what to /distill
  devbrain-preload --since 2026-01-01   # only prompts on/after this UTC day
  devbrain-preload --project owner__repo[,owner__repo2]   # only these project keys
  devbrain-preload --from DIR           # transcripts dir (default ~/.claude/projects)
  devbrain-preload --dry-run            # report what WOULD import; write nothing

Identity: live repos resolve via the shared project-key.sh (the key capture.sh /
todo.sh use). Most transcripts, though, point at deleted Conductor workspaces /
worktrees — so we LEARN each project's owner__repo key from the cwds that are still
live, then map the dead paths onto it (Conductor `workspaces/<project>/…` and
`<repo>-wN`/`-vN` worktree names). Secret-redaction mirrors hooks/capture.sh.
"""
import argparse, json, os, re, subprocess, sys
from collections import defaultdict

DATA = os.environ.get("DEVBRAIN_DATA", os.path.expanduser("~/devbrain-data"))
HERE = os.path.dirname(os.path.abspath(__file__))

PK = next((p for p in [
    os.path.join(HERE, "devbrain-project-key.sh"),
    os.path.join(HERE, "..", "hooks", "project-key.sh"),
    os.path.expanduser("~/.claude/hooks/devbrain-project-key.sh"),
] if os.path.exists(p)), None)

# Mirrors hooks/capture.sh redact() — high-confidence secret prefixes only.
REDACT = [
    (re.compile(r"sk-[A-Za-z0-9_-]{20,}"), "[REDACTED]"),
    (re.compile(r"gh[pousr]_[A-Za-z0-9]{20,}"), "[REDACTED]"),
    (re.compile(r"github_pat_[A-Za-z0-9_]{20,}"), "[REDACTED]"),
    (re.compile(r"(?:AKIA|ASIA)[0-9A-Z]{16}"), "[REDACTED]"),
    (re.compile(r"xox[baprs]-[A-Za-z0-9-]{10,}"), "[REDACTED]"),
    (re.compile(r"(Bearer )[A-Za-z0-9._-]{16,}"), r"\1[REDACTED]"),
]
# Whole injected blocks to drop (Conductor bootstrap, harness reminders, command
# stdout) — a message that is ONLY these isn't a human prompt.
BLOCK = re.compile(r"<(system_instruction|system-reminder|local-command-stdout)>.*?</\1>", re.S)
WRAP = re.compile(r"</?(command-message|command-args|bash-stdout|bash-stderr)[^>]*>")
CMDNAME = re.compile(r"<command-name>([^<]*)</command-name>")
CMDARGS = re.compile(r"<command-args>([^<]*)</command-args>")
CONDUCTOR = re.compile(r"/conductor/workspaces/([^/]+)/")
WORKTREE_SUFFIX = re.compile(r"-(?:w|v)\d+$|-main$")


def sanitize(s):  # matches capture.sh / project-key.sh sanitize()
    return re.sub(r"[^a-z0-9._-]", "", (s or "").lower().replace(" ", "-"))


# ── project identity ─────────────────────────────────────────────────────────
known = {}               # sanitized repo-basename -> canonical owner__repo key
_live = {}               # cwd -> owner__repo (or None)


def learn(key):
    base = key.split("__", 1)[1] if "__" in key else key
    known.setdefault(sanitize(base), key)


def resolve_live(cwd):
    """owner__repo from the git remote if the dir still exists, else None."""
    if cwd in _live:
        return _live[cwd]
    key = None
    if PK and cwd and os.path.isdir(cwd):
        try:
            r = subprocess.run(["bash", "-c", f'. "{PK}"; devbrain_project_key "$1"', "_", cwd],
                               capture_output=True, text=True, timeout=10)
            out = r.stdout.strip()
            if out and out != "miscellaneous":
                key = out
                learn(key)
        except Exception:
            pass
    _live[cwd] = key
    return key


def recover_from_path(cwd):
    """Dead dir: guess the project from the path, matched against learned keys."""
    cands = []
    m = CONDUCTOR.search(cwd)
    if m:
        cands.append(m.group(1))                       # conductor/workspaces/<project>/…
    cands.append(WORKTREE_SUFFIX.sub("", os.path.basename(cwd.rstrip("/"))))  # <repo>-w0 / -v1
    for c in cands:
        k = known.get(sanitize(c))
        if k:
            return k
    return None


def project_of(cwd):
    return resolve_live(cwd) or recover_from_path(cwd) or "miscellaneous"


_wt = {}
def worktree(cwd):
    if cwd in _wt:
        return _wt[cwd]
    tl = ""
    if os.path.isdir(cwd):
        try:
            tl = subprocess.run(["git", "-C", cwd, "rev-parse", "--show-toplevel"],
                                capture_output=True, text=True, timeout=10).stdout.strip()
        except Exception:
            pass
    wt = sanitize(os.path.basename(tl or cwd)) or "unknown"
    _wt[cwd] = wt
    return wt


# ── prompt extraction ────────────────────────────────────────────────────────
def redact(t):
    for rx, repl in REDACT:
        t = rx.sub(repl, t)
    return t


def prompt_text(msg):
    """The human's typed prompt, or '' for tool-result / non-text lines."""
    c = msg.get("content")
    if isinstance(c, str):
        t = c
    elif isinstance(c, list):
        t = "\n".join(b.get("text", "") for b in c if isinstance(b, dict) and b.get("type") == "text")
    else:
        return ""
    m = CMDNAME.search(t)
    if m:                      # a typed slash command → "/continue" + any args
        a = CMDARGS.search(t)
        return (m.group(1).strip() + (" " + a.group(1).strip() if a and a.group(1).strip() else "")).strip()
    return WRAP.sub("", BLOCK.sub("", t)).strip()   # drop injected blocks; keep human text


def main():
    ap = argparse.ArgumentParser(prog="devbrain-preload",
                                 description="Onboard existing Claude Code prompt history into the devbrain log.")
    ap.add_argument("--since", metavar="YYYY-MM-DD", help="only prompts on/after this UTC day")
    ap.add_argument("--project", help="only these project keys (comma-separated owner__repo)")
    ap.add_argument("--from", dest="src", default=os.path.expanduser("~/.claude/projects"),
                    help="transcripts dir (default ~/.claude/projects)")
    ap.add_argument("--dry-run", action="store_true", help="report what would import; write nothing")
    args = ap.parse_args()

    if not os.path.isdir(args.src):
        sys.exit(f"preload: no transcripts dir at {args.src}")
    only = set(p.strip() for p in args.project.split(",")) if args.project else None

    # Seed learned keys from any project folders that already exist (helps re-runs).
    pdir = os.path.join(DATA, "projects")
    if os.path.isdir(pdir):
        for d in os.listdir(pdir):
            if "__" in d:
                learn(d)

    files = []
    for root, _, names in os.walk(args.src):
        files += [os.path.join(root, n) for n in names if n.endswith(".jsonl")]

    # Pass 1: pull qualifying prompts AND learn live keys (so dead paths can recover).
    entries = []   # (cwd, day, tm, sid, text)
    skipped = 0
    for f in sorted(files):
        try:
            fh = open(f, errors="replace")
        except Exception:
            continue
        for line in fh:
            try:
                e = json.loads(line)
            except Exception:
                continue
            if e.get("type") != "user" or e.get("isMeta") or e.get("isSidechain"):
                continue
            text = prompt_text(e.get("message") or {})
            if not text:
                skipped += 1
                continue
            ts = e.get("timestamp") or ""
            if len(ts) < 19:
                continue
            day = ts[:10]
            if args.since and day < args.since:
                skipped += 1
                continue
            cwd = e.get("cwd") or ""
            resolve_live(cwd)            # learn key if this dir is still live
            entries.append((cwd, day, ts[11:19], sanitize(e.get("sessionId") or "nosession") or "nosession", text))

    # Pass 2: assign final project (now that all live keys are learned), then write.
    seen_times = {}
    new_by_proj = defaultdict(int)
    imported = 0
    for cwd, day, tm, sid, text in entries:
        proj = project_of(cwd)
        if only and proj not in only:
            skipped += 1
            continue
        path = f"{DATA}/projects/{proj}/log/{day}/{worktree(cwd)}.{sid}.md"
        if path not in seen_times:
            existing = set()
            if os.path.exists(path):
                try:
                    existing = set(l[3:].strip() for l in open(path, errors="replace") if l.startswith("## "))
                except Exception:
                    pass
            seen_times[path] = existing
        if tm in seen_times[path]:        # already logged (live capture or prior run)
            skipped += 1
            continue
        seen_times[path].add(tm)
        imported += 1
        new_by_proj[proj] += 1
        if args.dry_run:
            continue
        os.makedirs(os.path.dirname(path), exist_ok=True)
        fresh = not os.path.exists(path)
        with open(path, "a") as out:
            if fresh:
                out.write(f"# {proj} — {day} — session {sid}\n\n")
                out.write("> devbrain Stage A raw prompt log (preloaded). Append-only, source of truth.\n")
                out.write(f"> worktree: {worktree(cwd)} · cwd: {cwd} · times in UTC\n\n")
            out.write(f"## {tm}\n\n{redact(text)}\n\n")

    verb = "would import" if args.dry_run else "imported"
    print(f"preload: scanned {len(files)} transcript(s) → {verb} {imported} prompt(s), "
          f"skipped {skipped}, across {len(new_by_proj)} project(s)")
    for proj, n in sorted(new_by_proj.items(), key=lambda kv: -kv[1]):
        print(f"   {n:>5}  {proj}")
    if imported and not args.dry_run:
        print("\npreload populated the raw LOG. Fold it into the brain by running /distill")
        print("(or /continue) inside each project above — that's what turns log into pages.")


if __name__ == "__main__":
    main()
