#!/usr/bin/env python3
"""devbrain queue kanban — browser dogfood. Drives the REAL dashboard headless, asserts
the core flows, and screenshots each (doubles as the UI smoke test). PNGs go to .context/
(gitignored) — evidence you attach to a PR, never committed. Needs Playwright."""
import os
import sys

HERE = os.path.realpath(os.path.dirname(os.path.abspath(__file__)))
# Drop our own dir from sys.path BEFORE other imports so scripts/queue.py can't shadow the
# stdlib `queue` module (playwright's threads import it). realpath both sides for /tmp symlink.
sys.path[:] = [p for p in sys.path if os.path.realpath(p or ".") != HERE]
sys.modules.pop("queue", None)

import argparse, json, socket, subprocess, tempfile, time, urllib.request

REPO = os.path.dirname(HERE)
QUEUE = os.path.join(HERE, "queue.py")

FIXTURE = {                                  # one task per status + a second project
    "dogfood__demo": [
        ("0001-ship-the-control-plane", "open",   90, ""),
        ("0002-wire-the-action-endpoint", "taken", 70, "indianapolis-w0"),
        ("0003-document-the-queue-verbs", "review", 60, ""),
        ("0006-genuinely-blocked", "held", 65, ""),
        ("0005-archive-old-prototype", "done", 40, ""),
    ],
    "dogfood__other": [("0001-second-project", "open", 50, "")],
    "dogfood__idle":  [("0001-archived-only", "done", 30, "")],   # no open tasks -> grayed in picker
}

def task_md(tid, status, prio, who):
    return (f"---\nid: {tid}\nstatus: {status}\npriority: {prio}\ncreated: 2026-06-21T00:00:00Z\n"
            f"claimed_by: {who}\n---\n\n# {tid[5:].replace('-', ' ').title()}\n\nSeeded fixture task.\n")

def seed(data):
    for project, tasks in FIXTURE.items():
        td = os.path.join(data, "projects", project, "todo")
        os.makedirs(td, exist_ok=True)
        for t in tasks:
            open(os.path.join(td, t[0] + ".md"), "w", encoding="utf-8").write(task_md(*t))

def seed_nightshift(data):
    # a live fleet so the monitor view (token chart, agent terminals, logs, merges) has data
    repo = os.path.join(data, "ns-repo"); os.makedirs(os.path.join(repo, ".nightshift"), exist_ok=True)
    json.dump({"port": 0, "repo": repo},
              open(os.path.join(data, "projects", "dogfood__demo", "nightshift-run.json"), "w"))
    json.dump({
        "updated": "2026-06-23T00:00:00Z", "project": "demo", "running": True,
        "queue": {"open": 1, "done": 2, "review": 0}, "tokens_min": {"in": 120, "out": 3400},
        "tokens_total": {"in": 50000, "out": 1801800}, "cost_total": 45.3,
        "history": [{"t": "00:00", "out": 0, "in": 0}, {"t": "00:01", "out": 3400, "in": 120}],
        "workers": [
            {"i": 0, "state": "working", "task": "0002-wire", "tin": 50, "tout": 1800,
             "responses": [{"t": "00:00:10", "sid": "a", "text": "Starting the task."},
                           {"t": "00:01:02", "sid": "b", "text": "Tests pass."}]},
            {"i": 1, "state": "idle", "task": "—", "tin": 0, "tout": 0, "responses": []}],
        "nightshift": ["abc1234 nightshift: merge todo/0002-wire into nightshift"],
        "log": ["orch: starting 2 workers", "orch: merged 0002 into nightshift"],
        "parked": [], "parked_count": 0,
    }, open(os.path.join(repo, ".nightshift", "status.json"), "w"))

def seed_prompts(data):
    # Two interactive sessions in DIFFERENT repos, overlapping in time on one day — so the
    # Profile's cross-repo concurrency panel has parallel bands (peak 2) to render.
    day = "2026-06-21"
    for proj, wt, sid, times in [("dogfood__demo", "wt-a", "sessa", ["10:00:00", "10:05:00"]),
                                 ("dogfood__web",  "wt-b", "sessb", ["10:03:00", "10:08:00"])]:
        ld = os.path.join(data, "projects", proj, "log", day); os.makedirs(ld, exist_ok=True)
        body = (f"# {proj} — {day} — session {sid}\n\n> devbrain Stage A raw prompt log.\n"
                f"> worktree: {wt} · cwd: /tmp/{wt} · times in UTC\n\n")
        for ts in times:
            body += f"## {ts}\n\nadd the parallelism panel to the dashboard\n\n"
        open(os.path.join(ld, f"{wt}.{sid}.md"), "w", encoding="utf-8").write(body)

def free_port():
    s = socket.socket(); s.bind(("127.0.0.1", 0)); p = s.getsockname()[1]; s.close(); return p

