#!/usr/bin/env python3
"""devbrain queue — a small localhost kanban for the TODO queue.

The queue is one markdown file per task with YAML-ish frontmatter:
    $DEVBRAIN_DATA/projects/<project>/todo/<id>.md
A static page can't write those files back, so this is a tiny stdlib-only HTTP
server that serves the kanban UI and reads/writes the .md files DIRECTLY —
preserving frontmatter key order. No CLI, no deps. Binds 127.0.0.1 only.

  devbrain queue [--port N] [--no-open] [--data DIR]

It does NOT git-commit; review with `git -C ~/devbrain-data diff` and let the
devbrain flusher commit as usual.
"""
import os, re, sys, glob, json, errno, shlex, argparse, datetime, webbrowser, subprocess
from urllib.parse import urlparse, parse_qs
from urllib.request import urlopen
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
STATUSES = ["open", "taken", "review", "held", "done"]

def now():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


# --- prompt-log reader (powers the /api/prompts self-portrait view) ----------
# Stage-A raw logs live at projects/<proj>/log/<date>/<worktree>.<session>.md as
# "## HH:MM:SS\n\n<prompt>\n[↳ response summary]" blocks, under a header line:
#   > worktree: <wt> · cwd: <path> · times in UTC
# Classification is two-signal:
#  1) SESSION ORIGIN (the reliable one) — nightshift runs each worker in a throwaway
#     git worktree under ~/nightshift/ or ~/drain/, named <project>-w<N>. Any prompt
#     from such a session is autonomous ("nightshift"), no matter what the text says —
#     this is what catches the /continue loop that text-matching missed.
#  2) TEXT (for interactive sessions) — slash-command = "command" (you ran it),
#     harness injections = "system", title-gen, else "human" (what you typed).
# Toggle groups: TYPED = {human, command} (you, at the keyboard); BOT = everything else.
_PROMPT_RE = re.compile(r"^## (\d{2}:\d{2}:\d{2})\s*$")
_HEADER_RE = re.compile(r"worktree:\s*(\S+).*?cwd:\s*(\S+)")
_NS_CWD = re.compile(r"/(?:nightshift|drain)/")
_NS_WT = re.compile(r"-w\d+$")
_TYPED_KINDS = ("human", "command")

def session_is_autonomous(cwd, worktree):
    """True for a nightshift worker session — by its worktree path / name."""
    return bool(_NS_CWD.search(cwd or "") or _NS_WT.search(worktree or ""))

def classify(s, autonomous=False):
    """Kind for a prompt, or None to skip (empty). autonomous=True forces a
    keyboard turn (human/command) to 'nightshift' — the session was a bot."""
    s = s.strip()
    if not s:
        return None
    if s.startswith(("<system_instruction>", "<local-command-caveat>", "<command-", "<task-notification>")):
        return "system"
    if s.startswith("You are generating a short conversation title"):
        return "title-gen"
    if "Caveat: The messages below were generated" in s[:200]:
        return "system"
    if s.startswith("PLANNING TURN:") or s.startswith(("Check in on the nightshift", "Check on the nightshift")):
        return "nightshift"
    kind = "command" if s.startswith("/") else "human"
    return "nightshift" if autonomous else kind

def scan_prompts(data_dir, days=30, project=None):
    """Every prompt in the window, each tagged with its classify() kind."""
    base = os.path.join(data_dir, "projects")
    cutoff = ((datetime.date.today() - datetime.timedelta(days=days)).isoformat()
              if days else "0000-00-00")
    out = []
    for md in glob.glob(os.path.join(base, "*", "log", "*", "*.md")):
        parts = md.split(os.sep)
        date, proj = parts[-2], parts[-4]
        sess = parts[-1][:-3]               # <worktree>.<session-id> — one agent session (for concurrency)
        if date < cutoff or (project and proj != project):
            continue
        try:
            lines = open(md, encoding="utf-8", errors="replace").read().splitlines()
        except OSError:
            continue
        auton = False
        for l in lines[:6]:
            h = _HEADER_RE.search(l)
            if h:
                auton = session_is_autonomous(h.group(2), h.group(1)); break
        i = 0
        while i < len(lines):
            m = _PROMPT_RE.match(lines[i])
            if not m:
                i += 1; continue
            ts, body, j = m.group(1), [], i + 1
            while j < len(lines) and not _PROMPT_RE.match(lines[j]) and not lines[j].lstrip().startswith("↳"):
                body.append(lines[j]); j += 1
            text = "\n".join(body).strip()
            kind = classify(text, auton)
            if kind:
                try:
                    dt = datetime.datetime.strptime(f"{date} {ts}", "%Y-%m-%d %H:%M:%S")
                    out.append({"p": proj, "s": sess, "date": date, "time": ts[:5], "dt": dt.isoformat(),
                                "h": dt.hour, "wd": dt.strftime("%a"), "c": len(text),
                                "w": len(text.split()), "x": text, "kind": kind})
                except ValueError:
                    pass
            i = j
    out.sort(key=lambda r: r["dt"])
    return out

