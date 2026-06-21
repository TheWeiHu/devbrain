#!/usr/bin/env python3
"""devbrain — queue control plane: browser-driven dogfood pass.

Drives the REAL queue dashboard (scripts/queue.py + queue-dashboard.html) in a
headless Chromium and screenshots every flow the control plane offers: project
switch, status filter, create, edit, reprioritize, add-context, the hold /
release / approve / done verbs, and the "needs you" held section. Every flow gets
a before/after PNG, and each step asserts the visible outcome so this doubles as a
UI smoke test.

PNGs are written to .context/queue-dashboard-screenshots/ (gitignored scratch) so
they are evidence you ATTACH to a PR discussion — never committed into the diff.
Override the location with --out if you want them elsewhere.

It is side-effect-free: the server runs against a throwaway DEVBRAIN_DATA seeded
with a fixture project, never the real ~/devbrain-data queue.

Requires Playwright with a Chromium build:  python3 -m pip install playwright
                                            python3 -m playwright install chromium
Run:  python3 scripts/test-queue-dashboard-dogfood.py [--out DIR] [--keep]
"""
import argparse
import os
import re
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

# One fixture task per category so every chip, the needs-you panel, and every verb
# has something real to act on. id = NNNN-slug, matching todo.sh's own format.
# The parked-split (task 0040) means a held task whose reason starts "parked" is a
# focus-park (its own chip, excluded from "needs you") while a plain "blocked" hold
# is a genuine block that shows in the needs-you panel — so we seed BOTH: 0004 parked
# (drives the parked chip) and 0006 a genuine block (drives the needs-you panel +
# the release/approve flow). reason is per-task, not synthesized from status.
FIXTURE = {
    "dogfood__demo": [
        # (id, status, priority, pr, claimed_by, reason)
        ("0001-ship-the-control-plane", "open",   90, "", "", ""),
        ("0002-wire-the-action-endpoint", "taken", 70, "", "indianapolis-w0", ""),
        ("0003-document-the-queue-verbs", "review", 60, "https://example.com/pr/3", "", ""),
        ("0004-parked-for-a-product-call", "held", 55, "", "",
         "parked: needs a product call on multi-project default sort"),
        ("0006-genuinely-blocked", "held", 65, "", "",
         "blocked: waiting on a human decision"),
        ("0005-archive-the-old-prototype", "done", 40, "", "", ""),
    ],
    "dogfood__other": [
        ("0001-second-project-smoke", "open", 50, "", "", ""),
    ],
}


def task_md(tid, status, prio, pr, claimed_by, reason):
    body = "Seeded fixture task for the dashboard dogfood pass.\n\nAcceptance: the row renders and every verb round-trips."
    # Distinct created stamps ordered by the id's numeric prefix (0001 oldest …) so the
    # sort flow (task 0062) can assert newest/oldest ordering, not just that it doesn't error.
    n = int(tid[:4])
    created = f"2026-06-21T00:{n:02d}:00Z"
    return (
        "---\n"
        f"id: {tid}\n"
        f"status: {status}\n"
        f"priority: {prio}\n"
        f"created: {created}\n"
        f"claimed_by: {claimed_by}\n"
        "claimed_at: \n"
        f"pr: {pr}\n"
        f"reason: {reason}\n"
        "approved: \n"
        "---\n\n"
        f"# {tid[5:].replace('-', ' ').title()}\n\n"
        f"{body}\n"
    )


