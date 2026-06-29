#!/usr/bin/env bash
# devbrain — queue.py (kanban server) tests. Reads + writes the task .md files directly
# (no CLI); asserts the on-disk file changes after save/create/delete and that frontmatter
# key ORDER is preserved, plus the HTTP endpoints, traversal guards, and loopback binding.
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
qu = q.Queue(DATA)

p = f = 0
def check(name, cond):
    global p, f
    if cond: p += 1; print(f"  ok   — {name}")
    else:    f += 1; print(f"  FAIL — {name}")
def get(project, tid):
    return next((t for t in qu.all_tasks() if t["id"] == tid and t["project"] == project), None)

# --- discovery + parse ---
check("discovers both projects", qu.projects() == ["proj__a", "proj__b"])
tasks = qu.all_tasks()
check("lists all tasks across projects", len(tasks) == 3)
check("sorted by priority desc", [t["priority"] for t in tasks[:2]] == [90, 50])
alpha = get("proj__a", next(t["id"] for t in tasks if t["project"]=="proj__a" and t["priority"]==90))["id"]
beta  = next(t["id"] for t in tasks if t["project"]=="proj__a" and t["priority"]==20)
other = next(t["id"] for t in tasks if t["project"]=="proj__b")

# --- save: status/priority/reason/title/body all land; frontmatter ORDER preserved ---
qu.write("proj__a", alpha, {"status": "held", "priority": "55", "reason": "blocked: x"}, "renamed", "new\nbody")
a = get("proj__a", alpha)
check("save -> status changed", a["status"] == "held")
check("save -> priority changed", a["priority"] == 55)
check("save -> reason set", a["reason"] == "blocked: x")
check("save -> title rewritten", a["title"] == "renamed")
check("save -> body rewritten", "new" in a["body"])
order = open(os.path.join(DATA, "projects", "proj__a", "todo", alpha + ".md")).read().split("---")[1]
check("frontmatter key order preserved (id first)", order.strip().startswith("id:"))

# done sets done_at; moving off done clears it (no zombie)
qu.write("proj__a", beta, {"status": "done"}, "beta chore", "")
check("done -> done_at stamped", bool(get("proj__a", beta)["done_at"]))
qu.write("proj__a", beta, {"status": "open"}, "beta chore", "")
check("moving off done clears done_at", not get("proj__a", beta)["done_at"])

# approved flag (greenlight unattended pickup) round-trips: set writes `approved: true`, clear removes it
qu.write("proj__a", beta, {"approved": "true"}, "beta chore", "")
check("approve -> approved true", get("proj__a", beta)["approved"] is True)
qu.write("proj__a", beta, {"approved": None}, "beta chore", "")
check("un-approve -> approved cleared", get("proj__a", beta)["approved"] is False)

# --- create + delete ---
t = qu.create("proj__a", "fresh task", 33, "why")
check("create -> new task with next id", t["status"] == "open" and len(qu.all_tasks()) == 4)
check("create clamps priority 0-100", qu.create("proj__a", "huge", 9999, "")["priority"] == 100)
nid = t["id"]
check("delete -> file removed", qu.delete("proj__a", nid) and get("proj__a", nid) is None)

# --- guards: unknown project / traversal ---
try: qu.write("nope__x", alpha, {"status": "open"}, "x", ""); ok = False
except Exception: ok = True
check("save to unknown project rejected", ok)
try: qu.write("proj__a", "../../../etc/passwd", {"status": "open"}, "x", ""); ok = False
except Exception: ok = True
check("traversal id rejected", ok)

# --- nightshift: lists every project with a live fleet, independent of any filter ---
check("nightshift empty when no runs", qu.nightshift() == {"runs": []})
repo = os.path.join(DATA, "repo"); os.makedirs(os.path.join(repo, ".nightshift"))
json.dump({"port": 8799, "repo": repo}, open(os.path.join(DATA, "projects", "proj__a", "nightshift-run.json"), "w"))
json.dump({"running": True, "workers": [{"i": 0, "state": "working"}]},
          open(os.path.join(repo, ".nightshift", "status.json"), "w"))