def project_repo(data_dir, project):
    """Best-effort local checkout path for a project, read from the `cwd:` in its most
    recent INTERACTIVE (non-nightshift) session-log header. Nightshift workers run in
    throwaway worktrees, so those cwds are skipped — we want the real working clone. The
    dashboard needs this to launch nightshift on the right repo from a drag-to-🌙, since
    the queue server otherwise only knows the project KEY, not where it lives on disk.
    Returns an existing git checkout dir, or None."""
    pat = os.path.join(data_dir, "projects", project, "log", "*", "*.md")
    for md in sorted(glob.glob(pat), key=os.path.getmtime, reverse=True):
        try:
            head = open(md, encoding="utf-8", errors="replace").read(2000)
        except OSError:
            continue
        h = _HEADER_RE.search(head)
        if not h:
            continue
        wt, cwd = h.group(1), h.group(2)
        if session_is_autonomous(cwd, wt):
            continue
        if os.path.exists(os.path.join(cwd, ".git")):   # .git is a file in a linked worktree, dir in a clone
            return cwd
    return None

def now_epoch():
    return datetime.datetime.now(datetime.timezone.utc).timestamp()

def nightshift_running(repo):
    """True if a nightshift orchestrator is already running on this repo (pgrep on its argv)."""
    try:
        return subprocess.run(["pgrep", "-f", f"nightshift-orchestrate.sh --repo {repo}"],
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
    except OSError:
        return False

# Where dashboard-launched fleets live: a dedicated clone per repo, NOT the active working
# checkout. This keeps nightshift's sibling worktrees out of Conductor workspaces, names them
# after the repo (devbrain-w0, not a workspace codename), and gives the orchestrator a clone
# where `nightshift` isn't checked out elsewhere — so its reset-to-main actually works.
NIGHTSHIFT_HOME = os.environ.get("DEVBRAIN_NIGHTSHIFT_HOME", os.path.expanduser("~/nightshift"))

def git_remote_url(checkout):
    try:
        r = subprocess.run(["git", "-C", checkout, "remote", "get-url", "origin"],
                           capture_output=True, text=True)
        return r.stdout.strip() if r.returncode == 0 else ""
    except OSError:
        return ""

def repo_name_from_url(url):
    """github.com/Owner/devbrain.git -> devbrain ; git@host:owner/repo.git -> repo."""
    base = url.rstrip("/").rsplit("/", 1)[-1].rsplit(":", 1)[-1]
    return base[:-4] if base.endswith(".git") else base

def nightshift_clone_path(checkout):
    """The dedicated clone dir this checkout's project maps to (or None if it has no remote)."""
    url = git_remote_url(checkout)
    if not url:
        return None
    return os.path.join(NIGHTSHIFT_HOME, repo_name_from_url(url))

def ensure_nightshift_clone(checkout):
    """Resolve the isolated clone the fleet should run in, cloning from the remote on first use.
    Returns (repo_dir, note). Falls back to the checkout itself (old behavior) for a remote-less
    repo. (None, error) if a clone is needed but fails or the dir collides with another remote."""
    url = git_remote_url(checkout)
    if not url:
        return checkout, "no git remote — running in the checkout in place"
    dest = os.path.join(NIGHTSHIFT_HOME, repo_name_from_url(url))
    if os.path.exists(os.path.join(dest, ".git")):
        if git_remote_url(dest) == url:
            return dest, "reused dedicated clone"
        return None, f"{dest} exists but points at a different remote — move it aside"
    try:
        os.makedirs(NIGHTSHIFT_HOME, exist_ok=True)
        r = subprocess.run(["git", "clone", "--quiet", url, dest], capture_output=True, text=True)
    except OSError as e:
        return None, f"could not clone: {e}"
    if r.returncode != 0:
        return None, f"clone failed: {(r.stderr or '').strip()[:200]}"
    return dest, "cloned a fresh dedicated checkout"

def _filter_kind(recs, kind):
    if kind == "all":
        return recs
    if kind == "bot":
        return [r for r in recs if r["kind"] not in _TYPED_KINDS]
    return [r for r in recs if r["kind"] in _TYPED_KINDS]       # default: typed

def parse_prompts(data_dir, days=30, project=None, kind="typed"):
    return _filter_kind(scan_prompts(data_dir, days, project), kind)


# --- gbrain read/value log (powers the "Brain Value" card) ------------------
# Each project keeps projects/<proj>/gbrain-queries.log as JSONL:
#   {"ts","project","cmd","modes":[...],"hits":N,"slugs":[...]}
# modes are the gbrain verbs that ran; reads = search/query/get. "hits" + which
# "slugs" keep surfacing = how much real answer the brain returned.
_GB_READ = {"search", "query", "get"}
_GB_TOPIC = re.compile(r'gbrain\s+(?:search|query)\s+"([^"]{2,140})"')


# A real gbrain page is always `<project>/<page>` (the brain is one namespace, so a
# bare slug is page_not_found). Requiring the slash keeps prose that merely mentions
# "gbrain get as a hit" from surfacing "as" as the page someone tried to read.
_GB_SLUG = re.compile(r'[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._/-]+')


_GB_PUNCT = "();<>|&`"   # default shlex punctuation plus backtick


def _gb_page_arg(seq):
    """First real page argument after `get`: skip flags + bare redirection fds (the
    2 that punctuation_chars splits out of `2>&1`), credit a variable expansion as an
    unknowable read, and stop at any shell control token. Option-only get -> ""."""
    for t in seq:
        if not t or t.startswith("-") or t.isdigit():
            continue
        if t.startswith("$"):       # $page / ${page}: real read, slug unknowable
            return t                # (the slug-shape filter drops it -> generic label)
        if any(c in t for c in "<>&|;(){}"):
            return ""
        return t
    return ""


def _gb_tok(s):
    try:
        lex = shlex.shlex(s, posix=True, punctuation_chars=_GB_PUNCT)
        lex.whitespace_split = True
        lex.commenters = ""
        return list(lex)
    except ValueError:
        return None


def _gb_scan(toks):
    """adjacency: a bare or path-prefixed `gbrain get` as two tokens -> its page."""
    for i, t in enumerate(toks):
        if i + 1 < len(toks) and t.rsplit("/", 1)[-1] == "gbrain" and toks[i + 1] == "get":
            r = _gb_page_arg(toks[i + 2:])
            if r:
                return r   # skip an option-only get, keep scanning for a real one
    return ""


def gb_get_target(cmd):
    """Best-effort page slug a `gbrain get` tried to read, parsed from the logged
    command text. Display-only — the capture hook owns hit-crediting; this just
    lets the dashboard name the page behind a get (incl. one that returned nothing,
    whose slug never makes it into `slugs`). Returns "" if no plausible page slug is
    found. Mirrors the hook's shlex tokenizing so a quoted query (`search "gbrain
    get x"`) cannot masquerade as a get."""
    if not cmd or "gbrain get " not in cmd:
        return ""
    toks = _gb_tok(cmd)
    if toks is None:
        # Unparseable command (e.g. an unbalanced quote in the collapsed snippet):
        # show the generic label rather than fabricate a page from a string scan.
        return ""
    cand = _gb_scan(toks)
    if not cand:
        # A get can hide in a (possibly quoted) command substitution that shlex keeps
        # whole. Unwrap tokens carrying real substitution syntax ($( or backtick) and
        # re-scan the body; prose (a search arg) has neither, so it stays ignored.
        for t in toks:
            if "$(" in t or "`" in t:
                it = _gb_tok(t.replace("$(", " ").replace("(", " ").replace(")", " ").replace("`", " "))
                if it:
                    cand = _gb_scan(it)
                    if cand:
                        break
    return cand if _GB_SLUG.fullmatch(cand) else ""


def gbrain_queries(data_dir, days=0, project=None):
    base = os.path.join(data_dir, "projects")
    cutoff = ((datetime.date.today() - datetime.timedelta(days=days)).isoformat()
              if days else "0000-00-00")
    out = []
    for f in glob.glob(os.path.join(base, "*", "gbrain-queries.log")):
        proj = f.split(os.sep)[-2]
        if project and proj != project:
            continue
        try:
            lines = open(f, encoding="utf-8", errors="replace").read().splitlines()
        except OSError:
            continue
        for line in lines:
            line = line.strip()
            if not line:
                continue
            try:
                e = json.loads(line)
            except ValueError:
                continue
            ts = e.get("ts", "")
            if ts[:10] < cutoff:
                continue
            modes = e.get("modes") or []
            cmd = e.get("cmd", "") or ""
            topics = _GB_TOPIC.findall(cmd)
            out.append({"ts": ts, "date": ts[:10], "p": proj,
                        "read": any(m in _GB_READ for m in modes),
                        "modes": modes, "hits": e.get("hits", 0) or 0,
                        "slugs": e.get("slugs") or [], "q": topics[0] if topics else "",
                        "target": gb_get_target(cmd) if "get" in modes else ""})
    out.sort(key=lambda r: r["ts"])
    return out


# --- token-usage reader (powers the Profile "Token Cost" card) ---------------
# Each project keeps projects/<proj>/tokens.jsonl as JSONL, one record per turn:
#   {"ts","session","model","in","out","cache_create","cache_read"}
# Both paths write it: the live capture-response hook (new turns) and import.py
# (historical backfill of sessions still on disk). The two never overlap — import
# skips sessions already captured live — but we still dedup on (session, ts) so a
# re-run or a sync can't double-count. The dashboard humanizes + prices these raw
# integers; this reader stays pricing-agnostic (model id flows through untouched).
def token_usage(data_dir, days=0, project=None):
    base = os.path.join(data_dir, "projects")
    cutoff = ((datetime.date.today() - datetime.timedelta(days=days)).isoformat()
              if days else "0000-00-00")
    out, seen = [], set()
    for f in glob.glob(os.path.join(base, "*", "tokens.jsonl")):
        proj = f.split(os.sep)[-2]
        if project and proj != project:
            continue
        try:
            lines = open(f, encoding="utf-8", errors="replace").read().splitlines()
        except OSError:
            continue
        for line in lines:
            line = line.strip()
            if not line:
                continue
            try:
                e = json.loads(line)
            except ValueError:
                continue
            ts = e.get("ts", "")
            if ts[:10] < cutoff:
                continue
            key = (e.get("session"), ts)
            if key in seen:
                continue
            seen.add(key)
            out.append({"ts": ts, "date": ts[:10], "p": proj, "model": e.get("model") or "",
                        "in": e.get("in", 0) or 0, "out": e.get("out", 0) or 0,
                        "cc": e.get("cache_create", 0) or 0, "cr": e.get("cache_read", 0) or 0,
                        "auto": bool(e.get("auto"))})   # autonomous (nightshift) vs interactive
    out.sort(key=lambda r: r["ts"])
    return out

def find_dashboard():
    # new names first; keep the old queue-dashboard names as fallback for installs
    # made before the rename to the devbrain control-plane dashboard.
    for c in ("devbrain-dashboard.html", "dashboard.html",
              "devbrain-queue-dashboard.html", "queue-dashboard.html"):
        if os.path.exists(os.path.join(HERE, c)): return os.path.join(HERE, c)
    sys.exit("devbrain queue: dashboard.html not found")


class Queue:
    def __init__(self, data):
        self.data = data
        self.projects_dir = os.path.join(data, "projects")

    def projects(self):
        return sorted(os.path.basename(os.path.dirname(d))
                      for d in glob.glob(os.path.join(self.projects_dir, "*", "todo")))

    def todo_dir(self, project):                       # existing project dirs only (no traversal)
        safe = os.path.basename(project)
        d = os.path.join(self.projects_dir, safe, "todo")
        return d if os.path.isdir(os.path.join(self.projects_dir, safe)) else None

    def parse(self, path, project):
        text = open(path, encoding="utf-8", errors="replace").read()
        fm, order, title, body = {}, [], "", ""
        m = re.match(r"^---\n(.*?)\n---\n?(.*)$", text, re.S)
        if m:
            for line in m.group(1).splitlines():
                if ":" in line:
                    k, v = line.split(":", 1); k = k.strip()
                    fm[k] = v.strip(); order.append(k)
            rest = m.group(2).splitlines()
            for i, l in enumerate(rest):
                if l.startswith("# "):
                    title = l[2:].strip(); body = "\n".join(rest[i + 1:]).strip(); break
            else:
                body = m.group(2).strip()
        else:
            body = text.strip()
        try: pr = int(fm.get("priority", "0") or 0)
        except ValueError: pr = 0
        return {"id": fm.get("id", os.path.splitext(os.path.basename(path))[0]), "project": project,
                "status": fm.get("status", "open"), "priority": pr, "created": fm.get("created", ""),
                "claimed_by": fm.get("claimed_by", ""), "pr": fm.get("pr", ""),
                "reason": fm.get("reason", ""), "done_at": fm.get("done_at", ""),
                "approved": fm.get("approved", "").lower() == "true",
                "title": title, "body": body, "_order": order}

    def all_tasks(self):
        out = []
        for d in glob.glob(os.path.join(self.projects_dir, "*", "todo")):
            project = os.path.basename(os.path.dirname(d))
            for f in glob.glob(os.path.join(d, "*.md")):
                try: out.append(self.parse(f, project))
                except Exception as e:
                    out.append({"id": os.path.basename(f), "project": project, "status": "open",
                                "priority": 0, "title": "(parse error) " + str(e), "body": "",
                                "created": "", "pr": "", "reason": "", "claimed_by": "", "done_at": "", "_order": []})
        return sorted(out, key=lambda t: (-t["priority"], t["created"]))

    def write(self, project, tid, updates, title, body):
        d = self.todo_dir(project)
        if not d: raise ValueError("unknown project")
        path = os.path.join(d, os.path.basename(tid) + ".md")
        if not os.path.exists(path): raise FileNotFoundError(tid)
        cur = self.parse(path, project)
        if updates.get("status") == "done":            # done_at follows status:
            updates = {**updates, "done_at": now()}    #   stamp on entering done,
        elif updates.get("status"):
            updates = {**updates, "done_at": None}      #   clear on leaving it (no zombie)
        order = cur["_order"] or ["id", "status", "priority", "created"]
        fm = {k: cur.get(k, "") for k in order}
        fm.update({k: v for k, v in updates.items() if v is not None})
        lines, written = ["---"], set()
        for k in order:
            if updates.get(k) is None and k in updates: continue   # delete this field
            lines.append(f"{k}: {fm.get(k, '')}"); written.add(k)
        for k, v in updates.items():                               # any new fields
            if v is not None and k not in written: lines.append(f"{k}: {v}")
        lines += ["---", "", "# " + title, "", body.rstrip() + "\n"]
        open(path, "w", encoding="utf-8").write("\n".join(lines))
        return self.parse(path, project)

    def create(self, project, title, priority, body):
        d = self.todo_dir(project)
        if not d: raise ValueError("unknown project")
        mx = 0
        for f in glob.glob(os.path.join(d, "*.md")):
            m = re.match(r"(\d+)", os.path.basename(f))
            if m: mx = max(mx, int(m.group(1)))
        slug = re.sub(r"[^a-z0-9]+", "-", (title or "task").lower()).strip("-")[:50] or "task"
        tid = f"{mx + 1:04d}-{slug}"; path = os.path.join(d, tid + ".md")
        prio = max(0, min(100, int(priority or 0)))
        open(path, "w", encoding="utf-8").write(
            f"---\nid: {tid}\nstatus: open\npriority: {prio}\ncreated: {now()}\n---\n\n"
            f"# {title or 'untitled'}\n\n{(body or '').rstrip()}\n")
        return self.parse(path, project)

    def nightshift(self):
        """Every project with a live nightshift fleet — so the dashboard lists ALL
        running fleets without first selecting the right project. Each run records
        projects/<key>/nightshift-run.json = {port, repo}; the orchestrator writes
        <repo>/.nightshift/status.json (workers, token rate, merges). We read + forward
        them, so the queue dashboard IS the run monitor — no second server.

        Self-heal: a clean `nightshift stop` unregisters, but a crash/kill/reboot leaves
        the run file behind — a phantom 'stopped' fleet that would haunt the dashboard
        forever. Prune any registration whose fleet is gone (repo deleted, or stopped and
        no longer refreshing status.json) so dead runs clear themselves on the next poll."""
        runs = []
        for f in sorted(glob.glob(os.path.join(self.projects_dir, "*", "nightshift-run.json"))):
            try:
                run = json.load(open(f))
            except (OSError, ValueError):
                continue
            repo = run.get("repo", "")
            try:
                status = json.load(open(os.path.join(repo, ".nightshift", "status.json")))
            except (OSError, ValueError, TypeError):
                if not os.path.isdir(repo): self._prune_run(f)   # repo gone → registration is dead
                continue
            if self._stale_run(status): self._prune_run(f); continue
            runs.append({"project": os.path.basename(os.path.dirname(f)), **status})
        return {"runs": runs}

    @staticmethod
    def _stale_run(status, ttl=300):
        """A fleet that stopped without unregistering: not running AND its status.json
        hasn't been refreshed within ttl seconds (a live emit loop rewrites it every ~2s).
        A running fleet — or one whose stamp is still fresh — is kept regardless."""
        if status.get("running"): return False
        try:
            age = (datetime.datetime.now(datetime.timezone.utc).timestamp()
                   - datetime.datetime.fromisoformat(status.get("updated", "").replace("Z", "+00:00")).timestamp())
        except (ValueError, AttributeError):
            return True   # un-stamped / unparseable on a not-running run → treat as dead
        return age > ttl

    @staticmethod
    def _prune_run(run_file):
        try: os.remove(run_file)
        except OSError: pass

    def delete(self, project, tid):
        d = self.todo_dir(project)
        if not d: return False
        path = os.path.join(d, os.path.basename(tid) + ".md")
        if os.path.exists(path) and os.path.dirname(os.path.abspath(path)) == os.path.abspath(d):
            os.remove(path); return True
        return False

    def start_nightshift(self, project, ids, port):
        """Launch a bounded nightshift fleet over a chosen subset of tasks — the server side
        of the dashboard's drag-to-🌙. Resolves the project's local repo, sanity-checks the
        ids, and spawns `nightshift start <repo> --only <ids>` detached (NIGHTSHIFT_NO_OPEN so
        it registers the run + emit loop for THIS dashboard without popping a new tab)."""
        ids = [i for i in ids if re.match(r"^\d{4}", str(i))]   # ids look like task ids (0081 / 0081-foo)
        if not ids:
            return {"error": "no valid task ids selected"}
        checkout = project_repo(self.data, project)
        if not checkout:
            return {"error": f"couldn't find a local checkout for {project} — run "
                             "`devbrain nightshift start <repo>` once from that repo first"}
        # Run in a DEDICATED clone (~/nightshift/<repo>), not the active working checkout: keeps
        # the fleet's sibling worktrees out of your Conductor workspaces, names them after the
        # repo, and gives the orchestrator a clone where `nightshift` isn't checked out elsewhere
        # so its reset-to-main works. First launch clones from the remote (a few seconds).
        repo, note = ensure_nightshift_clone(checkout)
        if not repo:
            return {"error": note}
        # Refuse a second fleet on the same repo. The CLI's own pgrep dedup RACES on
        # near-simultaneous starts (a double-click / drop+click), which spawns two
        # orchestrators whose workers then collide. An atomic O_EXCL lock closes that window:
        # the first request wins the lock and launches; the rest get a clean "already running".
        if nightshift_running(repo):
            return {"error": "nightshift is already running on this repo — stop it first"}
        try:
            lock = os.path.join(repo, ".nightshift", "launch.lock")
            os.makedirs(os.path.dirname(lock), exist_ok=True)
            try:
                fd = os.open(lock, os.O_CREAT | os.O_EXCL | os.O_WRONLY)   # atomic claim
                os.close(fd)
            except FileExistsError:
                if now_epoch() - os.path.getmtime(lock) < 30:   # a launch is mid-flight
                    return {"error": "a nightshift launch is already starting on this repo"}
                os.utime(lock, None)   # stale lock from a crashed start → reclaim it
        except OSError:
            pass   # lock is best-effort; pgrep guard above is the primary defense
        cli = os.path.join(HERE, "nightshift")
        env = dict(os.environ, NIGHTSHIFT_NO_OPEN="1", DEVBRAIN_QUEUE_PORT=str(port))
        try:
            subprocess.Popen([cli, "start", repo, "--only", ",".join(ids)],
                             stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                             start_new_session=True, env=env)
        except OSError as e:
            return {"error": f"could not launch nightshift: {e}"}
        return {"ok": True, "repo": repo, "note": note, "ids": ids, "count": len(ids)}


class Handler(BaseHTTPRequestHandler):
    q = None
    dashboard = None
    port = 8799   # the port we actually bound — passed to a dashboard-launched nightshift run

    def log_message(self, format, *args): pass

    def _loopback(self):
        # Bound to 127.0.0.1; also require a loopback Host (DNS-rebinding) + Origin (CSRF).
        def lb(v):
            h = (v or "").split("://")[-1].rsplit(":", 1)[0].strip("[]").lower()
            return h in ("127.0.0.1", "localhost", "::1")
        if not lb(self.headers.get("Host")): return False
        o = self.headers.get("Origin")
        return lb(o) if o else True

    def _send(self, code, body, ctype="application/json"):
        b = body.encode() if isinstance(body, str) else body
        self.send_response(code); self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(b))); self.send_header("Cache-Control", "no-store")
        self.end_headers(); self.wfile.write(b)

    def do_GET(self):
        if not self._loopback(): return self._send(403, '{"error":"forbidden"}')
        if urlparse(self.path).path in ("/", "/index.html"):   # ignore ?project=… (client-side only)
            return self._send(200, open(self.dashboard, "rb").read(), "text/html; charset=utf-8")
        if self.path == "/api/todos":
            return self._send(200, json.dumps({"projects": self.q.projects(),
                                               "statuses": STATUSES, "tasks": self.q.all_tasks()}))
        if self.path.startswith("/api/nightshift/resolve"):   # where would a launch run + is one already going?
            proj = parse_qs(urlparse(self.path).query).get("project", [None])[0]
            checkout = project_repo(self.q.data, proj) if proj else None
            # report the DEDICATED clone dir the fleet will run in (computed, not cloned here),
            # whether it already exists, and whether a fleet is live there.
            repo = nightshift_clone_path(checkout) if checkout else None
            repo = repo or checkout
            exists = bool(repo) and os.path.exists(os.path.join(repo, ".git"))
            return self._send(200, json.dumps({"repo": repo, "cloned": exists,
                                                "running": exists and nightshift_running(repo)}))
        if self.path.startswith("/api/nightshift"):
            return self._send(200, json.dumps(self.q.nightshift()))
        if self.path.startswith("/api/prompts"):
            qs = parse_qs(urlparse(self.path).query)
            raw = (qs.get("days", ["30"])[0])
            days = int(raw) if raw.isdigit() else 30
            proj = qs.get("project", [None])[0]
            kind = qs.get("kind", ["typed"])[0]
            if kind not in ("typed", "bot", "all"):
                kind = "typed"
            recs = scan_prompts(self.q.data, days, proj)
            typed = sum(1 for r in recs if r["kind"] in _TYPED_KINDS)
            return self._send(200, json.dumps({"prompts": _filter_kind(recs, kind), "days": days,
                                                "kind": kind,
                                                "counts": {"typed": typed, "bot": len(recs) - typed}}))
        if self.path.startswith("/api/gbrain"):
            qs = parse_qs(urlparse(self.path).query)
            raw = qs.get("days", ["0"])[0]
            days = int(raw) if raw.isdigit() else 0
            return self._send(200, json.dumps({"queries": gbrain_queries(self.q.data, days)}))
        if self.path.startswith("/api/tokens"):
            qs = parse_qs(urlparse(self.path).query)
            raw = qs.get("days", ["0"])[0]
            days = int(raw) if raw.isdigit() else 0
            return self._send(200, json.dumps({"usage": token_usage(self.q.data, days)}))
        return self._send(404, '{"error":"not found"}')

    def do_POST(self):
        if not self._loopback(): return self._send(403, '{"error":"forbidden"}')
        try:
            n = int(self.headers.get("Content-Length") or 0)
            d = json.loads(self.rfile.read(n) or b"{}")
            if self.path == "/api/save":
                status = d.get("status")
                updates = {"status": status,
                           "priority": str(max(0, min(100, int(d.get("priority", 0))))),
                           "reason": d.get("reason") or None,            # write() handles done_at from status
                           "approved": "true" if d.get("approved") else None}   # greenlight unattended pickup
                t = self.q.write(d["project"], d["id"], updates, d.get("title", ""), d.get("body", ""))
                return self._send(200, json.dumps(t))
            if self.path == "/api/create":
                t = self.q.create(d["project"], d.get("title", ""), d.get("priority", 0), d.get("body", ""))
                return self._send(200, json.dumps(t))
            if self.path == "/api/delete":
                return self._send(200, json.dumps({"ok": self.q.delete(d["project"], d["id"])}))
            if self.path == "/api/nightshift/start":
                r = self.q.start_nightshift(d["project"], d.get("ids", []), self.port)
                return self._send(200 if r.get("ok") else 422, json.dumps(r))
            return self._send(404, '{"error":"not found"}')
        except Exception as e:
            return self._send(400, json.dumps({"error": str(e)}))


