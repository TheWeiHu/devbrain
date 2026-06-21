#!/usr/bin/env bash
# devbrain — queue.py (control-plane server) tests. Boots ONE in-process server
# against a throwaway DEVBRAIN_DATA and asserts the on-disk task file actually
# changed after each verb, plus the localhost-binding and cross-project/traversal
# guards. No sleeps, no real network: the server's listening socket is bound at
# construction, so requests issued right after the serving thread starts connect
# immediately. Every mutation goes through devbrain-todo.sh (one source of truth).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export DEVBRAIN_DATA="$(mktemp -d)"
trap 'rm -rf "$DEVBRAIN_DATA"' EXIT

# Seed two projects so the cross-project write guard is testable.
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
# Import by path under a non-stdlib name (the file is queue.py — `import queue`
# would collide with the standard library's queue module).
spec = importlib.util.spec_from_file_location("devbrain_queue", os.path.join(HERE, "queue.py"))
q = importlib.util.module_from_spec(spec); spec.loader.exec_module(q)
app = q.App(DATA, os.path.join(HERE, "todo.sh"), os.path.join(HERE, "queue-dashboard.html"))

p = f = 0
def check(name, cond):
    global p, f
    if cond: p += 1; print(f"  ok   — {name}")
    else:    f += 1; print(f"  FAIL — {name}")
def field(project, tid, key):                       # re-read from disk each time
    t = next((x for x in app.tasks(project) if x["id"] == tid), None)
    return t.get(key) if t else None

# --- discovery (reads the markdown directly) ---
check("discovers both projects on disk", app.project_keys() == ["proj__a", "proj__b"])
a = app.tasks("proj__a")
check("lists proj__a tasks", len(a) == 2)
check("sorted by priority desc", [t["priority_n"] for t in a] == [90, 20])
alpha, beta = a[0]["id"], a[1]["id"]
other = app.tasks("proj__b")[0]["id"]

# --- mtime+size cache: unchanged files come from cache, a touched file re-parses (0060).
# alpha/beta/other are already cached from the discovery reads above. A fresh poll must
# re-parse nothing; project_summary() must reuse the same cache (no full re-read); and
# bumping one file's mtime must re-parse exactly that file while its sibling stays cached.
n0 = app.parse_count
app.tasks("proj__a")
check("unchanged poll re-parses nothing (served from cache)", app.parse_count == n0)
app.project_summary()
check("project_summary reuses the task cache (no full re-read)", app.parse_count == n0)
apath = os.path.join(DATA, "projects", "proj__a", "todo", alpha + ".md")
st = os.stat(apath); os.utime(apath, ns=(st.st_mtime_ns + 2_000_000_000, st.st_mtime_ns + 2_000_000_000))
n1 = app.parse_count
app.tasks("proj__a")
check("touched file re-parsed, untouched served from cache", app.parse_count == n1 + 1)

# --- every mutation routes through the CLI and changes the file on disk ---
ok, _ = app.run_verb("proj__a", "prio", {"id": alpha, "prio": "55"})
check("prio -> frontmatter updated", ok and field("proj__a", alpha, "priority") == "55")
ok, _ = app.run_verb("proj__a", "edit", {"id": alpha, "title": "renamed", "body": "new\nbody"})
check("edit -> title rewritten", ok and field("proj__a", alpha, "title") == "renamed")
ok, _ = app.run_verb("proj__a", "claim", {"id": beta})
check("claim -> taken", ok and field("proj__a", beta, "status") == "taken")
ok, _ = app.run_verb("proj__a", "review", {"id": beta, "pr": "https://x/pr/1"})
check("review -> review + pr recorded", ok and field("proj__a", beta, "status") == "review"
                                            and field("proj__a", beta, "pr") == "https://x/pr/1")
ok, _ = app.run_verb("proj__a", "done", {"id": beta})
check("done -> done", ok and field("proj__a", beta, "status") == "done")
ok, _ = app.run_verb("proj__a", "hold", {"id": alpha, "reason": "parked: focus"})
check("hold -> held + reason", ok and field("proj__a", alpha, "status") == "held"
                                   and "parked" in (field("proj__a", alpha, "reason") or ""))
suma = next(x for x in app.project_summary() if x["key"] == "proj__a")
check("summary splits parked from held", suma["held"] == 1 and suma["parked"] == 1)
ok, _ = app.run_verb("proj__a", "release", {"id": alpha})
check("release -> open", ok and field("proj__a", alpha, "status") == "open")
ok, _ = app.run_verb("proj__a", "context", {"id": alpha, "body": "remember this"})
check("context -> appended to body", ok and "remember this" in (field("proj__a", alpha, "body") or ""))
ok, _ = app.run_verb("proj__a", "add", {"title": "fresh task", "prio": "33", "body": "why"})
check("add -> new task created", ok and len(app.tasks("proj__a")) == 3)

