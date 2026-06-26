<h1 align="center">devbrain</h1>

<p align="center">
  <strong>Turn the prompts you write into a durable, queryable brain any agent can resume from.</strong>
</p>

<p align="center">
  <a href="https://github.com/TheWeiHu/devbrain/releases"><img src="https://img.shields.io/github/v/release/TheWeiHu/devbrain" alt="Release"></a>
  <a href="https://github.com/TheWeiHu/devbrain/actions/workflows/test.yml"><img src="https://img.shields.io/github/actions/workflow/status/TheWeiHu/devbrain/test.yml" alt="CI"></a>
  <a href="https://github.com/TheWeiHu/devbrain/blob/main/LICENSE"><img src="https://img.shields.io/github/license/TheWeiHu/devbrain" alt="MIT license"></a>
  <a href="https://claude.ai/code"><img src="https://img.shields.io/badge/built%20for-Claude%20Code-d97757" alt="Built for Claude Code"></a>
</p>

<p align="center">
  <a href="#how-it-works">How It Works</a>
  ·
  <a href="#install">Install</a>
  ·
  <a href="#daily-use">Daily Use</a>
  ·
  <a href="#nightshift">nightshift</a>
  ·
  <a href="DESIGN.md">Design</a>
</p>

<p align="center">
  <img src="docs/dashboard-demo.gif" alt="devbrain dashboards — the Profile and the Board" width="860">
</p>

Every prompt is captured to a private, git-synced markdown store, distilled into a
searchable brain, and replayable by any future session or machine. Markdown + git is
the source of truth; everything else is a rebuildable projection. Built for
[Claude Code](https://claude.ai/code).

## How It Works

`./setup` wires Claude Code on *this machine*, then gets out of the way:

```
A. Capture    every prompt → raw markdown log      automatic, model-free · source of truth
B. Brain      /distill → brain pages + queue        markdown on disk · searchable, rebuildable
C. Assemble   /continue → brief + work top task     opens a minimal-MVP PR · /loop to drain
```

A `UserPromptSubmit` hook logs every prompt verbatim; a 5-min timer commits and pushes
it off-machine. `/distill` folds new log into linked brain pages and queue tasks;
`/continue` pulls what's relevant, briefs you, and works the top task. The log is keyed
by **git remote**, so all worktrees of a repo collapse to one project. Full design in
[`DESIGN.md`](DESIGN.md).

**Only the system ships.** This repo is the installer, hooks, and skills. Your prompts
and brain live in a *separate* private store at `~/devbrain-data` that you own — `setup`
creates it; give it a private remote to back up and sync.

## Install

One line — no clone, no config:

```bash
npx getdevbrain install
```

Idempotent, wires only *this machine*. In a terminal it asks y/n per component;
non-interactive runs take every default. When it finishes it opens the browser
dashboard (`devbrain queue` — the Board · Nightshift · Profile control plane) so you
land somewhere instead of on an invisible set of hooks; pass `--no-open` to skip it.
Common flags:

```bash
npx getdevbrain install --without nightshift          # skip the overnight loop
npx getdevbrain install --only capture                # just the prompt-capture hook
npx getdevbrain install --no-open                     # don't auto-open the dashboard
DEVBRAIN_DATA=~/path npx getdevbrain install           # store the brain elsewhere
```

Prefer to clone? `git clone … && ./setup` takes the same flags. **Needs only**
[Claude Code](https://claude.ai/code), Git, and `python3` — devbrain itself has zero
runtime dependencies. The brain is plain on-disk markdown, searchable out of the box via
`devbrain brain search/get`. For ranked + semantic search, setup installs the optional
gbrain engine by default (globally via [`bun`](https://bun.sh)); opt out with
`./setup --without-gbrain` (or answer `n` at the prompt), and add an OpenAI key for
semantic ranking. Even without it, the offline `devbrain brain` search keeps working.
Already have history? `devbrain import` seeds the brain from your existing Claude Code
transcripts.

## Daily Use

| Command | What it does |
|---|---|
| *(automatic)* | every prompt captured; flusher commits and pushes every 5 min |
| **`/distill`** | fold new log → brain pages **and** queue tasks |
| **`/continue`** | resume: brief, then work the top task as a minimal-MVP PR |
| **`/loop /continue`** | keep draining the queue, one MVP PR per task |
| **`/reconcile`** | mark brain facts the live repo contradicts (auto-runs ~weekly) |
| `gbrain search` / `devbrain brain search` | query the brain from the shell (gbrain if installed, else offline grep) |
| `devbrain queue` | browser control plane for the queue (view · edit · prioritize · unblock) |
| `devbrain help` | every devbrain subcommand |

The brain records *what happened*; the queue records *what's next* — one markdown file
per task, priority-ranked. `/distill` fills it, `/continue` drains it. A task isn't
`done` until its PR merges.

## nightshift

Run several `claude` workers in parallel against the queue, each in its own worktree,
auto-merging green work onto a throwaway `nightshift` branch — you wake to one
`git diff main...nightshift`.

```bash
devbrain nightshift start ~/nightshift/myrepo   # launch the fleet (runs until stopped)
devbrain nightshift watch                       # live browser dashboard
devbrain nightshift stop                        # stop the fleet
```

Installing it never spawns anything — the fleet runs only when you start it, and it
does autonomous git ops and spends real tokens, so point the first runs at a throwaway.
You stay the only `nightshift → main` gate.

## More

- [`DESIGN.md`](DESIGN.md) — architecture, the TODO queue, and the golden rule (never lose the log)
- [`SECURITY.md`](SECURITY.md) — what's captured, where it's stored, who can see it, and how to report a vuln
- [`CHANGELOG.md`](CHANGELOG.md) — release history
- `make test` — run the full suite
- Re-run `./setup` anytime; it only adds what's missing. Tear down with `scripts/uninstall.sh` (leaves your data untouched).
