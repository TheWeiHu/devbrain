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

## The pieces (see `PLAN.md` for design rationale)
- `hooks/turn-marker.sh` — Stop hook; the turn-complete signal. No-op unless
  `NIGHTSHIFT_MARKER` is set, so it's registered globally and safe everywhere.
- `scripts/nightshift-orchestrate.sh` — the engine (spawn / assign / green-gate /
  serial-merge-to-staging / requeue / respawn / replan). Runs forever by default.
- `scripts/nightshift-wall.sh` — the watch wall: N worker mirrors + 1 control pane.
- `scripts/nightshift-ctl.sh` — control-pane command library.

## Prerequisites
1. `brew install tmux`
2. A dedicated clone (isolated from your interactive workspace):
   `git clone <repo> ~/nightshift/<project>` (or any path; pass it as `--repo`).
3. An `objective.md` in the project's brain
   (`~/devbrain-data/projects/<key>/objective.md`) — the north star.
4. A seeded TODO queue (`/distill`) and, ideally, a test command for the green-gate.

## Run it
```bash
# start the fleet (runs forever; 3 workers; gates on pytest)
scripts/nightshift-orchestrate.sh --repo ~/nightshift/<project> --workers 3

# watch + control (3 worker mirrors + a control pane)
scripts/nightshift-wall.sh 3 ~/nightshift/<project>
```
Useful flags: `--max-turns N` / `--max-wall SECS` (bound a run), `--keep-staging`
(accumulate instead of reset), `--test-cmd "<cmd>"`, `--no-gate`, `--strict-gate`,
`--hang SECS`, `--replan SECS`.

## Control pane commands
`s`/`status` · `mon` (live) · `say <i> <msg>` (steer a worker) · `at <i>` (attach) ·
`killw <i>` · `ostart [N]` / `ostop` · `olog` (orchestrator log) · `sdiff`
(`git diff main...staging`) · `prs` · `q` · `wall` · `help`.

## In the morning
```bash
git -C ~/nightshift/<project> diff main...staging   # everything that landed
# merge to main if you like it, or reset staging to main and only compute was lost
```

## Stop it
`ostop` (control pane), or `pkill -f nightshift-orchestrate.sh`. Worker tmux
sessions keep running until you `killw <i>` / kill them.
