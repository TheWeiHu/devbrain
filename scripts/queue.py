#!/usr/bin/env python3
"""devbrain queue — a small localhost dashboard for the TODO queue.

It just edits the task .md files directly: they're markdown with a YAML-ish
frontmatter block, so reading is a parse and a mutation is a field rewrite — no
CLI, no subprocess, no abstraction. Binds 127.0.0.1 only.

  devbrain queue [--port N] [--no-open] [--data DIR]

Endpoints: GET / (dashboard) · GET /api/projects · GET /api/tasks?project=K
           GET /api/nightshift?project=K · POST /action?project=K&cmd=V&id=ID[...]
"""
import sys, os, re, json, argparse, datetime, webbrowser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs

HERE = os.path.dirname(os.path.abspath(__file__))
IDRE = re.compile(r"^[0-9]{4}-[a-z0-9._-]+$")     # task id = NNNN-slug
KEYRE = re.compile(r"^[a-z0-9._-]+$")             # project key = owner__repo (sanitized)
FIELDS = ("id", "status", "priority", "created", "claimed_by", "claimed_at",
          "pr", "reason", "approved", "done_at", "last_failure")

def now():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

def clamp_prio(v):
    try: n = int(str(v).strip())
    except (TypeError, ValueError): return None
    return n if 0 <= n <= 100 else None

# ---- task files: read + write (same format devbrain-todo.sh uses) --------------------
def read_task(path):
    try: lines = open(path, encoding="utf-8", errors="replace").read().splitlines()
    except OSError: return None
    fm, title, body, n = {}, "", [], 0
    for ln in lines:
        if ln.strip() == "---" and n < 2:
            n += 1; continue
        if n == 1:
            m = re.match(r"^([A-Za-z_]+):\s*(.*)$", ln)
            if m: fm[m.group(1)] = m.group(2)
        elif n >= 2:
            if not title and ln.startswith("# "): title = ln[2:].strip()
            else: body.append(ln)
    t = {k: fm.get(k, "") for k in FIELDS}
    t["title"] = title
    t["body"] = "\n".join(body).strip("\n")
    t["id"] = t["id"] or os.path.splitext(os.path.basename(path))[0]
    try: t["priority_n"] = int(t["priority"] or 0)
    except ValueError: t["priority_n"] = 0
    return t

def frontmatter(path):
    """The frontmatter block verbatim, up to and including the closing '---'."""
    out, n = [], 0
    for ln in open(path, encoding="utf-8").read().splitlines():
        out.append(ln)
        if ln.strip() == "---":
            n += 1
            if n == 2: break
    return out

def set_fields(path, **kw):
    """Set (or insert) frontmatter fields in place; leaves title + body untouched."""
    out, n, seen = [], 0, set()
    for ln in open(path, encoding="utf-8").read().splitlines():
        if ln.strip() == "---":
            n += 1
            if n == 2:
                for k, v in kw.items():
                    if k not in seen: out.append(f"{k}: {v}")
            out.append(ln); continue
        if n == 1:
            m = re.match(r"^([A-Za-z_]+):", ln)
            if m and m.group(1) in kw:
                seen.add(m.group(1)); out.append(f"{m.group(1)}: {kw[m.group(1)]}"); continue
        out.append(ln)
    open(path, "w", encoding="utf-8").write("\n".join(out) + "\n")

def set_title_body(path, title=None, body=None):
    t = read_task(path) or {"title": "", "body": ""}
    title = t["title"] if title is None else title
    body = t["body"] if body is None else body
    text = "\n".join(frontmatter(path)) + f"\n\n# {title}\n"
    if body: text += f"\n{body}\n"
    open(path, "w", encoding="utf-8").write(text)

def append_context(path, ctx):
    keep = []
    for ln in open(path, encoding="utf-8").read().splitlines():
        if ln.startswith("## Context (synthesized "): break
        keep.append(ln)
    text = "\n".join(keep).rstrip("\n")
    open(path, "w", encoding="utf-8").write(f"{text}\n\n## Context (synthesized {now()})\n\n{ctx}\n")