ns = qu.nightshift()
check("nightshift lists the live fleet", len(ns["runs"]) == 1 and ns["runs"][0]["project"] == "proj__a"
      and len(ns["runs"][0]["workers"]) == 1)

# self-heal: a phantom registration (stopped + status.json gone stale) is pruned off disk
import datetime as _dt
stale = os.path.join(DATA, "stale-repo"); os.makedirs(os.path.join(stale, ".nightshift"))
sf = os.path.join(DATA, "projects", "proj__b", "nightshift-run.json")
json.dump({"port": 8799, "repo": stale}, open(sf, "w"))
old = (_dt.datetime.now(_dt.timezone.utc) - _dt.timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ")
json.dump({"running": False, "updated": old}, open(os.path.join(stale, ".nightshift", "status.json"), "w"))
ns = qu.nightshift()
check("stale (stopped) fleet pruned from the list", all(r["project"] != "proj__b" for r in ns["runs"]))
check("stale registration file deleted off disk", not os.path.exists(sf))
check("live fleet survives the prune", len(ns["runs"]) == 1 and ns["runs"][0]["project"] == "proj__a")
# a registration whose repo was deleted is also pruned (no status.json, repo dir gone)
gone = os.path.join(DATA, "projects", "proj__b", "nightshift-run.json")
json.dump({"port": 8799, "repo": os.path.join(DATA, "vanished")}, open(gone, "w"))
qu.nightshift()
check("registration for a vanished repo deleted", not os.path.exists(gone))

# --- HTTP: endpoints + loopback (DNS-rebinding) guard ---
q.Handler.q = qu; q.Handler.dashboard = os.path.join(HERE, "dashboard.html")
srv = ThreadingHTTPServer(("127.0.0.1", 0), q.Handler)
check("server binds 127.0.0.1 only", srv.server_address[0] == "127.0.0.1")
base = f"http://127.0.0.1:{srv.server_address[1]}"
threading.Thread(target=srv.serve_forever, daemon=True).start()
todos = json.loads(urlopen(base + "/api/todos", timeout=5).read())
check("GET /api/todos returns tasks+projects+statuses",
      "tasks" in todos and todos["projects"] == ["proj__a", "proj__b"] and len(todos["statuses"]) == 5)
# /api/whoami: identity probe `nightshift watch` uses to spot a FOREIGN queue squatting the port.
who = json.loads(urlopen(base + "/api/whoami", timeout=5).read())
check("GET /api/whoami reports server + this data dir",
      who["server"] == "devbrain-queue" and os.path.realpath(who["data"]) == os.path.realpath(DATA)
      and isinstance(who["pid"], int))
# Root serves the dashboard even with a ?project= query (the `devbrain nightshift watch` deep-link form).
root = urlopen(base + "/?project=proj__a", timeout=5)
check("GET /?project=… serves dashboard (200)", root.status == 200 and b"<html" in root.read().lower())
def post(path, body, headers=None):
    r = Request(base + path, data=json.dumps(body).encode(),
                headers={"Content-Type": "application/json", **(headers or {})})
    try: return urlopen(r, timeout=5).status
    except HTTPError as e: return e.code
check("POST /api/save mutates", post("/api/save", {"project": "proj__b", "id": other, "title": "x", "body": "",
                                     "priority": 5, "status": "taken", "reason": ""}) == 200
      and get("proj__b", other)["status"] == "taken")
check("POST /api/save approves", post("/api/save", {"project": "proj__b", "id": other, "title": "x", "body": "",
                                     "priority": 5, "status": "held", "reason": "", "approved": True}) == 200
      and get("proj__b", other)["approved"] is True)
check("POST forged Host -> 403", post("/api/save", {"project": "proj__b", "id": other, "title": "x",
                                       "body": "", "priority": 5, "status": "open"}, {"Host": "evil.example"}) == 403)

# --- global preferences page: GET (absent), POST (write+create dir), GET (present) ---
pref0 = json.loads(urlopen(base + "/api/preferences", timeout=5).read())
check("GET /api/preferences absent -> exists false, empty", pref0["exists"] is False and pref0["content"] == "")
check("preferences path is preferences/global.md", pref0["path"].endswith("/preferences/global.md"))
check("POST /api/preferences writes", post("/api/preferences", {"content": "# Prefs\n\n- No warm colors.\n"}) == 200)
check("preferences file created on disk",
      open(os.path.join(DATA, "preferences", "global.md")).read() == "# Prefs\n\n- No warm colors.\n")
pref1 = json.loads(urlopen(base + "/api/preferences", timeout=5).read())
check("GET /api/preferences present -> exists true + content", pref1["exists"] is True and "No warm colors" in pref1["content"])
check("POST /api/preferences rejects non-string", post("/api/preferences", {"content": 5}) == 400)
# edit history: each save appends a `· you` diff entry to edits.md so /distill can SEE what
# changed (and never re-add a steer you deleted) — replaces the old hash/key ledgers.
histf = os.path.join(DATA, "preferences", "edits.md")
hist = open(histf).read()
check("first save logs additions to the history", "· you" in hist and "+- No warm colors." in hist)
# a save that removes a bullet and adds another records BOTH the deletion and the addition
post("/api/preferences", {"content": "# Prefs\n\n- Prefer teal accents.\n"})
hist = open(histf).read()
check("history records a removal (- line)", "-- No warm colors." in hist)
check("history records an addition (+ line)", "+- Prefer teal accents." in hist)
entries = lambda s: sum(1 for ln in s.splitlines() if ln.startswith("## ") and ln[3:4].isdigit())
n = entries(hist)
# an identical re-save changes nothing -> no diff -> no new entry
post("/api/preferences", {"content": "# Prefs\n\n- Prefer teal accents.\n"})
check("a no-op save logs nothing", entries(open(histf).read()) == n)

# --- prompt self-portrait reader: classification by session origin + text ---
import datetime
today = datetime.date.today().isoformat()
logdir = os.path.join(DATA, "projects", "proj__a", "log", today); os.makedirs(logdir)
# interactive session (normal cwd): your prose = human, the slash-command you ran = command
open(os.path.join(logdir, "edmonton.sess.md"), "w").write(
    "# header\n> worktree: edmonton · cwd: /Users/x/conductor/edmonton · times in UTC\n\n"
    "## 09:15:00\n\nhow do we fix the parser?\n\n"
    "↳ 09:16 — a model response summary that must be ignored\n"
    "   touched: x.py  ·  tools: Skill:distill×1, Bash×3\n"     # autonomous: no leading slash, skill named in meta
    "   ⤷ response sample:\n"
    "   > I wrote tools: Skill×9 and Skill:ship×4 into the meta line.\n\n"  # PROSE quote — must NOT be counted
    "## 09:20:00\n\n/continue\n\n"
    "↳ 09:21 — another summary\n"
    "   tools: Skill×1\n\n"                                       # older log: call recorded, name unknown (?)
    "## 09:25:00\n\nPLANNING TURN: do not write code\n\n"
    "## 09:30:00\n\ncommit and push it\n")
# autonomous nightshift worker session (cwd under ~/nightshift/): prose is STILL a bot turn
open(os.path.join(logdir, "proj-a-w2.ns.md"), "w").write(
    "# header\n> worktree: proj-a-w2 · cwd: /Users/x/nightshift/proj-a-w2 · times in UTC\n\n"
    "## 10:00:00\n\nadd a minimal test\n")
scan = q.scan_prompts(DATA, days=30)
kinds = {r["x"]: r["kind"] for r in scan}
check("interactive prose -> human", kinds["how do we fix the parser?"] == "human")
check("interactive slash -> command (not bot)", kinds["/continue"] == "command")
check("planning text -> nightshift", kinds["PLANNING TURN: do not write code"] == "nightshift")
check("autonomous session prose -> nightshift", kinds["add a minimal test"] == "nightshift")
check("scan strips the response line", all("model response" not in r["x"] for r in scan))
sk = {r["x"]: r.get("sk") for r in scan}
check("meta-named skill parsed off the turn (prose quote NOT counted)", sk["how do we fix the parser?"] == ["distill"])
check("unnamed Skill meta -> '?' placeholder", sk["/continue"] == ["?"])
check("turns with no skill meta -> empty list", sk["commit and push it"] == [])

typed = sorted(r["x"] for r in q.parse_prompts(DATA, days=30, kind="typed"))
check("typed = your prose + your slash-commands",
      typed == ["/continue", "commit and push it", "how do we fix the parser?"])
botp = sorted(r["x"] for r in q.parse_prompts(DATA, days=30, kind="bot"))
check("bot = nightshift + harness", botp == ["PLANNING TURN: do not write code", "add a minimal test"])
check("kind=all keeps everything", len(q.parse_prompts(DATA, days=30, kind="all")) == 5)

# classify(): text-only kind, with autonomous override
check("classify slash interactive -> command", q.classify("/continue") == "command")
check("classify slash autonomous -> nightshift", q.classify("/continue", True) == "nightshift")
check("classify prose autonomous -> nightshift", q.classify("merged", True) == "nightshift")
check("classify harness anywhere -> system", q.classify("<task-notification> x", True) == "system")
check("classify skip empty", q.classify("   ") is None)
check("session_is_autonomous by cwd/worktree", q.session_is_autonomous("/Users/x/drain/foo-w1", "foo-w1"))
check("session_is_autonomous false for normal cwd", not q.session_is_autonomous("/Users/x/conductor/edmonton", "edmonton"))

# date window
oldd = os.path.join(DATA, "projects", "proj__a", "log", "2020-01-01"); os.makedirs(oldd)
open(os.path.join(oldd, "x.s.md"), "w").write("## 01:00:00\n\nancient prompt\n")
check("windows by days", "ancient prompt" not in [r["x"] for r in q.parse_prompts(DATA, days=30)])
check("days=0 means all history", "ancient prompt" in [r["x"] for r in q.parse_prompts(DATA, days=0)])

# gbrain read/value log: read-vs-write, hits, slugs, topic extraction, windowing
gblog = os.path.join(DATA, "projects", "proj__a", "gbrain-queries.log")
open(gblog, "w").write("\n".join([
    json.dumps({"ts": today + "T10:00:00Z", "project": "proj__a", "cmd": 'gbrain search "edge cases retry"', "modes": ["search"], "hits": 3, "slugs": ["proj__a/impl", "proj__a/impl"]}),
    json.dumps({"ts": today + "T10:05:00Z", "project": "proj__a", "cmd": 'gbrain put "$x"', "modes": ["put"], "hits": 0, "slugs": []}),
    json.dumps({"ts": "2020-01-01T00:00:00Z", "project": "proj__a", "cmd": 'gbrain query "ancient"', "modes": ["query"], "hits": 0, "slugs": []}),
]) + "\n")
gq = q.gbrain_queries(DATA, days=0)
check("gbrain_queries parses every entry", len(gq) == 3)
check("gbrain read = search/query/get, not put", sum(1 for r in gq if r["read"]) == 2)
check("gbrain extracts topic + hits + slugs", any(r["q"] == "edge cases retry" and r["hits"] == 3 and r["slugs"] == ["proj__a/impl", "proj__a/impl"] for r in gq))
check("gbrain windows by days", all("2020" not in r["ts"] for r in q.gbrain_queries(DATA, days=30)))
gapi = json.loads(urlopen(base + "/api/gbrain", timeout=5).read())
check("GET /api/gbrain returns queries", len(gapi["queries"]) == 3)

# gb_get_target: name the page a `gbrain get` tried to read (display-only), parsed
# from the logged command. Slug-shape filtered so prose mentions don't surface junk.
check("get target: plain slug",        q.gb_get_target('gbrain get "proj__a/page" --fuzzy') == "proj__a/page")
check("get target: cmd substitution",  q.gb_get_target('body=$(gbrain get proj__a/page)') == "proj__a/page")
check("get target: chained after echo", q.gb_get_target('echo hi; gbrain get proj__a/smoke-testing 2>&1') == "proj__a/smoke-testing")
check("get target: quoted query is NOT a get", q.gb_get_target('gbrain search "why is gbrain get a miss"') == "")
check("get target: prose 'gbrain get as' has no slug -> empty", q.gb_get_target('credit a gbrain get as a hit') == "")
check("get target: bare name (no slash) rejected", q.gb_get_target('gbrain get pagename') == "")
check("get target: --help with redirection is not a page", q.gb_get_target('gbrain get --help 2>&1') == "")
check("get target: redirection fd not mistaken for slug", q.gb_get_target('gbrain get proj__a/page 2>&1 | head') == "proj__a/page")
check("get target: unparseable cmd -> no fabricated page",
      q.gb_get_target('gbrain search "why gbrain get proj__a/missing" ; echo don\'t') == "")
check("get target: option-only get before a real get finds the real one",
      q.gb_get_target('gbrain get --help; gbrain get proj__a/page') == "proj__a/page")
check("get target: quoted command substitution",
      q.gb_get_target('echo "$(gbrain get proj__a/page)"') == "proj__a/page")
check("get target: assigned quoted cmd-subst",
      q.gb_get_target('body="$(gbrain get proj__a/page)"; echo "$body"') == "proj__a/page")
check("get target: backtick substitution",
      q.gb_get_target('echo `gbrain get proj__a/page`') == "proj__a/page")
check("get target: query that IS the verb words is not a get",
      q.gb_get_target('gbrain search "gbrain get proj__a/page"') == "")
check("get target: chained get inside quoted substitution",
      q.gb_get_target('echo "$(cd repo && gbrain get proj__a/page)"') == "proj__a/page")
check("get target: path-prefixed get inside quoted substitution",
      q.gb_get_target('echo "$(/home/u/.bun/bin/gbrain get proj__a/page)"') == "proj__a/page")
# end-to-end: a not-found get exposes its attempted page via the `target` field.
open(gblog, "a").write(json.dumps({"ts": today + "T11:00:00Z", "project": "proj__a",
    "cmd": 'gbrain get "proj__a/missing" --fuzzy', "modes": ["get"], "hits": 0, "slugs": []}) + "\n")
gq2 = q.gbrain_queries(DATA, days=0)
check("get-miss record carries its target page",
      any(r["modes"] == ["get"] and r["hits"] == 0 and r["target"] == "proj__a/missing" for r in gq2))
check("non-get record has empty target",
      all(r["target"] == "" for r in gq2 if "get" not in r["modes"]))

# token usage sidecar (powers the Profile Token Cost card): parse, dedup, window
toklog = os.path.join(DATA, "projects", "proj__a", "tokens.jsonl")
open(toklog, "w").write("\n".join([
    json.dumps({"ts": today + "T10:00:00Z", "session": "s1", "model": "claude-opus-4-8", "in": 100, "out": 200, "cache_create": 0, "cache_read": 5000, "auto": True}),
    json.dumps({"ts": today + "T10:00:00Z", "session": "s1", "model": "claude-opus-4-8", "in": 100, "out": 200, "cache_create": 0, "cache_read": 5000, "auto": True}),  # exact dup (session,ts) -> dropped
    json.dumps({"ts": today + "T11:00:00Z", "session": "s2", "model": "claude-sonnet-4-6", "in": 10, "out": 20, "cache_create": 0, "cache_read": 0}),  # no auto -> interactive
    json.dumps({"ts": "2020-01-01T00:00:00Z", "session": "s0", "model": "claude-haiku-4-5", "in": 1, "out": 1, "cache_create": 0, "cache_read": 0}),
]) + "\n")
tu = q.token_usage(DATA, days=0)
check("token_usage dedups (session,ts)", len(tu) == 3)               # 4 lines, one exact dup dropped
check("token_usage carries model + fields", any(r["model"] == "claude-opus-4-8" and r["out"] == 200 and r["cr"] == 5000 for r in tu))
check("token_usage carries auto (bot vs interactive)",
      any(r["model"] == "claude-opus-4-8" and r["auto"] is True for r in tu)
      and any(r["model"] == "claude-sonnet-4-6" and r["auto"] is False for r in tu))
check("token_usage windows by days", all("2020" not in r["ts"] for r in q.token_usage(DATA, days=30)))
tapi = json.loads(urlopen(base + "/api/tokens", timeout=5).read())
check("GET /api/tokens returns usage", len(tapi["usage"]) == 3)

# HTTP
api = json.loads(urlopen(base + "/api/prompts?days=30", timeout=5).read())
check("GET /api/prompts defaults to typed", api["kind"] == "typed" and len(api["prompts"]) == 3)
allapi = json.loads(urlopen(base + "/api/prompts?days=30&kind=all", timeout=5).read())
check("GET /api/prompts?kind=all returns typed/bot counts",
      allapi["counts"] == {"typed": 3, "bot": 2} and len(allapi["prompts"]) == 5)
botapi = json.loads(urlopen(base + "/api/prompts?days=30&kind=bot", timeout=5).read())
check("GET /api/prompts?kind=bot filters", len(botapi["prompts"]) == 2
      and all(r["kind"] not in ("human", "command") for r in botapi["prompts"]))
junk = json.loads(urlopen(base + "/api/prompts?kind=evil", timeout=5).read())
check("GET /api/prompts bad kind -> typed", junk["kind"] == "typed")

# --- port self-heal: reuse a running queue, else step to the next free port ---
live_port = srv.server_address[1]
check("is_devbrain_queue true for the live server", q.is_devbrain_queue(live_port) is True)
check("is_devbrain_queue false for an unused port", q.is_devbrain_queue(0) is False)
# select_port: pure control flow, I/O injected (no real sockets, no sleeps)
BOUND = object()
def fake_bind(taken): return lambda p: (None if p in taken else BOUND)   # None = port busy
check("select_port: uses the requested port when free",
      q.select_port(9000, 20, fake_bind(set()), lambda p: False) == ("serve", BOUND, 9000))
check("select_port: steps past busy ports to the next free one",
      q.select_port(9000, 20, fake_bind({9000, 9001}), lambda p: False) == ("serve", BOUND, 9002))
check("select_port: reuses a busy port that IS a devbrain queue",
      q.select_port(9000, 20, fake_bind({9000}), lambda p: p == 9000) == ("reuse", None, 9000))
check("select_port: gives up when every port is busy & non-reusable",
      q.select_port(9000, 3, fake_bind({9000, 9001, 9002}), lambda p: False) == ("none", None, None))

srv.shutdown()

# --- project_repo + start_nightshift (drag-to-🌙 launch) ---
import subprocess
ld = os.path.join(DATA, "projects", "proj__a", "log", "2026-06-25"); os.makedirs(ld, exist_ok=True)
checkout = os.path.join(DATA, "checkout-a"); os.makedirs(os.path.join(checkout, ".git"), exist_ok=True)
interactive = os.path.join(ld, "amsterdam.sess1.md")
open(interactive, "w").write(
    f"# h\n\n> worktree: amsterdam · cwd: {checkout} · times in UTC\n\n## 09:00:00\n\nhi\n")
os.utime(interactive, (1.9e9, 1.9e9))   # newest among interactive logs (yr 2030)
# a NEWER autonomous worker log whose cwd must be ignored (nightshift worktree)
auton = os.path.join(ld, "proj__a-w1.sess2.md")
open(auton, "w").write(
    "# h\n\n> worktree: proj__a-w1 · cwd: /tmp/nightshift/proj__a-w1 · times in UTC\n\n## 10:00:00\n\n/continue\n")
os.utime(auton, (2e9, 2e9))   # force it newest overall
check("project_repo picks interactive checkout, skips nightshift cwd",
      q.project_repo(DATA, "proj__a") == checkout)
check("project_repo None when no log/checkout", q.project_repo(DATA, "proj__z") is None)
check("start_nightshift rejects bad ids", qu.start_nightshift("proj__a", ["nope"], 8799).get("error"))
check("start_nightshift errors when no repo", qu.start_nightshift("proj__z", ["0081"], 8799).get("error"))
_orig = subprocess.Popen; _spawned = {}
def _fake_popen(cmd, **kw): _spawned["cmd"], _spawned["env"] = cmd, kw.get("env", {}); return None
_orig_nr = q.nightshift_running; q.nightshift_running = lambda repo: False   # not already running (and avoid real pgrep / the faked Popen)
_orig_ec = q.ensure_nightshift_clone; q.ensure_nightshift_clone = lambda c: (c, "stub")   # skip real git clone; tested separately below
subprocess.Popen = _fake_popen
res = qu.start_nightshift("proj__a", ["0081-foo", "0076-bar"], 8123)
subprocess.Popen = _orig; q.nightshift_running = _orig_nr
check("start_nightshift launches on resolved repo", res.get("ok") and res["repo"] == checkout)
check("start_nightshift passes --only with the ids", _spawned["cmd"][1:] == ["start", checkout, "--only", "0081-foo,0076-bar"])
check("start_nightshift sets NO_OPEN + queue port", _spawned["env"].get("NIGHTSHIFT_NO_OPEN")=="1" and _spawned["env"].get("DEVBRAIN_QUEUE_PORT")=="8123")
# double-launch guard: a fleet already running on the repo refuses a second start (no Popen)
_spawned.clear()
_orig_run = q.nightshift_running
q.nightshift_running = lambda repo: True
subprocess.Popen = _fake_popen
res_dup = qu.start_nightshift("proj__a", ["0081-foo"], 8123)
q.nightshift_running = _orig_run; subprocess.Popen = _orig; q.ensure_nightshift_clone = _orig_ec
check("start_nightshift refuses a duplicate fleet", res_dup.get("error") and "already running" in res_dup["error"])
check("start_nightshift did NOT spawn on duplicate", "cmd" not in _spawned)
# dedicated-clone resolution: dashboard launches run in NIGHTSHIFT_HOME/<repo>, not the checkout
check("repo_name_from_url strips owner + .git", q.repo_name_from_url("https://github.com/Owner/devbrain.git") == "devbrain")
check("repo_name_from_url handles ssh form", q.repo_name_from_url("git@github.com:owner/repo.git") == "repo")
_rem = os.path.join(DATA, "rem.git"); subprocess.run(["git","init","-q","--bare",_rem], check=True)
_wrk = os.path.join(DATA, "wrk"); subprocess.run(["git","clone","-q",_rem,_wrk], check=True)
open(os.path.join(_wrk,"f"),"w").write("x"); subprocess.run(["git","-C",_wrk,"add","."], check=True)
subprocess.run(["git","-C",_wrk,"-c","user.email=a@b.c","-c","user.name=t","commit","-qm","i"], check=True)
subprocess.run(["git","-C",_wrk,"push","-q","origin","HEAD:main"], check=True)
q.NIGHTSHIFT_HOME = os.path.join(DATA, "nshome")
check("clone_path maps to NIGHTSHIFT_HOME/<repo>", q.nightshift_clone_path(_wrk) == os.path.join(q.NIGHTSHIFT_HOME, "rem"))
_cr, _cn = q.ensure_nightshift_clone(_wrk)
check("ensure clones a fresh dedicated checkout", _cr == os.path.join(q.NIGHTSHIFT_HOME,"rem") and os.path.exists(os.path.join(_cr,".git")))
_cr2, _cn2 = q.ensure_nightshift_clone(_wrk)
check("ensure reuses the existing clone", _cr2 == _cr and "reused" in _cn2)
_nr = os.path.join(DATA, "norem"); subprocess.run(["git","init","-q",_nr], check=True)
_r3, _n3 = q.ensure_nightshift_clone(_nr)
check("ensure falls back in-place when no remote", _r3 == _nr and "no git remote" in _n3)

print(f"== {p} passed, {f} failed ==")
sys.exit(1 if f else 0)
PY
