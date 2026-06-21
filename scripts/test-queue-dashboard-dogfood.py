#!/usr/bin/env python3
"""devbrain — queue control plane: browser-driven dogfood pass.

Drives the REAL queue dashboard (scripts/queue.py + queue-dashboard.html) in a
headless Chromium and screenshots every flow the control plane offers: project
switch, status filter, create, edit, reprioritize, add-context, the hold /
release / approve / done verbs, and the "needs you" held section. Every flow gets
a before/after PNG written to docs/queue-dashboard/screenshots/ as PR evidence,
and each step asserts the visible outcome so this doubles as a UI smoke test.

It is side-effect-free: the server runs against a throwaway DEVBRAIN_DATA seeded
with a fixture project, never the real ~/devbrain-data queue.

Requires Playwright with a Chromium build:  python3 -m pip install playwright
                                            python3 -m playwright install chromium
Run:  python3 scripts/test-queue-dashboard-dogfood.py [--out DIR] [--keep]
"""
import argparse
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
QUEUE = os.path.join(HERE, "queue.py")

# Running from scripts/ puts it on sys.path, where scripts/queue.py would shadow
# the stdlib `queue` module (asyncio imports it lazily). Drop our own dir.
sys.path[:] = [p for p in sys.path if os.path.abspath(p or ".") != HERE]

# One fixture task per status so every chip, the needs-you panel, and every verb
# has something real to act on. id = NNNN-slug, matching todo.sh's own format.
FIXTURE = {
    "dogfood__demo": [
        ("0001-ship-the-control-plane", "open",   90, "", ""),
        ("0002-wire-the-action-endpoint", "taken", 70, "", "indianapolis-w0"),
        ("0003-document-the-queue-verbs", "review", 60, "https://example.com/pr/3", ""),
        ("0004-needs-a-human-decision", "held",   55,
         "parked: needs a product call on multi-project default sort", ""),
        ("0005-archive-the-old-prototype", "done", 40, "", ""),
    ],
    "dogfood__other": [
        ("0001-second-project-smoke", "open", 50, "", ""),
    ],
}


def task_md(tid, status, prio, pr, claimed_by):
    body = "Seeded fixture task for the dashboard dogfood pass.\n\nAcceptance: the row renders and every verb round-trips."
    return (
        "---\n"
        f"id: {tid}\n"
        f"status: {status}\n"
        f"priority: {prio}\n"
        "created: 2026-06-21T00:00:00Z\n"
        f"claimed_by: {claimed_by}\n"
        "claimed_at: \n"
        f"pr: {pr}\n"
        f"reason: {'parked: needs a product call on multi-project default sort' if status == 'held' else ''}\n"
        "approved: \n"
        "---\n\n"
        f"# {tid[5:].replace('-', ' ').title()}\n\n"
        f"{body}\n"
    )


def seed(data):
    for project, tasks in FIXTURE.items():
        td = os.path.join(data, "projects", project, "todo")
        os.makedirs(td, exist_ok=True)
        for tid, status, prio, pr, who in tasks:
            with open(os.path.join(td, tid + ".md"), "w", encoding="utf-8") as f:
                f.write(task_md(tid, status, prio, pr, who))


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    p = s.getsockname()[1]
    s.close()
    return p


