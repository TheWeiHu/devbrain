<h1 align="center">devbrain</h1>

<p align="center">
  <strong>Turn the prompts you write into a durable, queryable brain any agent can resume from.</strong>
</p>

<p align="center">
  <a href="https://github.com/TheWeiHu/devbrain/releases"><img src="https://img.shields.io/github/v/release/TheWeiHu/devbrain" alt="Release"></a>
  <a href="https://github.com/TheWeiHu/devbrain/actions/workflows/test.yml"><img src="https://img.shields.io/github/actions/workflow/status/TheWeiHu/devbrain/test.yml" alt="CI"></a>
  <a href="https://github.com/TheWeiHu/devbrain/blob/main/LICENSE"><img src="https://img.shields.io/github/license/TheWeiHu/devbrain" alt="MIT license"></a>
  <a href="https://claude.ai/code"><img src="https://img.shields.io/badge/built%20for-Claude%20Code-d97757" alt="Built for Claude Code"></a>
  <a href="https://developers.openai.com/codex"><img src="https://img.shields.io/badge/also%20for-Codex-111111" alt="Also for Codex"></a>
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
[Claude Code](https://claude.ai/code), with the same workflows installed as skills in
other agents (e.g. [Codex](https://openai.com/codex/)).

## How It Works

`devbrain install` wires Claude Code (and any other installed agents) on *this machine*, then gets out of the way:

```
A. Capture    every prompt → raw markdown log      automatic, model-free · source of truth
B. Brain      /distill → brain pages + queue        markdown on disk · searchable, rebuildable
C. Assemble   /continue → brief + work top task     opens a minimal-MVP PR · /loop to drain
```

The hooks are `devbrain hook <event>` commands registered in the agent's
`settings.json` — a `UserPromptSubmit` hook logs every prompt verbatim; a 5-min timer
commits and pushes it off-machine. Config lives at `~/.config/devbrain/config.json`.
The markdown log layout is the same across agents, with only header metadata naming
which one. `/distill` folds new log into linked brain pages and queue tasks;
`/continue` pulls what's relevant, briefs you, and works the top task. The log is keyed
by **git remote**, so all worktrees of a repo collapse to one project. Full design in
[`DESIGN.md`](DESIGN.md).

**Only the system ships.** devbrain is a single binary. Your prompts and brain live in
a *separate* private store at `~/devbrain-data` that you own — `devbrain install`
creates it; give it a private remote to back up and sync.

## Install

```bash
brew install TheWeiHu/devbrain/devbrain
devbrain install
```

The tap goes live with the 1.0.0 release; until then use
`go install github.com/TheWeiHu/devbrain/cmd/devbrain@latest` or a
[release tarball](https://github.com/TheWeiHu/devbrain/releases).

`devbrain install` is idempotent and wires only *this machine*. In a terminal it asks
y/n per component; non-interactive runs take every default. When it finishes it opens
the browser dashboard (`devbrain queue` — Board · Nightshift · Profile). Common flags:

```bash
devbrain install --without nightshift          # skip the overnight loop
devbrain install --only capture                # just the prompt-capture hook
devbrain install --no-open                     # don't auto-open the dashboard
DEVBRAIN_DATA=~/path devbrain install          # store the brain elsewhere
```

devbrain needs only your coding agent and Git — no python3 or Node. Some agents may
ask you to review and trust the installed hooks with `/hooks` on next startup; that
is their normal hook trust flow.

## Daily Use

| Command | What it does |
|---|---|
| *(automatic)* | every prompt captured; flusher commits and pushes every 5 min |
| **`/distill`** | fold new log → brain pages **and** queue tasks |
| **`/continue`** | resume: brief, then work the top task as a minimal-MVP PR |
| **`/work`** | lean drain turn: top task → MVP PR, no fold-in/briefing (for `/loop` + nightshift) |
| **`/loop /work`** | keep draining the queue fast, one MVP PR per task |
| **`/reconcile`** | mark brain facts the live repo contradicts (auto-runs ~daily) |
| `gbrain search` / `devbrain brain search` | query the brain from the shell (gbrain if installed, else offline grep) |
| `devbrain queue` | browser control plane for the queue (view · edit · prioritize · unblock) |
| `devbrain help` | every devbrain subcommand |

The brain records *what happened*; the queue records *what's next* — one markdown file
per task, priority-ranked. `/distill` fills it, `/continue` drains it, and a task isn't
`done` until its PR merges. Agents without slash commands run the same workflows as
skills (`$distill`, `$continue`, `$work`, `$reconcile`).

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
- Ranked + semantic brain search comes from the optional gbrain engine, installed by default; skip it with `devbrain install --without-gbrain` and the offline `devbrain brain search` still works.
- `devbrain import` — seed the brain from your existing agent transcripts.
- Re-run `devbrain install` anytime; it only adds what's missing. Tear down with `devbrain uninstall` (leaves your data untouched).
