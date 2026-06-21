#!/usr/bin/env bash
# devbrain — queue.py (dashboard server) tests. Boots ONE in-process server against a
# throwaway DEVBRAIN_DATA and asserts the on-disk task file actually changed after each
# verb, plus the localhost-binding and cross-project/traversal guards. No sleeps, no real
# network. The server edits the .md files directly (no CLI), so a mutation = a field
# rewrite; we seed tasks with the CLI only because it's handy.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export DEVBRAIN_DATA="$(mktemp -d)"
trap 'rm -rf "$DEVBRAIN_DATA"' EXIT

mkdir -p "$DEVBRAIN_DATA/projects/proj__a/todo" "$DEVBRAIN_DATA/projects/proj__b/todo"
DEVBRAIN_PROJECT=proj__a bash "$HERE/todo.sh" add "alpha task" -p 90 -b "alpha body" >/dev/null
DEVBRAIN_PROJECT=proj__a bash "$HERE/todo.sh" add "beta chore" -p 20 >/dev/null
DEVBRAIN_PROJECT=proj__b bash "$HERE/todo.sh" add "other proj task" -p 50 >/dev/null

HERE="$HERE" python3 - <<'PY'
import os, sys, json, threading, importlib.util
from urllib.request import urlopen, Request
from urllib.error import HTTPError
from http.server import ThreadingHTTPServer

HERE = os.environ["HERE"]; DATA = os.environ["DEVBRAIN_DATA"]
spec = importlib.util.spec_from_file_location("devbrain_queue", os.path.join(HERE, "queue.py"))
q = importlib.util.module_from_spec(spec); spec.loader.exec_module(q)
app = q.App(DATA, os.path.join(HERE, "queue-dashboard.html"))

p = f = 0
def check(name, cond):
    global p, f
    if cond: p += 1; print(f"  ok   — {name}")
    else:    f += 1; print(f"  FAIL — {name}")
def field(project, tid, key):
    t = next((x for x in app.tasks(project) if x["id"] == tid), None)
    return t.get(key) if t else None

# --- discovery (reads the markdown directly) ---
check("discovers both projects on disk", app.project_keys() == ["proj__a", "proj__b"])
a = app.tasks("proj__a")
check("lists proj__a tasks", len(a) == 2)
check("sorted by priority desc", [t["priority_n"] for t in a] == [90, 20])
alpha, beta = a[0]["id"], a[1]["id"]
other = app.tasks("proj__b")[0]["id"]

# --- every mutation edits the .md on disk directly ---
ok, _ = app.action("proj__a", "prio", {"id": alpha, "prio": "55"})
check("prio -> frontmatter updated", ok and field("proj__a", alpha, "priority") == "55")
check("prio rejects out of range", not app.action("proj__a", "prio", {"id": alpha, "prio": "999"})[0])
ok, _ = app.action("proj__a", "edit", {"id": alpha, "title": "renamed", "body": "new\nbody"})
check("edit -> title rewritten", ok and field("proj__a", alpha, "title") == "renamed")
check("edit -> body rewritten",  "new" in (field("proj__a", alpha, "body") or ""))
ok, _ = app.action("proj__a", "claim", {"id": beta})
check("claim -> taken", ok and field("proj__a", beta, "status") == "taken")
ok, _ = app.action("proj__a", "review", {"id": beta, "pr": "https://x/pr/1"})
check("review -> review + pr recorded", ok and field("proj__a", beta, "status") == "review"
                                            and field("proj__a", beta, "pr") == "https://x/pr/1")
ok, _ = app.action("proj__a", "done", {"id": beta})
check("done -> done + done_at", ok and field("proj__a", beta, "status") == "done"
                                    and field("proj__a", beta, "done_at"))
ok, _ = app.action("proj__a", "release", {"id": beta})
check("release clears pr + done_at (no zombie)", field("proj__a", beta, "status") == "open"
      and not field("proj__a", beta, "pr") and not field("proj__a", beta, "done_at"))
ok, _ = app.action("proj__a", "hold", {"id": alpha, "reason": "parked: focus"})
check("hold -> held + reason", ok and field("proj__a", alpha, "status") == "held"
                                   and "parked" in (field("proj__a", alpha, "reason") or ""))
