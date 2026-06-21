#!/usr/bin/env python3
"""devbrain queue — localhost control plane for the multi-project TODO queue.

A browser dashboard to view, edit, prioritize, add context to, and unblock tasks
across every project's queue. Reads task markdown directly (fast); every *mutation*
is routed through `devbrain-todo.sh` so the CLI and UI share one source of truth and
the frontmatter format never drifts. Binds 127.0.0.1 only, and rejects any request
whose Host/Origin isn't loopback (DNS-rebinding defense — see Handler._local_only).

  devbrain queue [--port N] [--no-open] [--data DIR]

Endpoints:
  GET  /                      the dashboard HTML
  GET  /api/projects          [{key, open, taken, review, held, done}, ...]
  GET  /api/tasks?project=K   all tasks for project K (fields + title + body + reason)
  GET  /api/nightshift?project=K   {active, url, port} for that project's live nightshift monitor
  POST /action?project=K&cmd=V&id=ID[&...]   run a todo verb (see VERBS)
"""
import sys, os, re, json, socket, argparse, subprocess, webbrowser, threading
from typing import Optional
from urllib.parse import urlparse, urlsplit, parse_qs
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
IDRE = re.compile(r"^[0-9]{4}-[a-z0-9._-]+$")
KEYRE = re.compile(r"^[a-z0-9._-]+$")        # project keys are sanitized owner__repo
LOCAL_HOSTS = {"127.0.0.1", "localhost", "::1"}   # loopback host/origin allowlist


def bare_host(value):
    """Hostname (lowercased, port + scheme stripped) from a Host or Origin header.
    Host is a bare authority ('127.0.0.1:8799', '[::1]:8799'); Origin carries a
    scheme ('http://evil.example'). Empty string if unparseable."""
    value = (value or "").strip()
    if not value:
        return ""
    if "://" not in value:
        value = "//" + value                 # give the bare authority a scheme to parse
    try:
        return (urlsplit(value).hostname or "").lower()
    except ValueError:
        return ""

# Mutations the dashboard may issue, each mapped to a devbrain-todo verb. Status
# changes reuse the lifecycle verbs (no generic setter) so stamps/guards stay intact.
VERBS = {
    "add":     "add",       # title, [prio], [body]  -> prints new id
    "edit":    "edit",      # [title], [body]
    "prio":    "prio",      # prio
    "claim":   "claim",
    "review":  "review",    # [pr]
    "hold":    "hold",      # [reason]
    "approve": "approve",
    "done":    "done",
    "release": "release",
    "context": "context",   # body on stdin
}


USAGE_RE = re.compile(r'^\s*todo\s+[a-z]', re.M)   # the `todo <verb> …` menu lines


def looks_like_usage(text):
    """A stale devbrain-todo.sh that doesn't recognize a verb may print its usage
    banner and still exit 0 — so the UI would toast a no-op as success. The banner
    lists the whole `todo <verb> …` menu; a real single-action result is one terse
    line ("claimed <id>"), never the menu. Spot the menu (or an explicit marker)."""
    if not text:
        return False
    low = text.lower()
    if "unknown command" in low or "usage:" in low:
        return True
    return len(USAGE_RE.findall(text)) >= 3


def find_todo():
    # $DEVBRAIN_TODO pins the CLI build (tests/checkouts that aren't installed);
    # else prefer THIS repo's own copy over the installed hook. The installed hook
    # can lag the checkout, and a stale one no-ops new verbs (usage banner, exit 0),
    # so a `devbrain queue` run from a checkout would silently skew edits/prio. The
    # sibling copy ships with the dashboard you're running, so it can't drift.
    for c in (os.environ.get("DEVBRAIN_TODO", ""),
              os.path.join(HERE, "todo.sh"),
              os.path.expanduser("~/.claude/hooks/devbrain-todo.sh")):
        if c and os.access(c, os.X_OK):
            return c
    sys.exit("devbrain queue: cannot find devbrain-todo.sh")


