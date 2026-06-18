# devbrain

Turn the prompts you write — in *any* repo — into a durable, queryable brain any
agent can resume from. **The log is the agent.**

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

Components: `capture` · `response-trace` · `flusher` · `skills` · `claude-md` ·
`nightshift`. Tear down with `scripts/uninstall.sh` (leaves your data untouched).

## Onboard existing history

`setup` offers this on a fresh brain. To run it yourself, `devbrain-import` seeds the
data repo from the Claude Code history already on this machine — transcripts (prompts
**and** responses), `~/.claude/history.jsonl`, and Claude's memory store — through the
same rules + secret redaction the live hooks use:

```bash
devbrain-import            # DRY RUN by default — prints a per-project manifest
devbrain-import --apply    # write it into the data repo
```

Idempotent (skips sessions already captured live) and recovers project identity even
for deleted Conductor worktrees. It writes the raw **log + memory**; `/distill` (or
`/continue`) per project folds it into searchable brain pages.

## Daily use

| Command | What it does |
|---|---|
| *(automatic)* | every prompt captured; flusher commits/pushes every 5 min |
| **`/distill`** | fold new log → brain pages **and** queue tasks |
| **`/continue`** | resume: brief, then work the top task as a minimal-MVP PR |
| **`/loop /continue`** | keep draining the queue, one MVP PR per task |
| **`/reconcile`** | mark brain facts the live repo contradicts (mark-only; auto-runs ~weekly from `/distill`) |
| `gbrain search "<q>"` | query the brain from the shell |
| `devbrain-todo list` | see the queue from the shell |

## TODO queue

The brain records *what happened*; the queue records *what's next* — one markdown file
per task under `projects/<project>/todo/`, priority-ranked. `/distill` fills it;
`/continue` drains it (claims the top task → MVP PR). Lifecycle
`open → taken → review → done`; a task isn't `done` until its PR merges (the next
`/distill` detects merges and asks you to confirm). The `devbrain-todo` CLI
(`add · list · next · show · claim · review · done · release`) is there if you touch it
by hand. Details in [`DESIGN.md`](DESIGN.md).

## nightshift — drain the queue overnight (experimental, off by default)

nightshift runs several `claude` workers in parallel against the queue, each in its own
worktree, auto-merging green work onto a throwaway `staging` branch — you wake to one
`git diff main...staging`. Thin layer over `/continue`; nothing else depends on it.

```bash
DEVBRAIN_NIGHTSHIFT=1 ./setup          # opt in (puts `nightshift` on PATH)
nightshift start ~/nightshift/myrepo   # launch the fleet (runs until stopped)
nightshift watch                       # live browser dashboard
nightshift review  |  nightshift stop  # parked tasks  |  stop the fleet
```

Workers run headless (`claude -p`) by default; `--tmux` is a fallback (run `nightshift`
with no args for the why). You stay the only `staging → main` gate.

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
├── setup                    entrypoint (wraps scripts/install.sh)
├── scripts/                 install · uninstall · flush · rebuild · todo · import · nightshift*
├── hooks/                   capture · capture-response · capture-memory · project-key · devbrain_lib
├── skills/                  continue · distill · nightshift · reconcile
└── DESIGN.md
~/devbrain-data/             the private data repo (source of truth)
└── projects/<project>/{log,brain,todo}/
```

## Troubleshooting

- **Prompts not captured** — check `jq .hooks ~/.claude/settings.json` and that `jq`
  is installed (the hook fails open by design).
- **`gbrain not found`** — install the engine, re-run `./setup`.
- **Brain looks stale** — `~/.claude/hooks/devbrain-rebuild.sh` re-imports every page.
- Re-run `./setup` anytime; it only adds what's missing.
