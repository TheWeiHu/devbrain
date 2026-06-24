<h1 align="center">devbrain</h1>

<p align="center">
  <strong>Turn the prompts you write — in <em>any</em> repo — into a durable, queryable brain any agent can resume from. The log is the agent.</strong>
</p>

<p align="center">
  <a href="https://github.com/TheWeiHu/devbrain/releases"><img src="https://img.shields.io/github/v/release/TheWeiHu/devbrain" alt="Release"></a>
  <a href="https://github.com/TheWeiHu/devbrain/actions/workflows/test.yml"><img src="https://img.shields.io/github/actions/workflow/status/TheWeiHu/devbrain/test.yml" alt="CI"></a>
  <a href="https://github.com/TheWeiHu/devbrain/blob/main/LICENSE"><img src="https://img.shields.io/github/license/TheWeiHu/devbrain" alt="MIT license"></a>
  <a href="https://claude.ai/code"><img src="https://img.shields.io/badge/built%20for-Claude%20Code-d97757" alt="Built for Claude Code"></a>
</p>

<p align="center">
  <a href="#how-it-works">How it works</a>
  ·
  <a href="#install">Install</a>
  ·
  <a href="#daily-use">Daily use</a>
  ·
  <a href="#todo-queue">TODO queue</a>
  ·
  <a href="#nightshift--drain-the-queue-overnight-experimental-off-by-default">nightshift</a>
  ·
  <a href="DESIGN.md">Design</a>
</p>

<p align="center">
  <img src="docs/dashboard-demo.gif" alt="devbrain dashboards — the Board (kanban queue), the Profile (prompt self-portrait), and the Nightshift cockpit" width="860">
</p>

Every prompt is captured to a private, git-synced markdown store, distilled into a
searchable brain, and replayable by any future session or machine. Markdown + git is
the source of truth; everything else is a rebuildable projection.

## How it works

`./setup` wires Claude Code on *this machine*, then gets out of the way:

```
A. Capture    every prompt → raw markdown log      automatic, model-free · source of truth
B. Brain      /distill → gbrain pages + queue       searchable · a rebuildable projection
C. Assemble   /continue → brief + work top task     opens a minimal-MVP PR · /loop to drain
```

- **Capture** — a `UserPromptSubmit` hook logs every prompt verbatim; a `Stop` hook
  adds a one-line trace. Model-free, never blocks. **This log is the source of truth.**
- **Flush** — a 5-min timer commits/pushes the log off-machine (launchd on macOS,
  systemd/cron on Linux).
- **Brain & resume** — `/distill` folds new log into linked `gbrain` pages + queue
  tasks; `/continue` pulls what's relevant, briefs you, and works the top task.
- **Nudge** — a `SessionStart` hook reminds the agent, at the start of each session in
  a tracked repo, to query the brain (`gbrain search`) before answering or asking, and
  to read a surfaced page by its full `<project>/<page>` slug (`gbrain get … --fuzzy`,
  not the bare name) — with live page/task counts. A reminder, not a query: it never
  runs gbrain itself.

Log path `projects/<project>/log/<date>/<worktree>.<session>.md`, keyed by **git
remote** (all worktrees of a repo collapse to one project). **Golden rule:** everything
downstream of the raw log is re-derivable — never lose the log. Design in
[`DESIGN.md`](DESIGN.md).

**System vs. data — only the system ships.** This repo is the system (installer,
hooks, skills). Your prompts + brain live in a *separate* store at `~/devbrain-data`
that you own — `setup` creates it locally; give it a private remote to back up and
sync. The system never holds your data; the data store never holds code.

## Install

