#!/usr/bin/env python3
"""devbrain queue dashboard — browser dogfood. Drives the REAL dashboard headless,
asserts every core flow, and screenshots each (doubles as the UI smoke test). PNGs go to
.context/ (gitignored) — evidence you attach to a PR, never committed. Needs Playwright."""
import os
import sys

HERE = os.path.realpath(os.path.dirname(os.path.abspath(__file__)))
# Drop our own dir from sys.path BEFORE other imports so scripts/queue.py can't shadow
# the stdlib `queue` module (playwright's threads import it). realpath both sides so the
# /tmp -> /private/tmp symlink can't sneak the dir back past the equality check.
sys.path[:] = [p for p in sys.path if os.path.realpath(p or ".") != HERE]
sys.modules.pop("queue", None)

import argparse, socket, subprocess, tempfile, time, urllib.request

REPO = os.path.dirname(HERE)
QUEUE = os.path.join(HERE, "queue.py")

# One fixture task per status (+ a parked hold and a genuine block, + a second project).
FIXTURE = {
    "dogfood__demo": [
        ("0001-ship-the-control-plane", "open",   90, "", "", ""),
        ("0002-wire-the-action-endpoint", "taken", 70, "", "indianapolis-w0", ""),
        ("0003-document-the-queue-verbs", "review", 60, "https://example.com/pr/3", "", ""),
        ("0004-parked-for-a-call", "held", 55, "", "", "parked: needs a product call"),
        ("0006-genuinely-blocked", "held", 65, "", "", "blocked: waiting on a human"),
        ("0005-archive-old-prototype", "done", 40, "", "", ""),
    ],
    "dogfood__other": [("0001-second-project", "open", 50, "", "", "")],
}

def task_md(tid, status, prio, pr, who, reason):
    return (f"---\nid: {tid}\nstatus: {status}\npriority: {prio}\ncreated: 2026-06-21T00:00:00Z\n"
            f"claimed_by: {who}\nclaimed_at: \npr: {pr}\nreason: {reason}\napproved: \n---\n\n"
            f"# {tid[5:].replace('-', ' ').title()}\n\nSeeded fixture task.\n")

def seed(data):
    for project, tasks in FIXTURE.items():
        td = os.path.join(data, "projects", project, "todo")
        os.makedirs(td, exist_ok=True)
        for t in tasks:
            open(os.path.join(td, t[0] + ".md"), "w", encoding="utf-8").write(task_md(*t))

def free_port():
    s = socket.socket(); s.bind(("127.0.0.1", 0)); p = s.getsockname()[1]; s.close(); return p

def wait_up(port, timeout=15):
    end = time.time() + timeout
    while time.time() < end:
        try: urllib.request.urlopen(f"http://127.0.0.1:{port}/api/projects", timeout=1).read(); return True
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
            page = pw.chromium.launch().new_page(viewport={"width": 1180, "height": 820}, device_scale_factor=2)
            def shot(label):
                n[0] += 1; page.screenshot(path=os.path.join(args.out, f"{n[0]:02d}-{label}.png"), full_page=True)
            def settle():
                # wait out any open modal closing + the async re-render before the next step
                page.wait_for_function("() => { const m=document.querySelector('#mask'); return !m || !m.classList.contains('on'); }")
                page.wait_for_timeout(200)
            def row(tid): return page.locator("#rows tr").filter(has_text=tid)
            def submit(label): page.locator("#modal").get_by_role("button", name=label).click()  # scope to modal (row buttons share labels)

            page.goto(f"http://127.0.0.1:{port}/")
            page.wait_for_function("() => document.querySelectorAll('.chip').length === 6")
            page.wait_for_selector("#needs", state="visible"); settle(); shot("overview")
            check("six category chips render", page.locator(".chip").count() == 6)
            check("needs-you shows the genuine block", page.locator("#needs").get_by_text("0006-genuinely-blocked").count() > 0)
            check("parked hold excluded from needs-you", page.locator("#needs").get_by_text("0004-parked").count() == 0)

            page.locator(".chip", has_text="done").click(); settle(); shot("filter-done-on")
            check("toggling 'done' reveals the done task", row("0005-archive-old-prototype").count() == 1)
            page.locator(".chip", has_text="done").click()

            page.locator("#new").click(); page.fill("#m1", "fresh demo task"); page.fill("#m2", "80")
            submit("Create"); settle(); shot("create")
            check("created task appears", page.get_by_text("Fresh Demo Task").count() > 0)

            r = row("0001-ship-the-control-plane")
            r.get_by_role("button", name="Edit").click(); page.fill("#m1", "Renamed Task")
            submit("Save"); settle(); shot("edit")
            check("edited title is reflected", page.get_by_text("Renamed Task").count() > 0)

            r = row("0001-ship-the-control-plane")
            r.get_by_role("button", name="Prio").click(); page.fill("#m1", "95")
            submit("Save"); settle(); shot("prio")
            check("reprioritized to 95", "95" in row("0001-ship-the-control-plane").inner_text())

            r = row("0002-wire-the-action-endpoint")
            r.get_by_role("button", name="Hold").click(); page.fill("#m1", "blocked: demo")
            submit("Hold"); settle(); shot("hold")
            check("held task moves to needs-you", page.locator("#needs").get_by_text("0002-wire-the-action-endpoint").count() > 0)

            row("0002-wire-the-action-endpoint").get_by_role("button", name="Open").click()   # the row's "→ Open" = release
            settle(); shot("release")
            check("released task back to open", "OPEN" in row("0002-wire-the-action-endpoint").inner_text().upper())

            row("0003-document-the-queue-verbs").get_by_role("button", name="Done").click()
            page.locator(".chip", has_text="done").click(); settle(); shot("done")            # reveal done tasks
            check("marked done", "DONE" in row("0003-document-the-queue-verbs").inner_text().upper())

            page.select_option("#project", "dogfood__other"); settle(); shot("project-switch")
            check("project switch shows the other project's task", row("0001-second-project").count() == 1)

    finally:
        proc.terminate()
        if not args.keep:
            import shutil; shutil.rmtree(data, ignore_errors=True)

    print(f"dogfood: {n[0]} screenshots -> {args.out}")
    print(f"dogfood: {P['pass']} ok, {P['fail']} failed")
    sys.exit(1 if P["fail"] else 0)


if __name__ == "__main__":
    main()
