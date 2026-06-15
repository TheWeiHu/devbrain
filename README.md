# devbrain

Turn the prompts you write — in *any* repo — into a durable, queryable brain any
agent can resume from. **The log is the agent.**

devbrain captures every prompt to a private, git-synced store, distills it into a
searchable brain, and lets any future session (or machine) pick up where you left
off. Markdown + git is the source of truth; everything else is a rebuildable
projection.

It ships as **two repos**: this **system** repo (installer + tooling, no personal
content) and a separate **private** `devbrain-data` repo for your logs and pages.
System never holds data; data never holds code.

## Install

**Needs:** [Claude Code](https://claude.ai/code), Git, `jq`, `python3`. The
[`gbrain`](#gbrain--openai-key) engine auto-installs if [`bun`](https://bun.sh) is
present; an OpenAI key is optional (it unlocks semantic search).

```bash
git clone --depth 1 https://github.com/TheWeiHu/devbrain.git ~/.claude/skills/devbrain \
  && cd ~/.claude/skills/devbrain && ./setup
```

`./setup` is idempotent and wires *this machine* (never your working repos): the
capture hooks, the `/continue` and `/distill` skills, a launchd flusher that
commits/pushes the data repo every 5 min, and a standing line in
`~/.claude/CLAUDE.md`. Tear down with `scripts/uninstall.sh` — your data is left
untouched.

The brain lives in `~/devbrain-data` by default. To put it elsewhere — or to clone
an existing brain — set the path up front (works in every context, including when
the command is run by Claude Code or CI):

```bash
DEVBRAIN_DATA=~/path ./setup                               # store the brain elsewhere
DEVBRAIN_DATA_REMOTE=git@github.com:you/brain.git ./setup  # clone an existing brain
```

(Run directly in a terminal, `setup` will also *prompt* for the path; that prompt is
skipped in non-interactive runs — agent/CI/pipe — which just take the default.)

To back up / sync across machines, give the data repo a private remote:
`git -C ~/devbrain-data remote add origin <url>`.

## How it works

```
A. Capture    every prompt → raw markdown log         automatic, model-free · source of truth
B. Brain      /distill folds the log → gbrain pages   searchable · a rebuildable projection
C. Assemble   /continue → a short briefing to resume  pulls only what's relevant
```

- **Capture** — a `UserPromptSubmit` hook appends each prompt verbatim to
  `projects/<project>/log/<date>/<worktree>.<session>.md` (routed by git remote,
  never by topic); a `Stop` hook adds a one-line trace. Never blocks your turn.
- **Brain** — `/distill` curates new log entries into linked, tagged pages in
  gbrain (keyword + graph + optional semantic search, over MCP). Every fact keeps
  provenance back to the log.
- **Assemble** — `/continue` pulls the relevant brain, refreshes the live world
  (git / issues / CI), and briefs you — subtraction, not context-stuffing.

**Golden rule:** everything downstream of the raw log is re-derivable — never lose
the log. Full design in [`DESIGN.md`](DESIGN.md).

## Daily use

| Command | What it does |
|---|---|
| *(automatic)* | Every prompt is captured; a flusher commits/pushes every 5 min. |
| **`/continue`** | Resume: fold in new log → pull brain → refresh world → briefing. |
| **`/distill`** | Checkpoint new log into brain pages (writes directly; review by git diff). |
| `gbrain search "<q>"` | Query the brain from the shell. |

## gbrain & OpenAI key

The brain lives in **gbrain** (local PGLite by default). `setup` installs it via bun
and initializes a local brain; if bun is missing it prints the one command to run.
Capture works without gbrain — you just can't *query* until it's installed.

Semantic search needs an **OpenAI key** (optional). Without one, search falls back
to keyword + graph ranking (still useful). Add it and backfill embeddings:

```bash
gbrain config set openai_api_key sk-...   # or: export OPENAI_API_KEY=sk-...
gbrain embed --stale
```

## Layout

```
~/.claude/skills/devbrain/      this system repo (installer + tooling)
├── setup                       entrypoint (wraps scripts/install.sh)
├── scripts/                    install · uninstall · flush · rebuild · plist
├── hooks/                      capture · capture-response  (→ ~/.claude/hooks)
├── skills/{continue,distill}/  resume + checkpoint skills
└── DESIGN.md · CONTINUE.md

~/devbrain-data/                the private data repo (source of truth)
└── projects/<project>/{log,brain}/
```

The two repos are separate on purpose: the brain spans every project, the wiring
lives at the machine level, and your working repos (including OSS ones) stay clean.
The data home defaults to `~/devbrain-data` (override with `DEVBRAIN_DATA`).

## Troubleshooting

- **Prompts not captured** — check the hook is registered
  (`jq .hooks ~/.claude/settings.json`) and `jq` is installed; the hook fails open
  by design.
- **`gbrain not found`** — install the engine and re-run `./setup`.
- **Brain looks stale** — `~/.claude/hooks/devbrain-rebuild.sh` re-imports every page.
- Re-run `./setup` anytime; it only adds what's missing.