**Needs:** [Claude Code](https://claude.ai/code), Git, `jq`, `python3`. `gbrain`
auto-installs if [`bun`](https://bun.sh) is present; an OpenAI key is optional (semantic search).

```bash
git clone --depth 1 https://github.com/TheWeiHu/devbrain.git ~/.claude/skills/devbrain \
  && cd ~/.claude/skills/devbrain && ./setup
```

`./setup` is idempotent and wires only *this machine* (never your working repos). In a
terminal it asks y/n per component; non-interactive runs take the defaults (everything
but `nightshift`). Be explicit with flags — forwarded through `setup`:

```bash
./setup --without flusher,claude-md   # skip those components
./setup --only capture                # just the prompt-capture hook
./setup --with nightshift             # opt into the experimental loop
DEVBRAIN_DATA=~/path ./setup          # store the brain elsewhere (default ~/devbrain-data)
DEVBRAIN_DATA_REMOTE=git@github.com:you/brain.git ./setup   # clone an existing brain
```

Components: `capture` · `response-trace` · `nudge` · `flusher` · `skills` ·
`claude-md` · `nightshift`. Tear down with `scripts/uninstall.sh` (leaves your
data untouched).

Your **data repo** is your own private prompt-log + brain store — `setup` creates a
fresh one at `$DEVBRAIN_DATA` (default `~/devbrain-data`), or clones
`$DEVBRAIN_DATA_REMOTE` if you point it at your own. Keep it **private** (it holds
your prompts). Commits use your git config, or `$DEVBRAIN_GIT_NAME` /
`$DEVBRAIN_GIT_EMAIL` if set.

## Onboard existing history

`setup` offers this on a fresh brain. To run it yourself, `devbrain import` seeds the
data repo from the Claude Code history already on this machine — transcripts (prompts
**and** responses), `~/.claude/history.jsonl`, and Claude's memory store — through the
same rules + secret redaction the live hooks use:

```bash
devbrain import            # DRY RUN by default — prints a per-project manifest
devbrain import --apply    # write it into the data repo
```

Idempotent (skips sessions already captured live). Identity comes from each session's
git remote; unresolved sessions land in `miscellaneous` (add an `--alias` to route them).
It writes the raw **log + memory**; `/distill` (or
`/continue`) per project folds it into searchable brain pages.

## Daily use

| Command | What it does |
|---|---|
| *(automatic)* | every prompt captured; flusher commits/pushes every 5 min |
| **`/distill`** | fold new log → brain pages **and** queue tasks |
| **`/continue`** | resume: brief, then work the top task as a minimal-MVP PR |
| **`/loop /continue`** | keep draining the queue, one MVP PR per task |
| **`/reconcile`** | mark brain facts the live repo contradicts (mark-only; auto-runs ~weekly from `/distill`) |
| `gbrain search` | query the brain from the shell (returns full `<project>/<page>` slugs) |
| `gbrain get "<project>/<page>" --fuzzy` | read a page by its full slug — copy it from search output, don't strip the prefix |
| `devbrain todo list` | see the queue from the shell |
| `devbrain queue` | browser control plane for the queue (view · edit · prioritize · unblock, across projects) |
| `devbrain help` | every devbrain subcommand (todo · queue · import · rebuild · flush · nightshift · version) |

## TODO queue

The brain records *what happened*; the queue records *what's next* — one markdown file
per task under `projects/<project>/todo/`, priority-ranked. `/distill` fills it;
`/continue` drains it (claims the top task → MVP PR). Lifecycle
`open → taken → review → done`; a task isn't `done` until its PR merges (the next
`/distill` detects merges and asks you to confirm). The `devbrain todo` CLI
(`add · list · next · show · claim · review · done · release`) is there if you touch it
by hand. Every devbrain shell tool lives under the one `devbrain` command (`devbrain
help` lists them); the old bare names (`devbrain-todo`, `devbrain-import`, `nightshift`)
still work as back-compat aliases. Details in [`DESIGN.md`](DESIGN.md).

Prefer a UI? `devbrain queue` boots a localhost-only dashboard (the *control plane*):
switch between projects, see every task and its state (incl. `done`/`held`), and run
any mutation — create, edit title/body, reprioritize, change status, add context,
hold/release/approve/done — from the browser. Every action is routed through the same
`devbrain-todo` verbs (no format drift), and a "needs you" section surfaces `held`
tasks that need a human.

```bash
devbrain queue                        # open the dashboard (binds 127.0.0.1:8799)
devbrain queue --no-open --port 9000  # headless: serve only, pick the port
```

## nightshift — drain the queue overnight (experimental, off by default)

nightshift runs several `claude` workers in parallel against the queue, each in its own
worktree, auto-merging green work onto a throwaway `nightshift` branch — you wake to one
`git diff main...nightshift`. Thin layer over `/continue`; nothing else depends on it.

```bash
DEVBRAIN_NIGHTSHIFT=1 ./setup                   # opt in (adds the `devbrain nightshift` subcommand)
devbrain nightshift start ~/nightshift/myrepo   # launch the fleet (runs until stopped)
devbrain nightshift watch                       # live browser dashboard
devbrain nightshift review | devbrain nightshift stop  # parked tasks | stop the fleet
```

Workers run headless (`claude -p`) by default; `--tmux` is a fallback (run `devbrain
nightshift` with no args for the why). You stay the only `nightshift → main` gate.

## gbrain & OpenAI key

The brain lives in **gbrain** (local PGLite). `setup` installs it via bun; capture
works without it — you just can't *query* until it's there. Semantic search needs an
**OpenAI key** (optional; falls back to keyword + graph ranking):

```bash
gbrain config set openai_api_key sk-...   # then: gbrain embed --stale
```

## Layout

```
~/.claude/skills/devbrain/   the system (installer + tooling)
├── setup                    entrypoint (wraps scripts/install.sh); `./setup --version`
├── VERSION · CHANGELOG.md    current release + history (semver; see CHANGELOG "Releasing")
├── scripts/                 devbrain (unified CLI) · install · uninstall · flush · rebuild · todo · import · nightshift*
├── hooks/                   capture · capture-response · capture-memory · capture-gbrain · project-key · devbrain_lib
├── skills/                  continue · distill · nightshift · reconcile
└── DESIGN.md
~/devbrain-data/             the private data repo (source of truth)
└── projects/<project>/
    ├── {log,brain,todo}/
    └── gbrain-queries.log   trace of every gbrain call (PostToolUse hook; retrieval tuning)
```

## Testing

Run the whole suite — every `scripts/test-*.sh` — with one command:

```
make test            # or: bash scripts/test-all.sh
```

It reports PASS/FAIL/SKIP per script and a final summary, and exits non-zero if
any failed (so CI can gate on it). Tests with an unmet external dependency (e.g.
`jq`/`python3` missing, or Docker not running for the clean-room test) are reported
SKIP, not FAIL — a test signals this by printing a line starting with `skip:` and
exiting 0.

## Troubleshooting

- **Prompts not captured** — check `jq .hooks ~/.claude/settings.json` and that `jq`
  is installed (the hook fails open by design).
- **`gbrain not found`** — install the engine, re-run `./setup`.
- **Brain looks stale** — `devbrain rebuild` re-imports every page.
- Re-run `./setup` anytime; it only adds what's missing.