def wait_up(port, timeout=15):
    url = f"http://127.0.0.1:{port}/api/projects"
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            urllib.request.urlopen(url, timeout=1).read()
            return True
        except Exception:
            time.sleep(0.2)
    return False


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=os.path.join(REPO, "docs", "queue-dashboard", "screenshots"))
    ap.add_argument("--keep", action="store_true", help="keep the throwaway data dir")
    args = ap.parse_args()

    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        sys.exit("dogfood: playwright not installed — `python3 -m pip install playwright "
                 "&& python3 -m playwright install chromium`")

    os.makedirs(args.out, exist_ok=True)
    data = tempfile.mkdtemp(prefix="dogfood-data-")
    seed(data)
    port = free_port()
    # Pin the CLI to THIS checkout's todo.sh so the dogfood tests the code under
    # review, not whatever (possibly stale) hook is installed globally.
    env = dict(os.environ, DEVBRAIN_TODO=os.path.join(HERE, "todo.sh"))
    proc = subprocess.Popen(
        [sys.executable, QUEUE, "--data", data, "--no-open", "--port", str(port)],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env,
    )

    shots, checks = [], {"pass": 0, "fail": 0}

    def check(name, cond):
        ok = bool(cond)
        checks["pass" if ok else "fail"] += 1
        print(f"  {'ok  ' if ok else 'FAIL'} — {name}")
        return ok

    try:
        if not wait_up(port):
            sys.exit("dogfood: queue server did not come up")
        base = f"http://127.0.0.1:{port}/"

        with sync_playwright() as pw:
            browser = pw.chromium.launch()
            page = browser.new_page(viewport={"width": 1280, "height": 900},
                                    device_scale_factor=2)
            n = [0]

            def shot(label):
                n[0] += 1
                fn = os.path.join(args.out, f"{n[0]:02d}-{label}.png")
                page.screenshot(path=fn, full_page=True)
                shots.append(os.path.relpath(fn, REPO))

            def settle():
                page.wait_for_timeout(350)

            page.goto(base)
            page.wait_for_selector("#rows tr")
            settle()
            shot("overview")
            check("all five statuses render a chip", page.locator(".chip").count() == 5)
            check("needs-you panel shows the held task",
                  page.locator("#needs").is_visible())

            # --- flow: status filter (toggle down to open-only) ---
            for s in ("taken", "review", "held"):
                page.locator(f".chip:has-text('{s}')").first.click()
            settle()
            shot("filter-open-only")
            check("filtering hides non-open rows",
                  page.locator("#rows .st.taken").count() == 0)
            # restore filters
            for s in ("taken", "review", "held"):
                page.locator(f".chip:has-text('{s}')").first.click()
            settle()

            # --- flow: project switch ---
            page.select_option("#project", label=None, value="dogfood__other")
            page.wait_for_function(
                "() => document.querySelectorAll('#rows tr').length === 1")
            settle()
            shot("project-switch")
            check("other project shows its single task",
                  page.locator("#rows tr").count() == 1)
            page.select_option("#project", value="dogfood__demo")
            page.wait_for_selector("#rows tr")
            settle()

            # --- flow: create ---
            page.locator("#new").click()
            page.fill("#n_title", "Dogfood-created task")
            page.fill("#n_prio", "80")
            page.fill("#n_body", "Created by the browser dogfood pass.")
            settle()
            shot("create-modal")
            page.locator(".modal button.primary").click()
            page.wait_for_function(
                "() => [...document.querySelectorAll('.title')]"
                ".some(e => e.textContent.includes('Dogfood-created task'))")
            settle()
            shot("create-done")
            check("created task appears in the table",
                  page.get_by_text("Dogfood-created task").count() > 0)

            # --- flow: edit ---
            row = page.locator("tr", has=page.get_by_text("0001-ship-the-control-plane"))
            row.locator("button", has_text="Edit").first.click()
            page.fill("#m_title", "Ship the control plane (edited)")
            settle()
            shot("edit-modal")
            page.locator(".modal button.primary").click()
            page.wait_for_function(
                "() => [...document.querySelectorAll('.title')]"
                ".some(e => e.textContent.includes('(edited)'))")
            settle()
            shot("edit-done")
            check("edited title is reflected",
                  page.get_by_text("Ship the control plane (edited)").count() > 0)

            # --- flow: reprioritize ---
            row = page.locator("tr", has=page.get_by_text("0003-document-the-queue-verbs"))
            row.locator("button", has_text="Prio").first.click()
            page.fill("#m_prio", "95")
            settle()
            shot("prio-modal")
            page.locator(".modal button.primary").click()
            settle()
            shot("prio-done")
            check("reprioritized row jumps to priority 95",
                  page.locator("tr", has=page.get_by_text("0003-document-the-queue-verbs"))
                  .locator(".pri").inner_text().strip() == "95")

            # --- flow: add context ---
            row = page.locator("tr", has=page.get_by_text("0002-wire-the-action-endpoint"))
            row.locator("button", has_text="Context").first.click()
            page.fill("#m_ctx", "The /action endpoint must stay localhost-only and validate ids.")
            settle()
            shot("context-modal")
            page.locator(".modal button.primary").click()
            settle()
            shot("context-done")
            check("context modal closed cleanly",
                  not page.locator("#mask").get_attribute("class").endswith("on"))

            # --- flow: hold ---
            row = page.locator("tr", has=page.get_by_text("0001-ship-the-control-plane"))
            row.locator("button", has_text="Hold").first.click()
            page.fill("#m_reason", "blocked: dogfood hold demo")
            settle()
            shot("hold-modal")
            page.locator(".modal button.primary").click()
            page.wait_for_function(
                "() => [...document.querySelectorAll('#needs .id')]"
                ".some(e => e.textContent.includes('0001-ship-the-control-plane'))")
            settle()
            shot("hold-done")
            check("held task moves into the needs-you panel",
                  page.locator("#needs").get_by_text("0001-ship-the-control-plane").count() > 0)

            # --- flow: release (held -> open) from the needs-you panel ---
            page.locator("#needs button", has_text="Release").first.click()
            settle()
            shot("release-done")

            # --- flow: approve (needs-you panel) on the still-held fixture task ---
            held_row = page.locator("#needs").get_by_text("0004-needs-a-human-decision")
            check("a held task remains to approve", held_row.count() > 0)
            page.locator("#needs button", has_text="Approve").first.click()
            settle()
            shot("approve-done")

            # --- flow: done ---
            row = page.locator("tr", has=page.get_by_text("0002-wire-the-action-endpoint"))
            row.locator("button", has_text="Done").first.click()
            settle()
            # surface the done rows so the status shows
            if not page.locator(".chip.on:has-text('done')").count():
                page.locator(".chip:has-text('done')").first.click()
            settle()
            shot("done-done")
            check("done task carries the done status pill",
                  page.locator("tr", has=page.get_by_text("0002-wire-the-action-endpoint"))
                  .locator(".st.done").count() > 0)

            browser.close()
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except Exception:
            proc.kill()
        if not args.keep:
            shutil.rmtree(data, ignore_errors=True)

    print(f"\ndogfood: {len(shots)} screenshots -> {os.path.relpath(args.out, REPO)}")
    print(f"dogfood: {checks['pass']} ok, {checks['fail']} failed")
    sys.exit(1 if checks["fail"] else 0)


if __name__ == "__main__":
    main()
