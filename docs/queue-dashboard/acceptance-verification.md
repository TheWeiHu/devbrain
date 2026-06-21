# Queue control plane (0034) — acceptance verification

Mechanical check of every acceptance criterion in task `0034` against
`origin/nightshift` (`bd6cd91`). Each line cites the code/test that proves it.
Re-run the gate with `bash scripts/test-queue.sh` (34 assertions, one server boot).

**Verdict: 11/11 PASS — no gaps. The 0034 "are-we-done" gate is green.**

| # | Criterion | Result | Evidence |
|---|-----------|--------|----------|
| 1 | `devbrain queue --help` works; `devbrain help` lists `queue` | ✅ PASS | `scripts/devbrain` dispatch case `queue\|dashboard` (l.43); help banner lists `queue` (l.6); both run clean. |
| 2 | Localhost-only server (binds 127.0.0.1, not 0.0.0.0); `--no-open`/port flag for headless | ✅ PASS | `queue.py:355` `ThreadingHTTPServer(("127.0.0.1", …))`; argparse `--port`/`--no-open`/`--data` (l.345-348); no `0.0.0.0` anywhere; test "server binds 127.0.0.1 only". |
| 3 | Dropdown populated by scanning `projects/*/todo`; a disk-added project appears with no code change | ✅ PASS | `project_keys()` lists `projects/*` dirs that contain a `todo/` (l.145-152); HTML `#project` select filled from `/api/projects`; test "discovers both projects on disk". |
| 4 | Tasks render grouped by status incl. `done` & `held`; `held` shows `reason` in a "needs you" section | ✅ PASS | HTML `STATUSES` incl. `done`/`held` (l.89); `.needs` panel renders non-parked held with `.reason` (l.142-152); `parked`-prefixed holds excluded → own chip (l.92-95), mirroring `nightshift-status.py`. |
| 5 | Each mutation issues the matching `devbrain-todo.sh` verb and the file reflects it; no path writes markdown directly | ✅ PASS | `VERBS` maps every action to a todo verb (l.46-57); `run_verb()` only ever `subprocess.run`s `todo.sh` (l.254-256); no `.md` is opened for write in `queue.py`; test asserts on-disk field after prio/edit/claim/review/done/hold/release/context/add. |
| 6 | Action endpoint refuses non-localhost; validates id against the selected project (no traversal / cross-project writes) | ✅ PASS | `_local_only()` checks Host + Origin against loopback (l.284-295); `IDRE` + existence-in-project-queue check (l.230-235); tests: forged Host→403, forged Origin→403, traversal id rejected, cross-project write rejected (nothing written). |
| 7 | Nightshift run active → links/embeds its monitor; none active → still fully functions | ✅ PASS | `nightshift_run()` trusts `nightshift-run.json` only if the recorded port has a live listener (l.185-209); HTML shows the monitor link only when `active` (l.130-137); tests cover no-file/dead-port/live-port/unknown-project. Standalone otherwise. |
| 8 | A test script exercises the verbs against a temp `DEVBRAIN_DATA`, asserts file state, one boot many assertions, no network/sleeps | ✅ PASS | `scripts/test-queue.sh`: one in-process server boot, 34 assertions, `mktemp -d` data, socket bound at construction (no sleeps), every mutation re-read from disk. |
| 9 | README/CLI help updated; `queue` documented alongside `nightshift` | ✅ PASS | `README.md:103` (command table) and `:118` (control-plane prose); `devbrain help` + `devbrain queue --help` both document it. |
| 10 | PR includes browser-driven screenshot evidence of the key flows | ✅ PASS | `docs/queue-dashboard/screenshots/` holds 15 dogfood screenshots (overview, filter, create, edit, prio, context, hold, release, approve, done) refreshed for the parked-split (commit `dbac14d`). |
| 11 | Parked ≠ blocked: focus-parked holds hidden from "needs you", real blocks surfaced | ✅ PASS | `PARKED_RE`/`isParked` exclude `parked`-prefixed holds from the needs banner and give them a `parked` chip (HTML l.92-95, 142); `project_summary()` counts `parked` separately (l.177-179); test "summary splits parked from held". |

## How this was checked
- Read `scripts/queue.py`, `scripts/queue-dashboard.html`, `scripts/devbrain`,
  `scripts/test-queue.sh`, `scripts/todo.sh`, `README.md` at `origin/nightshift`.
- Ran `bash scripts/test-queue.sh` → **34 passed, 0 failed**.
- Ran `devbrain help`, `devbrain queue --help`; grepped for `0.0.0.0` (none) and for
  any direct `.md` write in `queue.py` (none — the only `.write` is the HTTP response).

No follow-up TODOs filed: every criterion is met and the test suite locks each one in.
