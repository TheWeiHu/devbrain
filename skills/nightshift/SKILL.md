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
First, a one-line preflight (catches the #1 install failure — a CLI symlink left
dangling after a Conductor workspace is deleted; `command -v` and tab-completion
both still "see" it, so it only fails on exec with a misleading ENOENT):
```bash
[ -x "$(command -v nightshift)" ] && nightshift doctor \
  || echo "reinstall nightshift: bash <devbrain-repo>/scripts/install.sh --only nightshift"
```
Then:
```bash
nightshift start ~/nightshift/<project>   # launch the fleet (forever; remembers the repo) + auto-open the dashboard
nightshift watch                          # (re)open the live browser dashboard manually
nightshift status                         # one-line text status
nightshift review                         # tasks PARKED for you (need attention)
nightshift doctor                         # diagnose a broken/ephemeral install + print the exact fix
nightshift stop                           # stop the fleet + dashboard
```
The CLI is installed as a **stable copy** under `~/.claude/nightshift/` (symlinked
onto your PATH), so it survives deletion of the workspace you installed from. If you
ever symlink it straight at a workspace path, `nightshift doctor` flags it and `start`
warns you before that workspace rots.
`start` forwards orchestrator flags: `--workers N`, `--keep-staging`, `--test-cmd`,
`--no-gate`, `--strict-gate`, `--hang`, `--replan`, `--max-turns`, `--max-wall`.

**Watching:** `start` auto-opens the dashboard for you — pass `--no-watch` to skip
that (e.g. headless/cron runs), then `nightshift watch` reopens it on demand. The
dashboard is a self-contained page (worker panes, scoreboard, staging feed) served
via a local `python3 -m http.server` — it stays live in the background. Parked tasks raise a **"Needs you"**
banner there *and* fire a native macOS notification the moment they park, so the one
human-touch state surfaces itself. (With the `--tmux` backend only, you can also
attach a worker session — `nightshift attach <i>` — and steer it: `nightshift say <i> "…"`.)

## In the morning
```bash
git -C ~/nightshift/<project> diff main...staging   # everything that landed
# merge to main if you like it, or reset staging to main and only compute was lost
nightshift review                                   # anything parked that needs a human
```
