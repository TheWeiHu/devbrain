#!/usr/bin/env python3
"""nightshift dashboard server — serves .nightshift/ AND handles task actions.

Plain `python3 -m http.server` is static, so dashboard buttons can't do anything.
This adds a localhost-only /action endpoint that runs the devbrain-todo verb for a
task (approve / release / drop), so the dashboard's buttons work.

Usage: nightshift-serve.py <repo> [port]   (binds 127.0.0.1 only)
"""
import sys, os, re, json, subprocess
from functools import partial
from urllib.parse import urlparse, parse_qs
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer

REPO = os.path.abspath(sys.argv[1]) if len(sys.argv) > 1 else os.path.expanduser("~/drain/chess-equity")
PORT = int(sys.argv[2]) if len(sys.argv) > 2 else 8787
DOCROOT = os.path.join(REPO, ".nightshift")
TODO = os.path.expanduser("~/.claude/hooks/devbrain-todo.sh")
if not os.access(TODO, os.X_OK):
    TODO = os.path.join(os.path.dirname(os.path.abspath(__file__)), "todo.sh")
VERB = {"approve": "approve", "release": "release", "drop": "done"}   # drop = mark done (won't-do)
IDRE = re.compile(r"^[0-9]{4}-[a-z0-9-]+$")

class Handler(SimpleHTTPRequestHandler):
    def _act(self):
        q = parse_qs(urlparse(self.path).query)
        cmd = (q.get("cmd") or [""])[0]
        tid = (q.get("id") or [""])[0]
        if cmd not in VERB or not IDRE.match(tid):
            return self._json(400, {"ok": False, "msg": "bad cmd/id"})
        try:
            r = subprocess.run([TODO, VERB[cmd], tid], cwd=REPO, capture_output=True, text=True, timeout=25)
            return self._json(200, {"ok": r.returncode == 0, "msg": (r.stdout or r.stderr).strip()})
        except Exception as e:
            return self._json(500, {"ok": False, "msg": str(e)})

    def end_headers(self):
        # Never let the browser cache index.html/status.json — stale pages were why
        # the action buttons "did nothing" (an old cached HTML with no/old handlers).
        self.send_header("Cache-Control", "no-store, max-age=0")
        super().end_headers()

    def _json(self, code, obj):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        if self.path.startswith("/action"):
            return self._act()
        return super().do_GET()

    def do_POST(self):
        return self._act() if self.path.startswith("/action") else self.send_error(404)

    def log_message(self, format, *args):
        pass

os.makedirs(DOCROOT, exist_ok=True)
ThreadingHTTPServer(("127.0.0.1", PORT), partial(Handler, directory=DOCROOT)).serve_forever()
