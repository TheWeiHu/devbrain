---
name: nightshift
description: |
  Autonomous overnight loop: spawns N parallel `claude` workers (in
  tmux, watchable + steerable) that drain the devbrain TODO queue toward the
  project's objective.md, each in its own git worktree off `nightshift`. Turn-complete
  is a Stop-hook marker; the orchestrator green-gates each finished branch and
  serially merges it into a disposable `nightshift` branch, then closes the task.
  Hung/dead workers are respawned; an empty queue triggers a planning turn that
  adds new TODOs, so it runs as long as you let it. You wake up and review one
  diff: `git diff main...nightshift`, then merge to main. Use when asked to "run
  nightshift", "start the overnight loop", "drain the queue autonomously", or
  "spin up the agent fleet". Costs real tokens and does autonomous git ops
  (force-pushes `nightshift`, opens PRs) — you start it deliberately; it never auto-runs.
---

# /nightshift — the autonomous overnight loop

**What it is.** devbrain captures `prompt → brain → queue → work → follow-ups`. The
one un-automated link is *follow-ups → next prompt* — normally you. nightshift fills
it: a fleet of `claude` workers drains the queue toward `objective.md`, compounding
their work into a disposable `nightshift` branch you review in the morning. You shrink
to one job: gate `nightshift → main`.

⚠️ **Runs autonomously + spends real tokens.** It runs many agents in parallel and
performs autonomous git operations (force-pushes `nightshift`, opens PRs). The toolset
installs by default, but it is **never auto-started** — it runs only when you start it.
Never point the first runs at anything precious — `nightshift` is reset freely.
Requires `tmux` (`brew install tmux`).

## The pieces
- `devbrain hook turn-marker` — Stop hook; the turn-complete signal. No-op unless
  `NIGHTSHIFT_MARKER` is set, so it's registered globally and safe everywhere.
- `scripts/nightshift-orchestrate.sh` — the engine (spawn / assign / green-gate /
  serial-merge-to-nightshift / requeue / respawn / replan). Runs forever by default.
- `scripts/nightshift-status.py` — writes `<repo>/.nightshift/status.json`, which the
  devbrain dashboard reads and renders under its 🌙 Nightshift toggle (the monitor
  lives inside the combined dashboard now — no separate server). Replaced the old tmux watch-wall.

## Prerequisites
1. `brew install tmux`
2. A dedicated clone (isolated from your interactive workspace):
   `git clone <repo> ~/nightshift/<project>` (or any path; pass it as `--repo`).
3. An `objective.md` in the project's brain
   (`~/devbrain-data/projects/<key>/objective.md`) — the north star.
4. A seeded TODO queue (`/distill`) and, ideally, a test command for the green-gate.

## Run it — `devbrain nightshift` (no path-pasting)
```bash
devbrain nightshift start ~/nightshift/<project>   # launch the fleet (forever; remembers the repo) + auto-open the dashboard
devbrain nightshift start <repo> --only 0081,0076  # FIXED-SET: drain ONLY those tasks, then stop (no new tasks)
devbrain nightshift watch                          # (re)open the live browser dashboard manually
devbrain nightshift status                         # one-line text status
devbrain nightshift review                         # tasks PARKED for you (need attention)
devbrain nightshift stop                           # stop the fleet + dashboard
```
`start` forwards orchestrator flags: `--workers N`, `--keep-nightshift`, `--test-cmd`,
`--no-gate`, `--strict-gate`, `--hang`, `--replan`, `--max-turns`, `--max-wall`, `--only`.

**Fixed-set mode (`--only IDS`).** A bounded run: workers drain ONLY the listed tasks
(comma list — full slug `0081-foo` or bare number `0081`), the empty-queue **planning turn
is disabled** (so no new tasks are ever created), and the fleet **winds down and exits**
once the selected set is all merged or held — instead of running forever. Use it for "do
exactly these overnight, then stop." Under the hood the orchestrator applies
`DEVBRAIN_TODO_ONLY` to its own queue reads and passes it to every worker turn, scoping
the whole queue (`next`/`list`/open-count + every worker's `/work`) to the subset —
without exporting it process-wide, so it can't leak into the green-gate's test suite.

**Or just drag.** In the dashboard (📋 Board), ⌘/Ctrl-click task cards to select them, then
drag the selection onto the floating **🌙** (or click it) → confirm → it launches exactly
this fixed-set run for you (resolving the project's repo from its recent session logs). The
quickest way to start a scoped overnight run without touching the CLI.

**ALWAYS open the monitor — this is not optional.** The dashboard (worker panes,
scoreboard, nightshift feed) is the *only* window the user has into a fleet that runs
unattended for hours and does autonomous git ops. So whenever you start the fleet on the user's behalf:

1. Run plain `devbrain nightshift start <repo>` — **never** add `--no-watch`. That flag
   exists only for true headless/cron runs with no human present; an interactive session
   always has a human who needs the monitor.
2. `start` opens the dashboard automatically. If for any reason it didn't (it printed
   `watch it: devbrain nightshift watch`, or you passed `start` through a wrapper that
   swallowed the open), **immediately run `devbrain nightshift watch`** — do not consider
   the launch done until the monitor is open.
3. Then surface the dashboard URL **the CLI actually printed** (`🌙 dashboard → …`)
   verbatim — the queue dashboard auto-bumps off 8799 when that port is already held, so
   read it from the command's output rather than assuming a fixed port — and tell them
   that's where to watch progress and approve parked tasks.

Treat "fleet started but monitor not opened" as a failed launch, not a success.

The run monitor lives inside the devbrain dashboard (the 🌙 Nightshift toggle) —
it stays live in the background. Parked tasks raise a **"Needs you"** banner there
*and* fire a native macOS notification the moment they park, so the one human-touch
state surfaces itself. (With the `--tmux` backend only, you can also attach a worker
session — `devbrain nightshift attach <i>` — and steer it: `devbrain nightshift say <i> "…"`.)

## In the morning
```bash
git -C ~/nightshift/<project> diff main...nightshift   # everything that landed
# merge to main if you like it, or reset nightshift to main and only compute was lost
devbrain nightshift review                          # anything parked that needs a human
```
