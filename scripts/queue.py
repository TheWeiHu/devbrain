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
import os, re, sys, glob, json, argparse, datetime, webbrowser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
STATUSES = ["open", "taken", "review", "held", "done"]

def now():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

def find_dashboard():
    for c in ("devbrain-queue-dashboard.html", "queue-dashboard.html"):
        if os.path.exists(os.path.join(HERE, c)): return os.path.join(HERE, c)
    sys.exit("devbrain queue: queue-dashboard.html not found")


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
        if self.path in ("/", "/index.html"):
            return self._send(200, open(self.dashboard, "rb").read(), "text/html; charset=utf-8")
        if self.path == "/api/todos":
            return self._send(200, json.dumps({"projects": self.q.projects(),
                                               "statuses": STATUSES, "tasks": self.q.all_tasks()}))
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
                           "reason": d.get("reason") or None}   # write() handles done_at from status
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
    url = f"http://127.0.0.1:{args.port}/"
    print(f"devbrain queue → {url}  (Ctrl-C to stop)")
    if not args.no_open:
        try: webbrowser.open(url)
        except Exception: pass
    try: ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()
    except KeyboardInterrupt: print("\nstopped")


if __name__ == "__main__":
    main()