def find_dashboard():
    for c in ("devbrain-queue-dashboard.html", "queue-dashboard.html"):
        p = os.path.join(HERE, c)
        if os.path.exists(p):
            return p
    sys.exit("devbrain queue: cannot find queue-dashboard.html")


def parse_task(path):
    """Read one task .md into a dict: frontmatter fields + title + body."""
    fm, title, body = {}, "", []
    try:
        lines = open(path, encoding="utf-8", errors="replace").read().splitlines()
    except OSError:
        return None
    n, in_body = 0, False
    for ln in lines:
        if ln.strip() == "---" and n < 2:
            n += 1
            continue
        if n == 1:                                   # inside frontmatter
            m = re.match(r"^([A-Za-z_]+):\s*(.*)$", ln)
            if m:
                fm[m.group(1)] = m.group(2)
            continue
        if n >= 2:                                   # body
            if not title and ln.startswith("# "):
                title = ln[2:].strip()
                in_body = True
                continue
            if in_body:
                body.append(ln)
    t = {k: fm.get(k, "") for k in
         ("id", "status", "priority", "created", "claimed_by", "claimed_at",
          "pr", "reason", "approved", "done_at", "last_failure")}
    t["title"] = title
    t["body"] = "\n".join(body).strip("\n")
    if not t["id"]:
        t["id"] = os.path.splitext(os.path.basename(path))[0]
    try:
        t["priority_n"] = int(t["priority"] or 0)
    except ValueError:
        t["priority_n"] = 0
    return t