# --- guards: bad project / traversal / unknown verb / cross-project write ---
check("unknown project rejected",    not app.run_verb("nope__x", "prio", {"id": alpha, "prio": "1"})[0])
check("traversal id rejected",       not app.run_verb("proj__a", "prio", {"id": "../../../etc/passwd", "prio": "1"})[0])
check("unknown verb rejected",       not app.run_verb("proj__a", "bogus", {"id": alpha})[0])
ok, _ = app.run_verb("proj__a", "prio", {"id": other, "prio": "1"})   # proj__b's id via proj__a
check("cross-project write rejected", not ok and field("proj__b", other, "priority") == "50")

# --- nightshift monitor link: active ONLY when the recorded run port is actually live ---
import socket as _sock
nsfile = os.path.join(DATA, "projects", "proj__a", "nightshift-run.json")
check("no run file -> inactive", app.nightshift_run("proj__a") == {"active": False})
with open(nsfile, "w") as fh: json.dump({"port": 1, "repo": "/tmp/x"}, fh)   # port 1: nothing listens
check("dead run port -> inactive", app.nightshift_run("proj__a")["active"] is False)
lsock = _sock.socket(); lsock.bind(("127.0.0.1", 0)); lsock.listen(); live = lsock.getsockname()[1]
with open(nsfile, "w") as fh: json.dump({"port": live, "repo": "/tmp/x"}, fh)
nsr = app.nightshift_run("proj__a")
check("live run port -> active w/ monitor url",
      nsr["active"] and nsr["url"] == f"http://127.0.0.1:{live}/index.html" and nsr["port"] == live)
lsock.close()
check("unknown project -> inactive", app.nightshift_run("nope__x") == {"active": False})
os.remove(nsfile)

# --- stale-CLI guard: an old todo.sh that prints its usage banner and exits 0 on
# an unknown verb must NOT read as success (else the UI toasts a no-op as done).
check("looks_like_usage spots the menu",
      q.looks_like_usage("todo add x\ntodo list\ntodo done y"))
check("looks_like_usage ignores a terse ok line", not q.looks_like_usage("claimed 0001-foo"))
stale = os.path.join(DATA, "stale-todo.sh")
with open(stale, "w") as fh:
    fh.write("#!/usr/bin/env bash\ncat <<'EOF'\n"
             "todo add\ntodo list\ntodo claim\ntodo done\nEOF\nexit 0\n")   # usage + exit 0
os.chmod(stale, 0o755)
stale_app = q.App(DATA, stale, os.path.join(HERE, "queue-dashboard.html"))
sok, smsg = stale_app.run_verb("proj__a", "claim", {"id": alpha})
check("stale CLI (usage + exit 0) -> error, not success", not sok and "stale" in smsg)

# --- find_todo prefers the sibling repo copy over a stale INSTALLED hook (0046).
# A checkout's installed ~/.claude/hooks/devbrain-todo.sh can lag the checkout; if
# it won the lookup, a real `devbrain queue` run would no-op edits/prio. Point HOME
# at a fake home holding a stale hook and assert find_todo() still picks this repo's
# copy, and that a mutation through that resolved CLI actually lands on disk.
fakehome = os.path.join(DATA, "fakehome")
os.makedirs(os.path.join(fakehome, ".claude", "hooks"), exist_ok=True)
installed = os.path.join(fakehome, ".claude", "hooks", "devbrain-todo.sh")
with open(installed, "w") as fh:
    fh.write("#!/usr/bin/env bash\ncat <<'EOF'\ntodo add\ntodo list\ntodo claim\nEOF\nexit 0\n")
os.chmod(installed, 0o755)
old_home = os.environ.get("HOME"); old_dt = os.environ.pop("DEVBRAIN_TODO", None)
os.environ["HOME"] = fakehome
try:
    check("find_todo prefers sibling repo todo.sh over stale installed hook",
          q.find_todo() == os.path.join(HERE, "todo.sh"))
    resolved_app = q.App(DATA, q.find_todo(), os.path.join(HERE, "queue-dashboard.html"))
    rok, _ = resolved_app.run_verb("proj__a", "prio", {"id": alpha, "prio": "42"})
    check("stale installed hook present -> UI reprioritize still mutates the file",
          rok and field("proj__a", alpha, "priority") == "42")
finally:
    if old_home is not None: os.environ["HOME"] = old_home
    if old_dt is not None: os.environ["DEVBRAIN_TODO"] = old_dt

# --- one in-process server boot: endpoints + localhost binding ---
q.Handler.app = app
srv = ThreadingHTTPServer(("127.0.0.1", 0), q.Handler)     # listening socket bound here
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

# --- DNS-rebinding guard: a forged non-loopback Host/Origin is refused, no write.
# The TCP connection still lands on 127.0.0.1; only the browser-supplied header is
# spoofed (exactly what a malicious local page would do). Must 403 and touch nothing.
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