def is_devbrain_queue(port):
    """True if a devbrain queue is already serving on this loopback port — probe /api/todos
    and look for its shape, so a second `devbrain queue` can reuse it instead of erroring."""
    try:
        with urlopen(f"http://127.0.0.1:{port}/api/todos", timeout=1) as r:
            return b'"statuses"' in r.read(4096)
    except Exception:
        return False

def select_port(start, tries, try_bind, is_reusable):
    """Pick where to serve, never crashing on a busy port. Walk ports from `start`:
      - ('serve', httpd, port)  first port we could bind (use it);
      - ('reuse', None, port)   a busy port already hosting a devbrain queue (open that one);
      - ('none',  None, None)   start..start+tries all busy with something else.
    I/O is injected (try_bind returns an httpd or None; is_reusable probes) so it unit-tests
    without real sockets."""
    for port in range(start, start + tries):
        httpd = try_bind(port)
        if httpd is not None:
            return ("serve", httpd, port)
        if is_reusable(port):
            return ("reuse", None, port)
    return ("none", None, None)

def main():
    ap = argparse.ArgumentParser(prog="devbrain queue", description="localhost TODO-queue kanban")
    ap.add_argument("--port", type=int, default=8799)
    ap.add_argument("--no-open", action="store_true")
    ap.add_argument("--data", default=os.environ.get("DEVBRAIN_DATA", os.path.expanduser("~/devbrain-data")))
    args = ap.parse_args()
    Handler.q = Queue(os.path.abspath(args.data))
    Handler.dashboard = find_dashboard()
    if not os.path.isdir(Handler.q.projects_dir):
        sys.exit(f"devbrain queue: no projects dir at {Handler.q.projects_dir}")

    def open_browser(url):
        if not args.no_open:
            try: webbrowser.open(url)
            except Exception: pass

    def try_bind(port):
        try:
            return ThreadingHTTPServer(("127.0.0.1", port), Handler)
        except OSError as e:
            if e.errno == errno.EADDRINUSE:
                return None
            raise

    kind, httpd, port = select_port(args.port, 20, try_bind, is_devbrain_queue)
    if kind == "none":
        sys.exit(f"devbrain queue: no free port in {args.port}–{args.port + 19}")
    Handler.port = port   # so a dashboard-launched nightshift run advertises THIS dashboard's port
    url = f"http://127.0.0.1:{port}/"
    if kind == "reuse":
        print(f"devbrain queue already running → {url}  (opening it)")
        open_browser(url); return
    if port != args.port:
        print(f"devbrain queue: port {args.port} busy — using {port}")
    print(f"devbrain queue → {url}  (Ctrl-C to stop)")
    open_browser(url)
    try: httpd.serve_forever()
    except KeyboardInterrupt: print("\nstopped")


if __name__ == "__main__":
    main()