def seed(data):
    for project, tasks in FIXTURE.items():
        td = os.path.join(data, "projects", project, "todo")
        os.makedirs(td, exist_ok=True)
        for tid, status, prio, pr, who, reason in tasks:
            with open(os.path.join(td, tid + ".md"), "w", encoding="utf-8") as f:
                f.write(task_md(tid, status, prio, pr, who, reason))


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
    # Default to gitignored scratch so a dogfood run never dirties the tracked tree.
    ap.add_argument("--out", default=os.path.join(REPO, ".context", "queue-dashboard-screenshots"))
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
            # Wait out the initial async loadProjects→loadTasks→render before asserting,
            # so a slow first paint can't race the checks below.
            page.wait_for_function("() => document.querySelectorAll('.chip').length === 6")
            page.wait_for_selector("#needs", state="visible")
            settle()
            shot("overview")
            check("all six categories render a chip", page.locator(".chip").count() == 6)
            check("needs-you panel shows the genuine block",
                  page.locator("#needs").is_visible()
                  and page.locator("#needs").get_by_text("0006-genuinely-blocked").count() > 0)

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

            # --- flow: text search (id+title+body), debounced + AND with chips ---
            page.fill("#search", "wire")
            page.wait_for_function(
                "() => document.querySelectorAll('#rows tr').length === 1")
            settle()
            shot("search-filter")
            check("search narrows to the id/title match",
                  page.locator("#rows tr").count() == 1
                  and page.get_by_text("0002-wire-the-action-endpoint").count() > 0)
            # AND with chips: toggling off the match's status hides it entirely
            page.locator(".chip:has-text('taken')").first.click()
            page.wait_for_function(
                "() => document.querySelectorAll('#rows tr').length === 0")
            check("search AND status chips (toggling the match's status hides it)",
                  page.locator("#rows tr").count() == 0 and page.locator("#empty").is_visible())
            page.locator(".chip:has-text('taken')").first.click()   # restore chip
            page.wait_for_function(
                "() => document.querySelectorAll('#rows tr').length === 1")
            # clearing the box restores the chip-filtered view
            page.fill("#search", "")
            page.wait_for_function(
                "() => document.querySelectorAll('#rows tr').length === 4")
            check("clearing the search restores the chip view",
                  page.locator("#rows tr").count() == 4)
            # "/" focuses the search box from anywhere. Defocus via the header h1,
            # not body.click(): clicking <body> lands on its center, which sits over
            # a row's action button — that opens the modal, and "/" is (correctly)
            # suppressed while a modal is open. The h1 is a stable, non-interactive
            # target that just blurs the input.
            page.locator("h1").click()
            page.keyboard.press("/")
            # Poll for focus rather than reading activeElement instantly: the handler's
            # #search.focus() is synchronous, but on a loaded/headless box focus can land
            # a tick after keyboard.press() resolves, so the immediate read flaked (task
            # 0067 — 27 ok / 1 failed in CI, green locally). wait_for_function bounds the
            # wait and degrades to a clean FAIL instead of throwing if focus never lands.
            try:
                page.wait_for_function(
                    "() => document.activeElement === document.querySelector('#search')",
                    timeout=2000)
                slash_focused = True
            except Exception:
                slash_focused = False
            check("'/' focuses the search box", slash_focused)
            page.keyboard.press("Escape")   # Escape in the box clears + blurs
            settle()

            # --- flow: timestamps + sort options (task 0062) ---
            # Every row carries a relative age from `created`; the sort control reorders
            # client-side and persists in ?sort= so a view is shareable.
            meta0 = page.locator("#rows .meta").first.inner_text()
            check("rows show a relative age badge",
                  "ago" in meta0 or "just now" in meta0)
            check("sort control offers the four orders",
                  page.locator("#sort option").count() == 4)
            # oldest → the lowest-numbered seeded task floats to the top of the table
            page.select_option("#sort", value="oldest")
            page.wait_for_function(
                "() => document.querySelector('#rows tr .id')"
                ".textContent.includes('0001-ship-the-control-plane')")
            check("oldest sort puts the earliest-created task first",
                  "0001-ship-the-control-plane"
                  in page.locator("#rows tr .id").first.inner_text())
            check("sort persists in the ?sort= URL", "sort=oldest" in page.url)
            shot("sort-oldest")
            # newest → the highest-numbered visible task leads instead
            page.select_option("#sort", value="newest")
            page.wait_for_function(
                "() => document.querySelector('#rows tr .id')"
                ".textContent.includes('0006-genuinely-blocked')")
            check("newest sort puts the latest-created task first",
                  "0006-genuinely-blocked"
                  in page.locator("#rows tr .id").first.inner_text())
            # a ?sort= deep link restores that order on load
            page.goto(base + "?project=dogfood__demo&sort=oldest")
            page.wait_for_selector("#rows tr")
            settle()
            check("?sort= deep link restores the chosen order",
                  page.eval_on_selector("#sort", "el => el.value") == "oldest"
                  and "0001-ship-the-control-plane"
                  in page.locator("#rows tr .id").first.inner_text())
            page.select_option("#sort", value="priority")   # restore default for later flows
            settle()

            # --- a11y: keyboard + dialog semantics (task 0061) ---
            # "n" opens the New task dialog from anywhere (defocus search via h1 first).
            page.locator("h1").click()
            page.keyboard.press("n")
            page.wait_for_selector("#mask.on")
            check("'n' opens the New task dialog",
                  page.locator("#mask").get_attribute("class").endswith("on"))
            check("dialog exposes role=dialog + aria-modal",
                  page.eval_on_selector(
                      "#modal",
                      "el => el.getAttribute('role')==='dialog' "
                      "&& el.getAttribute('aria-modal')==='true'"))
            check("dialog is labelled from its heading",
                  "New task" in (page.eval_on_selector("#modal", "el => el.getAttribute('aria-label')") or ""))
            check("opening a dialog moves focus inside it",
                  page.eval_on_selector("#modal", "el => el.contains(document.activeElement)"))
            page.keyboard.press("Escape")
            page.wait_for_function("() => !document.querySelector('#mask').className.includes('on')")
            check("Esc closes the dialog",
                  not page.locator("#mask").get_attribute("class").endswith("on"))
            # Focus returns to the triggering button when a dialog closes.
            row = page.locator("tr", has=page.get_by_text("0001-ship-the-control-plane"))
            row.locator("button", has_text="Edit").first.click()
            page.wait_for_selector("#mask.on")
            page.locator(".modal button[onclick='closeModal()']").click()   # Cancel
            page.wait_for_function("() => !document.querySelector('#mask').className.includes('on')")
            check("focus returns to the trigger button on close",
                  page.evaluate("() => document.activeElement && "
                                "document.activeElement.textContent.trim()==='Edit'"))
            # Filter chips are exposed as toggle buttons and operable via the keyboard.
            check("chips are role=button with aria-pressed state",
                  page.eval_on_selector(
                      ".chip",
                      "el => el.getAttribute('role')==='button' "
                      "&& el.hasAttribute('aria-pressed')"))
            open_chip = page.locator(".chip:has-text('open')").first
            before = open_chip.get_attribute("aria-pressed")
            open_chip.focus()
            page.keyboard.press("Enter")     # Enter toggles a focused chip
            after = page.locator(".chip:has-text('open')").first.get_attribute("aria-pressed")
            check("Enter toggles a focused chip", before != after)
            page.locator(".chip:has-text('open')").first.focus()
            page.keyboard.press("Enter")     # restore the open filter
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

            # --- flow: deep-link via ?project= URL param ---
            check("selecting a project reflects it in the URL",
                  "project=dogfood__demo" in page.url)
            # opening a ?project= deep link selects that project
            page.goto(base + "?project=dogfood__other")
            page.wait_for_function(
                "() => document.querySelectorAll('#rows tr').length === 1")
            settle()
            check("?project= deep link selects that project",
                  page.eval_on_selector("#project", "el => el.value") == "dogfood__other")
            # a forged/unknown key falls back safely to a real project (no error, no leak)
            page.goto(base + "?project=../../etc/passwd")
            page.wait_for_selector("#rows tr")
            settle()
            check("forged ?project= falls back to a valid project",
                  page.eval_on_selector(
                      "#project",
                      "el => !!el.value && [...el.options].map(o=>o.value).includes(el.value)"))
            # restore to demo for the remaining flows
            page.goto(base + "?project=dogfood__demo")
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
            page.wait_for_function(
                "() => { const r=[...document.querySelectorAll('#rows tr')]"
                ".find(tr=>tr.textContent.includes('0003-document-the-queue-verbs'));"
                " return r && r.querySelector('.pri .pval').textContent.trim()==='95'; }")
            settle()
            shot("prio-done")
            check("reprioritized row jumps to priority 95",
                  page.locator("tr", has=page.get_by_text("0003-document-the-queue-verbs"))
                  .locator(".pri .pval").inner_text().strip() == "95")

            # --- flow: quick priority bump (▲/▼ stepper) ---
            # The ▼ stepper drops 0003 from 95 by one PRIO_STEP (5) through the prio
            # verb; the row must re-sort and reflect 90 immediately, proving the
            # quick-bump goes through the verb and stays in 0–100.
            row = page.locator("tr", has=page.get_by_text("0003-document-the-queue-verbs"))
            row.locator(".pbump[title='-5']").first.click()
            page.wait_for_function(
                "() => { const r=[...document.querySelectorAll('#rows tr')]"
                ".find(tr=>tr.textContent.includes('0003-document-the-queue-verbs'));"
                " return r && r.querySelector('.pri .pval').textContent.trim()==='90'; }")
            settle()
            shot("prio-bump-down")
            check("▼ stepper lowers priority by one step (95→90)",
                  page.locator("tr", has=page.get_by_text("0003-document-the-queue-verbs"))
                  .locator(".pri .pval").inner_text().strip() == "90")

            # --- flow: add context ---
            row = page.locator("tr", has=page.get_by_text("0002-wire-the-action-endpoint"))
            row.locator("button", has_text="Context").first.click()
            page.fill("#m_ctx", "The /action endpoint must stay localhost-only and validate ids.")
            settle()
            shot("context-modal")
            page.locator(".modal button.primary").click()
            page.wait_for_function(
                "() => !document.querySelector('#mask').className.includes('on')")
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
            # #needs now holds two genuine blocks: 0001 (just held above) and the
            # seeded 0006. Release 0001 specifically and wait for it to leave the panel,
            # so 0006 remains for the approve flow below regardless of render order.
            page.locator("#needs").locator("button[onclick*=\"'release','0001-ship-the-control-plane'\"]").click()
            # Release is now guarded by an inline confirm (not a single click) — answer Yes.
            check("release pops an inline confirm before firing",
                  page.locator("#needs .confirm .cyes").count() > 0)
            page.locator("#needs .confirm .cyes").click()
            page.wait_for_function(
                "() => ![...document.querySelectorAll('#needs .id')]"
                ".some(e => e.textContent.includes('0001-ship-the-control-plane'))")
            settle()
            shot("release-done")

            # --- flow: approve (needs-you panel) on the still-held genuine block ---
            held_row = page.locator("#needs").get_by_text("0006-genuinely-blocked")
            check("a genuine held task remains to approve", held_row.count() > 0)
            page.locator("#needs").locator("button[onclick*=\"'approve','0006-genuinely-blocked'\"]").click()
            page.wait_for_selector("#toast.on")   # action round-tripped (success toast)
            settle()
            shot("approve-done")

            # --- flow: release a TAKEN task + one-click Undo (the inverse re-claims it) ---
            # 0002 is taken; releasing it offers an Undo that re-claims, restoring taken.
            taken_row = page.locator("tr", has=page.get_by_text("0002-wire-the-action-endpoint"))
            taken_row.locator("button", has_text="→ Open").first.click()
            taken_row.locator(".confirm .cyes").click()
            page.wait_for_selector("#toast .undo")   # success toast carries the Undo affordance
            settle()
            shot("release-undo-offered")
            check("releasing a taken task offers a one-click Undo",
                  page.locator("#toast .undo").count() > 0)
            page.locator("#toast .undo").click()
            page.wait_for_function(
                "() => { const r=[...document.querySelectorAll('#rows tr')]"
                ".find(tr=>tr.textContent.includes('0002-wire-the-action-endpoint'));"
                " return r && r.querySelector('.st.taken'); }")
            check("Undo re-claims the task back to taken",
                  page.locator("tr", has=page.get_by_text("0002-wire-the-action-endpoint"))
                  .locator(".st.taken").count() > 0)

            # --- flow: done (now guarded by an inline confirm) ---
            row = page.locator("tr", has=page.get_by_text("0002-wire-the-action-endpoint"))
            row.locator("button", has_text="Done").first.click()
            row.locator(".confirm .cyes").click()
            page.wait_for_selector("#toast.on")   # done verb round-tripped
            # surface the done rows so the status shows
            if not page.locator(".chip.on:has-text('done')").count():
                page.locator(".chip:has-text('done')").first.click()
            # wait for the reload to land the done pill before screenshotting
            page.wait_for_function(
                "() => { const r=[...document.querySelectorAll('#rows tr')]"
                ".find(tr=>tr.textContent.includes('0002-wire-the-action-endpoint'));"
                " return r && r.querySelector('.st.done'); }")
            settle()
            shot("done-done")
            check("done task carries the done status pill",
                  page.locator("tr", has=page.get_by_text("0002-wire-the-action-endpoint"))
                  .locator(".st.done").count() > 0)

            # --- flow: expand a row to READ its body + metadata (no edit modal) ---
            # Click the title to reveal the detail row; assert the seeded body text and
            # a metadata field render read-only, then collapse it again.
            row = page.locator("tr", has=page.get_by_text("0001-ship-the-control-plane"))
            row.locator(".title.row-toggle").first.click()
            page.wait_for_selector("tr.detail .detail-body")
            settle()
            shot("row-expanded")
            check("expanded row shows the task body inline",
                  "Seeded fixture task" in page.locator("tr.detail .detail-body").first.inner_text())
            check("expanded row shows read-only metadata (created)",
                  page.locator("tr.detail .detail-meta dt", has_text="created").count() > 0)
            check("expanding does not open the edit modal",
                  not page.locator("#mask").get_attribute("class").endswith("on"))
            row.locator(".title.row-toggle").first.click()   # collapse
            page.wait_for_function(
                "() => !document.querySelector('tr.detail')")
            check("row collapses again", page.locator("tr.detail").count() == 0)

            # --- flow: auto-refresh while open (no manual reload) ---
            # Simulate a worker mutating a task by rewriting its file on disk (the
            # server reads the todo dir fresh per /api/tasks), then assert the open
            # table picks the change up on its own poll — proving setInterval works.
            def set_priority_on_disk(tid, prio):
                path = os.path.join(data, "projects", "dogfood__demo", "todo", tid + ".md")
                txt = open(path, encoding="utf-8").read()
                txt = re.sub(r"^priority:.*$", f"priority: {prio}", txt, count=1, flags=re.M)
                open(path, "w", encoding="utf-8").write(txt)

            set_priority_on_disk("0001-ship-the-control-plane", 11)
            page.wait_for_function(
                "() => { const r=[...document.querySelectorAll('#rows tr')]"
                ".find(tr=>tr.textContent.includes('0001-ship-the-control-plane'));"
                " return r && r.querySelector('.pri .pval').textContent.trim()==='11'; }")
            settle()
            shot("auto-refresh")
            check("background poll updates a row without a manual reload",
                  page.locator("tr", has=page.get_by_text("0001-ship-the-control-plane"))
                  .locator(".pri .pval").inner_text().strip() == "11")

            # --- flow: auto-refresh is paused while a modal is open ---
            # Open an edit, type, then mutate a DIFFERENT task on disk and wait past
            # the poll interval: the modal must stay open with the in-progress text
            # intact (the poll skips loadTasks while the mask is shown).
            row = page.locator("tr", has=page.get_by_text("0003-document-the-queue-verbs"))
            row.locator("button", has_text="Edit").first.click()
            page.fill("#m_title", "Half-typed edit in flight")
            set_priority_on_disk("0001-ship-the-control-plane", 22)
            page.wait_for_timeout(int(6000))   # comfortably past AUTO_REFRESH_MS (5000)
            check("modal stays open through a background-poll window",
                  page.locator("#mask").get_attribute("class").endswith("on"))
            check("in-progress edit text is not clobbered by the poll",
                  page.input_value("#m_title") == "Half-typed edit in flight")
            page.locator(".modal button[onclick='closeModal()']").click()
            settle()

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
