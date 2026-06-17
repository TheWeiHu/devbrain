# /drain — autonomous overnight loop (PLAN)

**One line:** an orchestrator drives interactive `claude` sessions (in tmux, so you
can watch and remote-control them) to drain work toward a written objective,
auto-merging green PRs into a disposable `staging` branch — the human's only job is
`git diff main...staging` in the morning.

This is devbrain's missing edge. The pipeline already runs
`prompt → brain → queue → work → follow-ups`; the only un-automated link is
**follow-ups → next prompt** — that link is the human. `/drain` fills it. The loop
becomes a *second* legitimate queue writer (alongside `/distill`), steered by an
`objective.md`, with merge-to-`main` staying the human gate.

## Why interactive sessions, not `claude -p`
Decided: manage our own persistent sessions so we get **remote control** — attach
live, watch, and type a course-correction mid-run. A headless one-shot has no
session to attach to. `claude -p` would be easier, but it can't be watched or steered.

The one hard part this creates — "when is a turn done?" — is **not** solved by
scraping the TUI pane. It's solved by a **Stop-hook turn marker** (see Phase 0):
the pane is for humans to watch; a marker file is the driver's signal. Control is
out-of-band, so the on-screen panes are purely cosmetic and can be as elaborate as
we like.

## Key design decisions (from the brainstorm)
- **Substrate:** tmux is the bus. `send-keys` drives; a Stop-hook marker file signals
  turn completion; `tmux attach` (or iTerm2 `-CC` / a web bridge) is remote control.
- **`staging` is disposable, like the brain.** Workers branch off `staging` (not
  `main`) so work *compounds* overnight. Auto-merge gated only on **green tests**
  (objective, no external skill). If the morning diff is junk: `git reset --hard main`
  on staging — only compute is lost. Human gates `staging → main`.
- **`objective.md` is the steering wheel.** Human-authored, stable, per project
  (`~/devbrain-data/projects/<project>/objective.md`). The loop optimizes against it,
  may write its own TODOs (tagged `source: loop` vs `distill`), and is allowed to
  STOP when it hits diminishing returns — stopping is a valid outcome, not busywork.
- **Persistent session ≠ persistent context.** Keep the session alive (for remote
  control) but `/clear` between tasks and re-orient via `/continue`, so the brain
  stays the memory and context stays lean.
- **Usage-limit fallback:** on limit, release the in-flight task, sleep until the
  window resets, retry the same step. Wall-clock cap so it doesn't run into the day.
- **Watch-wall:** tmux tiled panes (MVP) or iTerm2 `tmux -CC` native windows, each
  pane titled `task · status`, plus a driver-fed status/dashboard pane.
- **No external skills.** Build only on `/continue`, `/distill`, and plain
  `git`/`gh`/`tmux`/`claude`. `codex` only if the binary happens to be installed.

## The scripts (current, consolidated set)
- **`hooks/turn-marker.sh`** — Stop hook; appends one line per finished turn to
  `$DRAIN_MARKER`. The turn-complete signal (never scrape the pane). No-op unless
  `DRAIN_MARKER` is set, so it is registered GLOBALLY in `~/.claude/settings.json`
  (a worktree-local hook would be stashed away by `/continue`'s `git stash -u`).
- **`scripts/drain-orchestrate.sh`** — the engine. N workers, each in its own git
  worktree off `origin/staging`; assigns `/continue`; turn-complete (marker) →
  green-gate (pytest in a venv) → **serialized merge into `staging`** → task `done`;
  conflict/red → requeue (retry cap). Hang (frozen pane) → kill+release+respawn.
  Low queue → a planning turn that adds TODOs from `followups.md`. Self-installs the
  marker hook at boot. You review `git diff main...staging` and merge to main.
- **`scripts/drain-wall.sh`** — the watch wall: N read-only worker mirrors + 1
  CONTROL pane.
- **`scripts/drain-ctl.sh`** — control library loaded into the control pane:
  `status/mon/say/at/killw/ostart/ostop/olog/sdiff/prs/q/wall`.

Single-worker `drain-drive.sh` / `drain-watch.sh` were folded into the orchestrator
(`--workers 1`) and the wall, and removed.

## Status / what's still open
- Built + validated: marker, worktrees, parallel claim, question-avoidance
  (`--disallowedTools AskUserQuestion` + drain rules), staging + green-gate +
  serialized automerge, the control wall.
- Next: convergence proof (queue must trend to empty, not grow), orchestrator
  durability (persist state + reconcile existing sessions on restart + run under
  launchd), and collapsing per-task PRs into one `staging→main` PR.

## Test target
**chess-equity** — objective in the brain (`objective.md`), queue seeded, tests
scaffolded (pytest green-gate). Run:
`scripts/drain-orchestrate.sh --repo ~/drain/chess-equity` then `scripts/drain-wall.sh`.