class App:
    def __init__(self, data, todo, dashboard):
        self.data = data
        self.todo = todo
        self.dashboard = dashboard
        # path -> ((mtime_ns, size), parsed task). The dashboard polls /api/tasks
        # and /api/projects every few seconds, each re-listing a project's todo
        # dir; without this every poll re-reads + regex-parses every .md (O(all
        # files), and done/ only grows). Cache parses, keyed by mtime+size so an
        # on-disk edit invalidates its entry. ThreadingHTTPServer serves polls
        # concurrently, so guard the dict with a lock.
        self._cache = {}
        self._cache_lock = threading.Lock()
        self.parse_count = 0   # actual parses (cache misses) — observability for tests

    def projects_dir(self):
        return os.path.join(self.data, "projects")

    def project_keys(self):
        root = self.projects_dir()
        out = []
        if os.path.isdir(root):
            for name in sorted(os.listdir(root)):
                if KEYRE.match(name) and os.path.isdir(os.path.join(root, name, "todo")):
                    out.append(name)
        return out

    def todo_dir(self, project):
        return os.path.join(self.projects_dir(), project, "todo")

    def _read_task(self, path):
        """parse_task(path), served from the mtime+size cache. The parsed dict is
        treated as read-only by every caller, so a cached copy is safe to share
        across the serving threads; do not mutate a task returned from here."""
        try:
            st = os.stat(path)
        except OSError:
            with self._cache_lock:
                self._cache.pop(path, None)
            return None
        key = (st.st_mtime_ns, st.st_size)
        with self._cache_lock:
            hit = self._cache.get(path)
        if hit and hit[0] == key:
            return hit[1]
        t = parse_task(path)   # cache miss — parse outside the lock, then store
        with self._cache_lock:
            self.parse_count += 1
            if t is not None:
                self._cache[path] = (key, t)
        return t

    def tasks(self, project):
        d = self.todo_dir(project)
        out = []
        if os.path.isdir(d):
            for fn in sorted(os.listdir(d)):
                if fn.endswith(".md"):
                    t = self._read_task(os.path.join(d, fn))
                    if t:
                        out.append(t)
        out.sort(key=lambda t: (-t["priority_n"], t.get("created", "")))
        return out

    def project_summary(self):
        out = []
        for k in self.project_keys():
            counts = {s: 0 for s in ("open", "taken", "review", "held", "done")}
            parked = 0   # held tasks whose reason starts 'parked' = deliberate focus-parks, not blocks
            for t in self.tasks(k):
                if t["status"] in counts:
                    counts[t["status"]] += 1
                if t["status"] == "held" and re.match(r"(?i)\s*parked\b", t.get("reason", "")):
                    parked += 1
            out.append({"key": k, **counts, "parked": parked})
        return out

    def valid_project(self, project):
        return bool(project) and KEYRE.match(project) and project in self.project_keys()

    def nightshift_run(self, project):
        """Detect a live nightshift monitor for `project`.

        nightshift's `watch` writes projects/<key>/nightshift-run.json = {port, repo}
        when it brings the monitor server up (the dashboard knows projects by
        <owner>__<repo> key, not by checkout path, so the run records its own port).
        We trust the file only if that port still has a listener — a stale record from
        a stopped run probes dead, so the dashboard links to a real monitor or stays
        standalone. Returns {active, port?, repo?, url?}.
        """
        if not self.valid_project(project):
            return {"active": False}
        path = os.path.join(self.projects_dir(), project, "nightshift-run.json")
        try:
            with open(path) as fh:
                run = json.load(fh)
            port = int(run["port"])
        except (OSError, ValueError, KeyError, TypeError):
            return {"active": False}
        try:
            socket.create_connection(("127.0.0.1", port), timeout=0.2).close()
        except OSError:
            return {"active": False, "port": port}
        return {"active": True, "port": port, "repo": run.get("repo"),
                "url": f"http://127.0.0.1:{port}/index.html"}

    def run_verb(self, project, cmd, params):
        """Translate a dashboard action into a devbrain-todo invocation."""
        if cmd not in VERBS:
            return False, "unknown cmd"
        if not self.valid_project(project):
            return False, "unknown project"
        verb = VERBS[cmd]
        argv, stdin = [self.todo, verb], None
        if cmd == "add":
            title = (params.get("title") or "").strip()
            if not title:
                return False, "add needs a title"
            argv.append(title)
            if params.get("prio"):
                argv += ["-p", str(int(params["prio"]))]
            if params.get("body"):
                argv += ["-b", params["body"]]
        else:
            tid = (params.get("id") or "").strip()
            if not IDRE.match(tid):
                return False, "bad id"
            # id must already live in THIS project's queue — blocks traversal /
            # cross-project writes (the action only ever mutates the selected queue).
            if not os.path.exists(os.path.join(self.todo_dir(project), tid + ".md")):
                return False, "no such task in project"
            argv.append(tid)
            if cmd == "edit":
                if "title" in params:
                    argv += ["-t", params["title"]]
                if "body" in params:
                    argv += ["-b", params["body"]]
                if "title" not in params and "body" not in params:
                    return False, "edit needs title and/or body"
            elif cmd == "prio":
                argv.append(str(int(params.get("prio", 0))))
            elif cmd == "review" and params.get("pr"):
                argv.append(params["pr"])
            elif cmd == "hold" and params.get("reason"):
                argv.append(params["reason"])
            elif cmd == "context":
                stdin = params.get("body") or ""
                if not stdin.strip():
                    return False, "context needs a body"
        env = dict(os.environ, DEVBRAIN_DATA=self.data, DEVBRAIN_PROJECT=project)
        try:
            r = subprocess.run(argv, cwd=self.data, env=env, input=stdin,
                               capture_output=True, text=True, timeout=25)
        except Exception as e:
            return False, str(e)
        out = (r.stdout or r.stderr).strip()
        if r.returncode != 0:
            return False, out or "command failed"
        # rc==0 but the output is a usage banner -> stale CLI that didn't know the
        # verb (printed help, exited 0). The task is unchanged; don't toast success.
        if looks_like_usage(out):
            return False, "stale devbrain-todo.sh — verb not supported; update the CLI"
        return True, out