def wait_up(port, timeout=15):
    end = time.time() + timeout
    while time.time() < end:
        try: urllib.request.urlopen(f"http://127.0.0.1:{port}/api/todos", timeout=1).read(); return True
        except Exception: time.sleep(0.2)
    return False


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=os.path.join(REPO, ".context", "queue-dashboard-screenshots"))
    ap.add_argument("--keep", action="store_true")
    args = ap.parse_args()
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        print("skip: playwright not installed (python3 -m pip install playwright "
              "&& python3 -m playwright install chromium)")
        sys.exit(0)

    os.makedirs(args.out, exist_ok=True)
    data = tempfile.mkdtemp(prefix="dogfood-data-")
    seed(data)
    seed_nightshift(data)
    seed_prompts(data)
    port = free_port()
    proc = subprocess.Popen([sys.executable, QUEUE, "--data", data, "--no-open", "--port", str(port)],
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    P = {"pass": 0, "fail": 0}
    n = [0]
    def check(name, cond):
        ok = bool(cond); P["pass" if ok else "fail"] += 1
        print(f"  {'ok  ' if ok else 'FAIL'} — {name}")

    try:
        if not wait_up(port): sys.exit("dogfood: queue server did not come up")
        with sync_playwright() as pw:
            page = pw.chromium.launch().new_page(viewport={"width": 1320, "height": 860}, device_scale_factor=2)
            page.on("dialog", lambda d: d.accept())   # auto-accept the delete confirm
            def shot(label):
                n[0] += 1; page.screenshot(path=os.path.join(args.out, f"{n[0]:02d}-{label}.png"), full_page=True)
            def col(status): return page.locator(f'.col[data-status="{status}"]')
            def card(title): return page.locator(".card").filter(has_text=title)

            page.goto(f"http://127.0.0.1:{port}/")
            page.wait_for_selector("#viewseg", state="visible", timeout=6000)
            check("Profile is the default view", page.locator('#viewseg button[data-view="profile"].active').count() == 1)
            # Cross-repo "Agents In Parallel" panel: seeded overlapping sessions in two repos
            # render stacked bands (≥2) and a peak readout.
            page.wait_for_timeout(500)
            check("concurrency panel renders parallel bars",
                  page.locator('#pf-s-conc rect.cbar').count() >= 2
                  and "peak" in (page.locator('#pf-c-conc').inner_text() or ""))
            # Board tests need the Board view (Profile is now the default).
            page.locator('#viewseg button[data-view="board"]').click()
            page.wait_for_selector(".card")
            # Project picker: defaults to the most-active project (not "all"), pins
            # "all projects" to the bottom, and grays projects with no open tasks.
            check("default project filter is the most-active project, not all",
                  page.eval_on_selector("#filterProject", "el => el.value") == "dogfood__demo")
            check("'all projects' option is pinned to the bottom",
                  page.eval_on_selector("#filterProject", "el => el.options[el.options.length-1].value") == "")
            check("project with no open tasks is grayed",
                  page.eval_on_selector("#filterProject",
                      "el => [...el.options].find(o => o.value==='dogfood__idle').classList.contains('noopen')"))
            page.select_option("#filterProject", "")   # view all projects for the cross-project overview
            page.wait_for_timeout(150)
            page.wait_for_timeout(300); shot("overview")
            check("five status columns render", page.locator(".col").count() == 5)
            check("cards render across columns", page.locator(".card").count() >= 6)
            check("open task in the Open column", col("open").get_by_text("Ship The Control Plane").count() > 0)

            # edit: click a card -> modal -> rename + move status to review -> save
            card("Ship The Control Plane").click()
            page.wait_for_selector("#modal.show")
            page.fill("#fTitle", "Renamed Demo"); page.select_option("#fStatus", "review")
            page.click("#saveBtn"); page.wait_for_timeout(400); shot("edit-and-move")
            check("edited title shows", page.get_by_text("Renamed Demo").count() > 0)
            check("status change moved the card to Review", col("review").get_by_text("Renamed Demo").count() > 0)

            # create
            page.click("#newBtn"); page.wait_for_selector("#modal.show")
            page.fill("#fTitle", "Fresh Kanban Task"); page.fill("#fPriority", "80")
            page.click("#saveBtn"); page.wait_for_timeout(400); shot("create")
            check("created card appears in Open", col("open").get_by_text("Fresh Kanban Task").count() > 0)

            # search narrows the board
            page.fill("#search", "fresh kanban"); page.wait_for_timeout(200); shot("search")
            check("search narrows to the match", page.locator(".card").count() == 1)
            page.fill("#search", "")

            # project filter
            page.select_option("#filterProject", "dogfood__other"); page.wait_for_timeout(200); shot("project-filter")
            check("project filter shows only that project", page.locator(".card").count() == 1
                  and card("Second Project").count() == 1)
            page.select_option("#filterProject", "")

            # delete (confirm auto-accepted)
            before = page.locator(".card").count()
            card("Genuinely Blocked").click(); page.wait_for_selector("#modal.show")
            page.click("#deleteBtn"); page.wait_for_timeout(400); shot("delete")
            check("delete removes the card", page.locator(".card").count() == before - 1)

            # select tasks -> drag/click the 🌙 -> fixed-set nightshift launch dialog
            check("moon launcher is present", page.locator("#moon").count() == 1)
            check("moon starts unselected (no badge)", page.locator("#moon .badge").count() == 0)
            card("Fresh Kanban Task").locator(".seldot").click(); page.wait_for_timeout(120)
            check("select dot picks a card (no modifier)", page.locator(".card.sel").count() == 1)
            # select mode: with one picked, a plain card click adds another (no modifier, no editor)
            card("Wire The Action Endpoint").click(); page.wait_for_timeout(100)
            check("plain click adds to selection in select mode", page.locator(".card.sel").count() == 2)
            check("plain click in select mode does NOT open the editor", page.locator("#modal.show").count() == 0)
            # clicking empty board space exits select mode (deselects all)
            page.eval_on_selector("#board", "b => b.click()"); page.wait_for_timeout(100)
            check("clicking empty board clears the selection", page.locator(".card.sel").count() == 0)
            # nothing selected again -> a plain card click opens the editor as usual
            card("Fresh Kanban Task").click(); page.wait_for_selector("#modal.show", timeout=2000)
            check("plain click opens editor when nothing is selected", page.locator("#modal.show").count() == 1)
            page.click("#cancelBtn"); page.wait_for_timeout(100)
            card("Fresh Kanban Task").locator(".seldot").click(); page.wait_for_timeout(120)   # re-select for launch flow
            check("moon arms + badges the count", page.locator("#moon.armed").count() == 1
                  and (page.locator("#moon .badge").inner_text() or "") == "1")
            page.click("#moon"); page.wait_for_selector("#launchModal.show"); shot("moon-launch")
            check("launch dialog lists the selected task", page.locator("#lxBody .lx-row").count() == 1)
            check("launch dialog shows the Launch button", page.locator("#lxGoBtn").is_visible())
            page.click("#lxCancelBtn"); page.wait_for_timeout(120)
            check("cancel closes the launch dialog", page.locator("#launchModal.show").count() == 0)

            # big corner catch-zone: inert until a drag, then receives the drop too (not just the 60px moon)
            check("drop zone inert when not dragging",
                  page.eval_on_selector("#moonzone", "z => getComputedStyle(z).pointerEvents") == "none")
            page.eval_on_selector("#moonzone", "z => z.dispatchEvent(new Event('drop', {bubbles:true, cancelable:true}))")
            page.wait_for_selector("#launchModal.show", timeout=2000)
            check("dropping on the corner zone opens the launch dialog", page.locator("#launchModal.show").count() == 1)
            page.click("#lxCancelBtn"); page.wait_for_timeout(120)

            # nightshift monitor: the segmented switch reveals the fleet view
            page.wait_for_selector("#viewseg", state="visible", timeout=6000)
            check("nightshift switch shows both emoji segments",
                  page.locator('#viewseg button[data-view="board"]').count() == 1
                  and page.locator('#viewseg button[data-view="monitor"]').count() == 1)
            page.locator('#viewseg button[data-view="monitor"]').click(); page.wait_for_timeout(300); shot("monitor")
            check("monitor renders agent terminals", page.locator(".ns-term").count() >= 1)
            check("monitor renders 8 stat boxes + token chart",
                  page.locator(".ns-stat").count() == 8 and page.locator(".ns-chart").count() == 1)
            mon_txt = page.locator("#monitor").inner_text().lower()   # labels are CSS-uppercased
            check("monitor shows cumulative tokens + est. cost",
                  "σ tokens" in mon_txt and "est. cost" in mon_txt and "$45.30" in mon_txt)
            check("monitor renders the agent response feed", page.locator(".ns-msg").count() >= 1)
            check("monitor renders orchestrator log + merge feed",
                  page.locator(".ns-log").count() == 2
                  and page.get_by_text("merged → nightshift", exact=False).count() > 0)
            page.locator('#viewseg button[data-view="board"]').click(); page.wait_for_timeout(200)
            check("board returns from monitor", page.locator(".col").count() == 5)

            # a just-launched fleet shows a booting "starting…" spinner until it registers
            # (set the state directly — exercising the render path without spawning a real fleet)
            page.evaluate("() => { NS={runs:[]}; NS_STARTING={repo:'/demo/repo', count:2}; setView('monitor'); renderMonitor(); }")
            page.wait_for_timeout(80)
            check("launch shows a booting 'starting…' state",
                  page.locator("#monitor .ns-boot").count() == 1
                  and "starting nightshift" in (page.locator("#monitor .ns-boot").inner_text() or "").lower())

    finally:
        proc.terminate()
        if not args.keep:
            import shutil; shutil.rmtree(data, ignore_errors=True)

    print(f"dogfood: {n[0]} screenshots -> {args.out}")
    print(f"dogfood: {P['pass']} ok, {P['fail']} failed")
    sys.exit(1 if P["fail"] else 0)


if __name__ == "__main__":
    main()
