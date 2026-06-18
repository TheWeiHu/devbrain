---
name: nightshift
description: |
  EXPERIMENTAL. Autonomous overnight loop: spawns N parallel `claude` workers (in
  tmux, watchable + steerable) that drain the devbrain TODO queue toward the
  project's objective.md, each in its own git worktree off `staging`. Turn-complete
  is a Stop-hook marker; the orchestrator green-gates each finished branch and
  serially merges it into a disposable `staging` branch, then closes the task.
  Hung/dead workers are respawned; an empty queue triggers a planning turn that
  adds new TODOs, so it runs as long as you let it. You wake up and review one
  diff: `git diff main...staging`, then merge to main. Use when asked to "run
  nightshift", "start the overnight loop", "drain the queue autonomously", or
  "spin up the agent fleet". Costs real tokens and does autonomous git ops
  (force-pushes `staging`, opens PRs) — opt-in only.
---

# /nightshift — the autonomous overnight loop

**What it is.** devbrain captures `prompt → brain → queue → work → follow-ups`. The
one un-automated link is *follow-ups → next prompt* — normally you. nightshift fills
it: a fleet of `claude` workers drains the queue toward `objective.md`, compounding
their work into a disposable `staging` branch you review in the morning. You shrink
to one job: gate `staging → main`.

⚠️ **Experimental + opt-in.** It spends real tokens, runs many agents in parallel,
and performs autonomous git operations (force-pushes `staging`, opens PRs). Never
auto-started; never point the first runs at anything precious — `staging` is reset
freely. Requires `tmux` (`brew install tmux`).

## The pieces
- `hooks/turn-marker.sh` — Stop hook; the turn-complete signal. No-op unless
  `NIGHTSHIFT_MARKER` is set, so it's registered globally and safe everywhere.
- `scripts/nightshift-orchestrate.sh` — the engine (spawn / assign / green-gate /
  serial-merge-to-staging / requeue / respawn / replan). Runs forever by default.
- `scripts/nightshift-status.py` + `nightshift-serve.py` + `nightshift-dashboard.html`
  — the browser dashboard (the monitor). Replaced the old tmux watch-wall.

## Prerequisites
1. `brew install tmux`
2. A dedicated clone (isolated from your interactive workspace):
   `git clone <repo> ~/nightshift/<project>` (or any path; pass it as `--repo`).
3. An `objective.md` in the project's brain
   (`~/devbrain-data/projects/<key>/objective.md`) — the north star.
4. A seeded TODO queue (`/distill`) and, ideally, a test command for the green-gate.

## Run it — the `nightshift` command (no path-pasting)
```bash
nightshift start ~/nightshift/<project>   # launch the fleet (forever; remembers the repo)
nightshift watch                          # open the live browser dashboard
nightshift status                         # one-line text status
nightshift review                         # tasks PARKED for you (need attention)
nightshift stop                           # stop the fleet + dashboard
```
`start` forwards orchestrator flags: `--workers N`, `--keep-staging`, `--test-cmd`,
`--no-gate`, `--strict-gate`, `--hang`, `--replan`, `--max-turns`, `--max-wall`.

**Watching:** `nightshift watch` serves a self-contained dashboard (worker panes,
scoreboard, staging feed) via a local `python3 -m http.server` and opens it in your
browser — it stays live in the background. Parked tasks raise a **"Needs you"**
banner there *and* fire a native macOS notification the moment they park, so the one
human-touch state surfaces itself. (With the `--tmux` backend only, you can also
attach a worker session — `nightshift attach <i>` — and steer it: `nightshift say <i> "…"`.)

## In the morning
```bash
git -C ~/nightshift/<project> diff main...staging   # everything that landed
# merge to main if you like it, or reset staging to main and only compute was lost
nightshift review                                   # anything parked that needs a human
```