# ---- the verbs: each is just a few field/body writes ---------------------------------
def do_add(tdir, p):
    title = (p.get("title") or "").strip()
    if not title: return False, "add needs a title"
    prio = clamp_prio(p.get("prio", 0))
    if prio is None: return False, "priority must be 0-100"
    os.makedirs(tdir, exist_ok=True)
    slug = re.sub(r"-+", "-", re.sub(r"[^a-z0-9-]", "", title.lower().replace(" ", "-"))).strip("-")[:40] or "task"
    seq = 0
    for fn in os.listdir(tdir):
        m = re.match(r"^(\d{4})-", fn)
        if m: seq = max(seq, int(m.group(1)))
    while True:
        seq += 1; tid = f"{seq:04d}-{slug}"; path = os.path.join(tdir, tid + ".md")
        if not os.path.exists(path): break
    fm = f"---\nid: {tid}\nstatus: open\npriority: {prio}\ncreated: {now()}\nclaimed_by:\nclaimed_at:\npr:\n---\n\n# {title}\n"
    open(path, "w", encoding="utf-8").write(fm + (f"\n{p['body']}\n" if p.get("body") else ""))
    return True, tid

def do_edit(path, p):
    if "title" not in p and "body" not in p: return False, "edit needs title and/or body"
    set_title_body(path, p.get("title"), p.get("body")); return True, "edited"

def do_prio(path, p):
    n = clamp_prio(p.get("prio", 0))
    if n is None: return False, "priority must be 0-100"
    set_fields(path, priority=n); return True, f"priority {n}"

def do_context(path, p):
    ctx = (p.get("body") or "").strip()
    if not ctx: return False, "context needs a body"
    append_context(path, ctx); return True, "context added"

# status verbs: just frontmatter fields. Reopening (approve/release) clears the merged-PR
# record + done stamp so the task is a clean 'open' again — no zombie.
def do_claim(path, p):
    import socket, getpass
    set_fields(path, status="taken",
               claimed_by=f"{getpass.getuser()}@{socket.gethostname().split('.')[0]}", claimed_at=now())
    return True, "claimed"
def do_review(path, p):
    set_fields(path, status="review", **({"pr": p["pr"]} if p.get("pr") else {})); return True, "review"
def do_hold(path, p):
    set_fields(path, status="held", **({"reason": p["reason"]} if p.get("reason") else {})); return True, "held"
def do_approve(path, p):
    set_fields(path, approved="true", status="open", claimed_by="", pr="", done_at=""); return True, "approved"
def do_done(path, p):
    set_fields(path, status="done", done_at=now()); return True, "done"
def do_release(path, p):
    set_fields(path, status="open", claimed_by="", claimed_at="", pr="", done_at=""); return True, "released"

VERBS = {"add": do_add, "edit": do_edit, "prio": do_prio, "context": do_context,
         "claim": do_claim, "review": do_review, "hold": do_hold,
         "approve": do_approve, "done": do_done, "release": do_release}


class App:
    def __init__(self, data, dashboard):
        self.data, self.dashboard = data, dashboard

    def projects_dir(self): return os.path.join(self.data, "projects")
    def todo_dir(self, key): return os.path.join(self.projects_dir(), key, "todo")

    def project_keys(self):
        root = self.projects_dir()
        if not os.path.isdir(root): return []
        return sorted(n for n in os.listdir(root)
                      if KEYRE.match(n) and os.path.isdir(os.path.join(root, n, "todo")))

    def valid_project(self, key):
        return bool(key) and bool(KEYRE.match(key)) and key in self.project_keys()

    def tasks(self, key):
        d = self.todo_dir(key)
        out = [read_task(os.path.join(d, fn)) for fn in sorted(os.listdir(d))
               if fn.endswith(".md")] if os.path.isdir(d) else []
        out = [t for t in out if t]
        out.sort(key=lambda t: (-t["priority_n"], t.get("created", "")))
        return out

    def project_summary(self):
        out = []
        for k in self.project_keys():
            c = {s: 0 for s in ("open", "taken", "review", "held", "done")}
            parked = 0
            for t in self.tasks(k):
                if t["status"] in c: c[t["status"]] += 1
                if t["status"] == "held" and re.match(r"(?i)\s*parked\b", t.get("reason", "")): parked += 1
            out.append({"key": k, **c, "parked": parked})
        return out

    def nightshift_run(self, key):
        """A live nightshift monitor link, if this project has one (the watcher writes
        projects/<key>/nightshift-run.json = {port}). Best-effort, no probing."""
        if not self.valid_project(key): return {"active": False}
        try:
            run = json.load(open(os.path.join(self.projects_dir(), key, "nightshift-run.json")))
            return {"active": True, "url": f"http://127.0.0.1:{int(run['port'])}/index.html"}
        except (OSError, ValueError, KeyError, TypeError):
            return {"active": False}

    def action(self, key, cmd, p):
        if cmd not in VERBS: return False, "unknown cmd"
        if not self.valid_project(key): return False, "unknown project"
        if cmd == "add": return do_add(self.todo_dir(key), p)
        tid = (p.get("id") or "").strip()
        if not IDRE.match(tid): return False, "bad id"
        path = os.path.join(self.todo_dir(key), tid + ".md")
        if not os.path.exists(path): return False, "no such task in project"   # blocks traversal
        return VERBS[cmd](path, p)


