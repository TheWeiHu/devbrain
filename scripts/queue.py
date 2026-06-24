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
import os, re, sys, glob, json, errno, argparse, datetime, webbrowser
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
                    out.append({"p": proj, "date": date, "time": ts[:5], "dt": dt.isoformat(),
                                "h": dt.hour, "wd": dt.strftime("%a"), "c": len(text),
                                "w": len(text.split()), "x": text, "kind": kind})
                except ValueError:
                    pass
            i = j
    out.sort(key=lambda r: r["dt"])
    return out

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
            topics = _GB_TOPIC.findall(e.get("cmd", "") or "")
            out.append({"ts": ts, "date": ts[:10], "p": proj,
                        "read": any(m in _GB_READ for m in modes),
                        "modes": modes, "hits": e.get("hits", 0) or 0,
                        "slugs": e.get("slugs") or [], "q": topics[0] if topics else ""})
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
    # made before the rename to the DevBrain control-plane dashboard.
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


class Handler(BaseHTTPRequestHandler):
    q = None
    dashboard = None

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