class Handler(BaseHTTPRequestHandler):
    app: Optional[App] = None   # set to the App instance before serving

    def _send(self, code, body, ctype="application/json"):
        if isinstance(body, (dict, list)):
            body = json.dumps(body)
        b = body.encode() if isinstance(body, str) else body
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(b)))
        self.send_header("Cache-Control", "no-store, max-age=0")
        self.end_headers()
        self.wfile.write(b)

    def _local_only(self):
        """Reject DNS-rebinding. Binding 127.0.0.1 stops *remote* TCP, but a page
        on this machine can still POST to the server via a forged Host header
        (evil.example resolving to 127.0.0.1). So require the browser-supplied Host
        — and Origin, when present — to name a loopback address."""
        host = bare_host(self.headers.get("Host"))
        if host and host not in LOCAL_HOSTS:
            return False
        origin = self.headers.get("Origin")
        if origin and bare_host(origin) not in LOCAL_HOSTS:
            return False
        return True

    def _params(self):
        q = {k: v[0] for k, v in parse_qs(urlparse(self.path).query).items()}
        if self.command == "POST":
            n = int(self.headers.get("Content-Length") or 0)
            raw = self.rfile.read(n).decode() if n else ""
            ctype = self.headers.get("Content-Type", "")
            if raw and ctype.startswith("application/json"):
                try:
                    q.update(json.loads(raw))
                except ValueError:
                    pass
            elif raw:
                q.update({k: v[0] for k, v in parse_qs(raw).items()})
        return q

    def do_GET(self):
        if not self._local_only():
            return self._send(403, {"ok": False, "msg": "forbidden: non-loopback host/origin"})
        path = urlparse(self.path).path
        if path in ("/", "/index.html"):
            return self._send(200, open(self.app.dashboard, "rb").read(), "text/html; charset=utf-8")
        if path == "/api/projects":
            return self._send(200, self.app.project_summary())
        if path == "/api/tasks":
            p = self._params().get("project", "")
            if not self.app.valid_project(p):
                return self._send(400, {"ok": False, "msg": "unknown project"})
            return self._send(200, {"project": p, "tasks": self.app.tasks(p)})
        if path == "/api/nightshift":
            return self._send(200, self.app.nightshift_run(self._params().get("project", "")))
        return self._send(404, {"ok": False, "msg": "not found"})

    def do_POST(self):
        if not self._local_only():
            return self._send(403, {"ok": False, "msg": "forbidden: non-loopback host/origin"})
        if urlparse(self.path).path != "/action":
            return self._send(404, {"ok": False, "msg": "not found"})
        p = self._params()
        ok, msg = self.app.run_verb(p.get("project", ""), p.get("cmd", ""), p)
        return self._send(200 if ok else 400, {"ok": ok, "msg": msg})

    def log_message(self, format, *args):
        pass


def main():
    ap = argparse.ArgumentParser(prog="devbrain queue", description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--port", type=int, default=8799, help="localhost port (default 8799)")
    ap.add_argument("--no-open", action="store_true", help="don't open a browser (headless/tests)")
    ap.add_argument("--data", default=os.environ.get("DEVBRAIN_DATA",
                    os.path.expanduser("~/devbrain-data")), help="devbrain data repo")
    args = ap.parse_args()

    app = App(os.path.abspath(args.data), find_todo(), find_dashboard())
    if not os.path.isdir(app.projects_dir()):
        sys.exit(f"devbrain queue: no projects dir at {app.projects_dir()}")
    Handler.app = app
    httpd = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    url = f"http://127.0.0.1:{args.port}/"
    print(f"devbrain queue → {url}  (data: {app.data}, {len(app.project_keys())} project(s))")
    print("  Ctrl-C to stop")
    if not args.no_open:
        try:
            webbrowser.open(url)
        except Exception:
            pass
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\ndevbrain queue: stopped")


if __name__ == "__main__":
    main()