class Handler(BaseHTTPRequestHandler):
    app = None

    def _send(self, code, body, ctype="application/json"):
        b = json.dumps(body).encode() if isinstance(body, (dict, list)) else (
            body.encode() if isinstance(body, str) else body)
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(b)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(b)

    def _loopback(self):
        # Bound to 127.0.0.1 already; also require a loopback Host (DNS-rebinding) and,
        # when present, a loopback Origin (CSRF). Fails closed on a missing Host.
        def lb(v):
            h = (v or "").split("://")[-1].rsplit(":", 1)[0].strip("[]").lower()
            return h in ("127.0.0.1", "localhost", "::1")
        if not lb(self.headers.get("Host")): return False
        origin = self.headers.get("Origin")
        return lb(origin) if origin else True

    def _params(self):
        q = {k: v[0] for k, v in parse_qs(urlparse(self.path).query).items()}
        if self.command == "POST":
            n = int(self.headers.get("Content-Length") or 0)
            raw = self.rfile.read(n).decode() if n else ""
            if raw.startswith("{"):
                try: q.update(json.loads(raw))
                except ValueError: pass
            elif raw:
                q.update({k: v[0] for k, v in parse_qs(raw).items()})
        return q

    def do_GET(self):
        if not self._loopback(): return self._send(403, {"ok": False, "msg": "forbidden"})
        path = urlparse(self.path).path
        if path in ("/", "/index.html"):
            return self._send(200, open(self.app.dashboard, "rb").read(), "text/html; charset=utf-8")
        if path == "/api/projects":
            return self._send(200, self.app.project_summary())
        if path == "/api/tasks":
            p = self._params().get("project", "")
            if not self.app.valid_project(p): return self._send(400, {"ok": False, "msg": "unknown project"})
            return self._send(200, {"project": p, "tasks": self.app.tasks(p)})
        if path == "/api/nightshift":
            return self._send(200, self.app.nightshift_run(self._params().get("project", "")))
        return self._send(404, {"ok": False, "msg": "not found"})

    def do_POST(self):
        if not self._loopback(): return self._send(403, {"ok": False, "msg": "forbidden"})
        if urlparse(self.path).path != "/action":
            return self._send(404, {"ok": False, "msg": "not found"})
        p = self._params()
        try:
            ok, msg = self.app.action(p.get("project", ""), p.get("cmd", ""), p)
        except Exception as e:
            return self._send(400, {"ok": False, "msg": f"bad request: {e}"})
        return self._send(200 if ok else 400, {"ok": ok, "msg": msg})

    def log_message(self, format, *args): pass


def main():
    ap = argparse.ArgumentParser(prog="devbrain queue", description="localhost TODO-queue dashboard")
    ap.add_argument("--port", type=int, default=8799)
    ap.add_argument("--no-open", action="store_true")
    ap.add_argument("--data", default=os.environ.get("DEVBRAIN_DATA", os.path.expanduser("~/devbrain-data")))
    args = ap.parse_args()
    dash = next((os.path.join(HERE, f) for f in ("devbrain-queue-dashboard.html", "queue-dashboard.html")
                 if os.path.exists(os.path.join(HERE, f))), None)
    if not dash: sys.exit("devbrain queue: queue-dashboard.html not found")
    Handler.app = App(os.path.abspath(args.data), dash)
    if not os.path.isdir(Handler.app.projects_dir()):
        sys.exit(f"devbrain queue: no projects dir at {Handler.app.projects_dir()}")
    url = f"http://127.0.0.1:{args.port}/"
    print(f"devbrain queue → {url}  (Ctrl-C to stop)")
    if not args.no_open:
        try: webbrowser.open(url)
        except Exception: pass
    try: ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()
    except KeyboardInterrupt: print("\nstopped")


if __name__ == "__main__":
    main()