suma = next(x for x in app.project_summary() if x["key"] == "proj__a")
check("summary splits parked from held", suma["held"] == 1 and suma["parked"] == 1)
ok, _ = app.action("proj__b", "hold", {"id": other, "reason": "blocked on review"})
summ = app.project_summary()
sumb = next(x for x in summ if x["key"] == "proj__b")
check("genuine hold counts as held, not parked", sumb["held"] == 1 and sumb["parked"] == 0)
app.action("proj__b", "release", {"id": other})
app.action("proj__a", "release", {"id": alpha})
check("release -> open", field("proj__a", alpha, "status") == "open")
ok, _ = app.action("proj__a", "context", {"id": alpha, "body": "remember this"})
check("context -> appended to body", ok and "remember this" in (field("proj__a", alpha, "body") or ""))
ok, _ = app.action("proj__a", "add", {"title": "fresh task", "prio": "33", "body": "why"})
check("add -> new task created", ok and len(app.tasks("proj__a")) == 3)

# --- guards: bad project / traversal / unknown verb / cross-project write ---
check("unknown project rejected",     not app.action("nope__x", "prio", {"id": alpha, "prio": "1"})[0])
check("traversal id rejected",        not app.action("proj__a", "prio", {"id": "../../../etc/passwd", "prio": "1"})[0])
check("unknown verb rejected",        not app.action("proj__a", "bogus", {"id": alpha})[0])
ok, _ = app.action("proj__a", "prio", {"id": other, "prio": "1"})   # proj__b's id via proj__a
check("cross-project write rejected", not ok and field("proj__b", other, "priority") == "50")

# --- nightshift monitor link: present iff the project recorded a run port ---
nsfile = os.path.join(DATA, "projects", "proj__a", "nightshift-run.json")
check("no run file -> inactive", app.nightshift_run("proj__a") == {"active": False})
with open(nsfile, "w") as fh: json.dump({"port": 8788}, fh)
nsr = app.nightshift_run("proj__a")
check("run file -> active with monitor url",
      nsr["active"] and nsr["url"] == "http://127.0.0.1:8788/index.html")
check("unknown project -> inactive", app.nightshift_run("nope__x") == {"active": False})
os.remove(nsfile)

# --- one in-process server boot: endpoints + localhost binding + DNS-rebinding guard ---
q.Handler.app = app
srv = ThreadingHTTPServer(("127.0.0.1", 0), q.Handler)
check("server binds 127.0.0.1 only", srv.server_address[0] == "127.0.0.1")
base = f"http://127.0.0.1:{srv.server_address[1]}"
threading.Thread(target=srv.serve_forever, daemon=True).start()
def get(path): return json.loads(urlopen(base + path, timeout=5).read())

check("GET /api/projects lists projects",
      {x["key"] for x in get("/api/projects")} == {"proj__a", "proj__b"})
check("GET /api/tasks returns tasks", len(get("/api/tasks?project=proj__a")["tasks"]) >= 1)
try: get("/api/tasks?project=evil__x"); bad = False
except HTTPError as e: bad = (e.code == 400)
check("GET /api/tasks bad project -> 400", bad)
req = Request(base + "/action",
              data=json.dumps({"project": "proj__a", "cmd": "prio", "id": other, "prio": "1"}).encode(),
              headers={"Content-Type": "application/json"})
try: urlopen(req, timeout=5); code = 200
except HTTPError as e: code = e.code
check("POST /action cross-project -> 400", code == 400)

before = field("proj__a", alpha, "priority")
def forge(header):
    r = Request(base + "/action",
                data=json.dumps({"project": "proj__a", "cmd": "prio", "id": alpha, "prio": "7"}).encode(),
                headers={"Content-Type": "application/json", **header})
    try: urlopen(r, timeout=5); return 200
    except HTTPError as e: return e.code
check("POST /action forged Host -> 403",   forge({"Host": "evil.example"}) == 403)
check("POST /action forged Origin -> 403", forge({"Origin": "http://evil.example"}) == 403)
check("forged requests wrote nothing",     field("proj__a", alpha, "priority") == before)
srv.shutdown()

print(f"== {p} passed, {f} failed ==")
sys.exit(1 if f else 0)
PY
