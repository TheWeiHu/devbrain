# Changelog

All notable changes to devbrain are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

The single source of truth for the current version is the [`VERSION`](VERSION)
file at the repo root. See [Releasing](#releasing) for how a version is cut.

## [Unreleased]

### Added
- **gbrain is now optional — the brain is searchable with zero engine.** A new
  `devbrain brain` router (`hooks/brain.sh`) prefers gbrain when installed (transparent
  passthrough — ranked + semantic search, `--fuzzy` get, all unchanged) and otherwise
  falls back to an offline grep over the on-disk `projects/<project>/brain/*.md` pages for
  `search`/`get`/`list`. The pages were always the source of truth; gbrain is just an index
  over them, so a fresh install with no `bun`/gbrain still has a working, searchable brain.
  `/continue`, `/distill`, and `/reconcile` route their reads/index-writes through it (guarded
  so index-only steps cleanly no-op without an engine); `rebuild-brain.sh` soft-skips instead
  of hard-failing. `setup` still installs gbrain by default (like nightshift) but now via a
  `[Y/n]` prompt so interactive users get a say, plus `--with-gbrain`/`--without-gbrain` flags
  and `DEVBRAIN_GBRAIN=1/0` to decide explicitly (e.g. in CI). devbrain itself has zero runtime
  dependencies — gbrain is an opt-out accelerator, not a hard requirement. Covered by
  `scripts/test-brain.sh`.

- **"Agents In Parallel" dashboard panel** — a Profile chart of how many agent sessions
  ran concurrently over time, across all repos, computed from the existing prompt logs
  (no new telemetry). A session is "live" for 5 min after each prompt; concurrency is
  measured at that resolution and shown in auto-scaled, stacked-by-repo bins (each bar is
  the busiest moment in its bin, so the height is a true "how many at once"). Honors the
  Typed/Bot/All + date filters; hover a column for the per-repo breakdown. Counts prompt
  activity, not live OS processes.

### Changed
- **Most-Called Skills chip cloud hides the ≤2× long tail** — the Profile chip cloud now
  renders a chip only for skills called more than twice; everything called ≤2× folds into
  a dashed, expandable "others · N" chip. Skill detection is a structural match on a leading
  slash (no allowlist), so a typo, a native `/clear`, or a stray pasted path can surface as a
  one-off — collapsing the tail keeps such a false positive from cluttering the cloud until
  you click to expand it.
- **Nightshift merge-retry is now "land the finished work, don't redo it"** — when a
  worker branch can't land (merge conflict or a red gate), the retry prompt reframes
  the task as already-finished work to PRESERVE: fix only the blocker against current
  `origin/nightshift`, never rebuild or re-scope. Workers may now MERGE DIRECTLY — once
  the gate passes locally, merge the `todo/<id>` branch into `nightshift`, push, and
  signal with `devbrain-todo done <id>`. The orchestrator honors that signal (alongside
  the branch-is-ancestor check) and confirms the close instead of re-merging.
- **Queue dashboard project picker** is now activity-ordered: the most-active project
  (most recent task created/done) leads the list and is the default selection instead of
  "all projects", which moves to the very bottom with `miscellaneous` pinned just above it.
  Projects with no open tasks render grayed, and divider rows fence off the three zones
  (active projects · miscellaneous · all).
- **Queue dashboard project picker** now fences its three zones with native `<optgroup>`
  headers instead of full-height dash-separator rows, removing the dead vertical space
  that made the open dropdown look empty above "miscellaneous".

### Removed
- **"How Terse, By Day" Profile chart** — retired.

### Fixed
- **Token cost was inflated ~2–3×.** Claude Code writes one transcript line per content
  block, each repeating the message-level `usage`; both writers summed per line. Now deduped
  by `message.id` (re-harvest corrects history).
- **No-prompt-log turns are now captured.** `capture-response.sh` exited before the token
  write when no prompt was logged, silently dropping nightshift workers; the harvest now runs
  on every `Stop`.
- **`import.py` dedup is now global, not per-project** — a session whose routing changed is
  no longer re-added (double-counted) under a new project.

## [0.4.1] — 2026-06-24

### Added
- **`devbrain uninstall`** — uninstall is now a first-class subcommand, symmetric with the
  rest of the CLI (you install via `npx getdevbrain install`, but everything after is
  `devbrain <verb>`). `npx getdevbrain uninstall` still works for the pre-install / not-on-PATH
  case. Your data repo is always left intact.

### Changed
- **Dashboard opens on the Profile, not the Board** — the self-portrait is the more
  interesting landing view; `#board` in the URL still forces the Board.
- **Profile defaults to the "All" prompt filter, not "Typed"** — show the full picture
  (your prompts + autonomous/nightshift turns) by default; toggle to Typed/Bot as needed.

### Fixed
- **Dashboard project picker splits by open work** — the picker now groups projects under
  "projects" (those with open TODOs) and "other" (no open TODOs), and pulls the
  "miscellaneous" catch-all out of the "other" zone to stand ungrouped alongside "all
  projects". Previously every project sat in one "projects" zone with miscellaneous alone
  under "other".
- **`devbrain: command not found` after install** — the installer symlinks `devbrain`
  into `~/.local/bin`, which isn't on `PATH` by default on macOS, so the command was
  unusable after `npx getdevbrain install` (the installer only printed a NOTE that was
  easy to miss). It now adds `~/.local/bin` to your shell rc (`.zshrc` / `.bash_profile`)
  when it's missing — idempotently, reversed by `uninstall`, and skippable with
  `DEVBRAIN_NO_PATH=1`. Already installed? Run `export PATH="$HOME/.local/bin:$PATH"`
  (add it to your shell rc to persist). Covered by `scripts/test-install-path.sh`.

## [0.4.0] — 2026-06-24

### Fixed
- **Nightshift cost no longer double-counts** — the cumulative Σ-tokens / est.-cost reader
  (`nightshift-status.py`) summed every assistant `usage` line in the worker transcripts,
  but Claude Code replays earlier turns into the JSONL on resume/compaction, so ~56% of
  those lines were duplicates — inflating the headline ~2.3×. The reader now dedups on
  `(message.id, requestId)`, exactly like `ccusage`, so the Σ-cost matches an independent
  count (pricing was never the gap — the table is within ~10% of ccusage's rates).

### Added
- **One-line npm install — `npx getdevbrain install`** — a thin npm front-end
  (`bin/devbrain.js`) maps `install`/`uninstall` to the existing bash entrypoints and
  forwards every other verb to the installed `devbrain` CLI. No new runtime: the installer
  already copies stable copies into `~/.claude`, so the package runs straight from npx's
  cache. Published as `getdevbrain` (npm blocks the bare `devbrain` as too similar to an
  unrelated `dev.brain`); the installed command stays `devbrain`.
- **`/distill` step 3e — retro-ledger merges that shipped without a task** as a judgment
  step (prose, not a CLI verb): list merged PRs whose number isn't on any task, and for
  the substantive gaps mint a closed task by hand (skip releases/chores/pre-queue
  history). Keeps the queue a fuller ledger without minting noise.
- **Profile view in the dashboard** — a prompt self-portrait served live from the same
  localhost server (`/api/prompts`): project focus, weekday×hour rhythm (in the viewer's
  local timezone), tone fingerprint, prompt-length and weekly-terseness charts, plus a
  word-cloud source panel where clicking a word, chart element, or stat chip drills into
  the verbatim prompts behind it.
- **Typed / Bot / All toggle** classified by session origin — nightshift worker sessions
  (worktrees under `~/nightshift`/`~/drain`, named `<project>-w<N>`) are `nightshift`;
  interactive sessions yield `human` prose + `command` slash-commands. Typed = human +
  command, Bot = nightshift + harness.
- **Date-range filter** (7d / 30d / 90d / All + pickers) and a `typed · bot · showing`
  readout, all in the navbar.
- **gbrain "Brain Value" cards** — `/api/gbrain` reads `gbrain-queries.log`; the Profile
  shows the brain's hit rate, the pages it surfaced most, and a cloud of the terms you
  search the brain for (click a term → your prompts that mention it).

### Changed
- **nightshift is now a default component (no longer experimental)** — it installs with
  every `npx getdevbrain install` / `./setup` instead of being opt-in. Installing it only
  ships the `devbrain nightshift` toolset; the fleet still runs ONLY on an explicit
  `devbrain nightshift start`. Opt out with `--without nightshift` or `DEVBRAIN_NIGHTSHIFT=0`.
- **`scripts/release.sh` keeps `package.json` in lockstep with `VERSION`** — the npm
  package version is bumped with each release so it never drifts from the git tag.
- **`scripts/queue-dashboard.html` → `scripts/dashboard.html`** (installed as
  `devbrain-dashboard.html`) — the page is the devbrain control plane (Board + Nightshift
  + Profile), not just the queue. Old names stay as `find_dashboard` fallbacks; the
  pre-rename copy is cleaned up on upgrade.
- **Nightshift monitor stat chips centered** and aligned with the Profile cards.
- **Nightshift monitor sorts running fleets to the top** — stopped/stale runs sink to the
  bottom (stable, so each group stays in server order).
- **Nightshift run registry self-heals** — `nightshift()` prunes phantom registrations a
  crash/kill/reboot left behind (repo deleted, or stopped and no longer refreshing
  `status.json` past a 5-min TTL), so dead "stopped" fleets clear themselves on the next
  poll instead of haunting the dashboard. Running/fresh fleets are always kept.
- **`nightshift` is now reached only as `devbrain nightshift`** — the standalone `nightshift`
  command is no longer put on `PATH`. One namespace, no generic-name collisions; install
  removes the legacy symlink, and uninstall now also drops it plus the `~/.claude/nightshift`
  toolset dir (both previously leaked).

## [0.3.0] — 2026-06-21

### Added
- **`done_at` on TODO tasks** — `devbrain todo done` stamps a UTC completion time, so
  cycle time (created → done) is measurable by `/retro` and the landing report.
- **`scripts/test-nightshift-gate.sh`** — unit tests for the nightshift green-gate.

### Changed
- **nightshift integration branch renamed `staging` → `nightshift`** — workers branch
  off `origin/nightshift` and the orchestrator merges green turns into `nightshift`;
  review with `git diff main...nightshift`. The `--keep-staging` flag is now
  `--keep-nightshift`.
- **Capture biases toward keeping; no per-harness special-casing** — a turn that embeds
  the user's text inside a host wrapper (e.g. a `<system_instruction>` preamble a harness
  prepends to a session's first prompt) is now captured WHOLE instead of being dropped as
  synthetic. `SYNTHETIC_PREFIXES` is trimmed to markers with zero user authorship, and
  identity routing in `import.py` is the git remote only (the same rule as
  `project-key.sh`) with no harness-specific path parsing. The deleted
  basename-against-scanned-clones guessing (and its `--roots`/`--no-gh` flags) is replaced
  by a prompt: a fresh-brain `devbrain import` preview now lists history that landed in
  `miscellaneous` (deleted worktrees with no live remote) and asks the setting-up agent to
  `--alias` the ones it recognizes — judgment by the agent, not heuristics in code.

### Fixed
- **Project identity no longer mints a folder from a local-path origin** — a worktree
  worktree whose `origin` is a filesystem path (e.g. `…/devbrain/<workspace>`) was
  parsed as `<owner>/<repo>`, creating a bogus `<repo>__<workspace>` project folder.
  Local-path / `file://` origins now route to `miscellaneous` like any remote-less repo.

### Fixed — nightshift
- Green-gate picks a `requires-python`-compatible interpreter and fails fast if none works, instead of silently building a venv that can never pass.
- A collection/import error no longer counts as a "red base" that hijacks the whole fleet — only a genuine test failure does.
- Stopping the fleet now reaps in-flight turns and releases their tasks; claims stranded by dead workers get reclaimed.
- Concurrent fleets get their own dashboard port instead of colliding on 8787.
- **`scripts/test-cross-platform-docker.sh`** — Tier 2 clean-room test: spins a fresh
  Linux container (Ubuntu / Amazon Linux / Debian), runs the unit suite under GNU
  coreutils, then a real `./setup` on an empty data repo and asserts hooks install,
  the flusher takes the Linux path, the importer seeds, live capture appends, and a
  re-run is idempotent. Validated green on Ubuntu 22.04 and Amazon Linux 2023.

### Changed
- **The session nudge, installed CLAUDE.md, and README now teach reading a found page,
  not just searching.** The `/continue` skill already taught the trick, but every other
  agent-facing entry point only said `gbrain search` — so outside `/continue`, agents
  found pages then called `gbrain get <bare-page>` (stripping the `<project>/` prefix the
  search output shows), got `page_not_found`, and groped. Trace analysis showed a 0%
  read-back rate across a session that leaned on `get` repeatedly. The `SessionStart`
  nudge (`hooks/session-start-nudge.sh`), the `install.sh` CLAUDE.md block, and the
  README now all state: read a surfaced page by its FULL `<project>/<page>` slug via
  `gbrain get "<project>/<page>" --fuzzy`, never the bare name, and don't pipe reads
  through `2>/dev/null` (it hides gbrain's "Did you mean" fix-hints).
- **`/continue` now teaches reading found pages with `--fuzzy` and visible errors.**
  Trace analysis showed agents repeatedly failing to *read* pages they'd just *found*:
  the brain is one global namespace, so `gbrain get <bare-page>` (without the
  `<owner>__<repo>/` prefix the search output shows) is `page_not_found`, and the
  common `2>/dev/null` pipe hid gbrain's own `use fuzzy` / `Did you mean: …` fix-hints
  — so the failed read looked like an empty page and the agent groped. The skill's
  read steps now use `gbrain get "<owner>__<repo>/<page>" --fuzzy` (which one-shot
  resolves bare/typo'd slugs, or lists candidates) and explicitly drop `2>/dev/null`
  on reads. Fuzzy-first beats a retry loop — agents were re-trying the same failing
  bare slug.

### Fixed
- **Install no longer aborts on a fresh headless Linux box.** The Linux flusher
  step ran the cron-install pipeline under `set -e`, and on a box with no existing
  crontab `crontab -l` exits 1 — aborting the whole install (and skipping first-run
  import) over an optional auto-flush convenience. The systemd→cron→manual fallback
  chain is now best-effort and degrades gracefully.
- **`capture-memory` no longer depends on `cmp`** (diffutils), which is absent on
  minimal Linux (e.g. Amazon Linux 2023). The changed-file check is now shell-native.
- **gbrain call traces no longer misfile to `miscellaneous`** — the
  `capture-gbrain.sh` PostToolUse hook keyed identity off the payload `cwd`, so
  calls an agent made by `cd`-ing into a repo inline (`cd <repo> && gbrain …`, or
  the nightshift `v="<repo>" (cd "$v" && gbrain …)` pattern) from a non-repo
  parent landed under the shared `miscellaneous` bucket instead of the repo they
  actually queried. The hook now routes by the repo the call actually hit, in
  priority order: (1) the `owner__repo` prefix of a result slug in gbrain's own
  output (authoritative when the call returned hits); (2) the inline `cd` target
  (literal paths and `$VAR`/`"$VAR"` references) when it resolves to a hosted
  `<owner>__<repo>` — covers writes and zero-hit reads, which surface no slug;
  otherwise the payload cwd stands. `$DEVBRAIN_PROJECT` still overrides all.

## [0.2.0] — 2026-06-18

### Added
- **`nudge` component (SessionStart hook)** — at the start of each session in a
  tracked repo, injects one tiny, project-specific line reminding the agent to
  query the brain (`gbrain search`) before answering, asking, or starting work,
  with live brain-page/open-task counts. A reminder, not a query: never runs
  gbrain itself (no latency/cost/stale injection); real hosted projects only;
  silent when there's no brain to consult; fail-open. On by default.
- `scripts/release.sh --push` now also publishes a **GitHub Release**
  (`gh release create`) from the new CHANGELOG section, so a release is one
  command end-to-end; `--no-release` opts out (tag only).
- The release cutter is the maintainer script `scripts/release.sh` (run from a
  checkout) — no longer installed as a `devbrain release` subcommand, since it
  releases the devbrain *project*, not anything an installed user would run.

## [0.1.0] — 2026-06-18

First versioned release. Establishes devbrain's two-stage design — raw capture
(Stage A) feeding a curated, queryable brain (Stage B) — and the install/skill
machinery around it.

### Added
- **Unified `devbrain` command** — one dispatcher with subcommands (`todo`,
  `import`, `rebuild`, `flush`, `nightshift`, `release`, `version`, `help`);
  legacy names (`devbrain-todo`, `devbrain-import`, `nightshift`) keep working as
  back-compat aliases.
- **Release tooling** — `scripts/release.sh` cuts a version in one command: bump
  `VERSION`, roll the `[Unreleased]` notes into a dated section, commit, tag `vX.Y.Z`.
- **Versioning** — a `VERSION` file (semver source of truth) + this CHANGELOG;
  `./setup --version` and `devbrain version`.
- **Open-source-ready install** — no hardcoded personal defaults: data-repo
  remote configurable via `DEVBRAIN_DATA_REMOTE`, commit identity from your git
  config or `DEVBRAIN_GIT_NAME` / `DEVBRAIN_GIT_EMAIL` (impersonal
  `devbrain@localhost` fallback).
- **Prompt + response capture.** `UserPromptSubmit` → `capture.sh` logs every
  prompt; `Stop` → `capture-response.sh` logs a head/middle/tail-sampled recap of
  the model's final message plus a `touched:`/`tools:` trace. Append-only Markdown
  under `projects/<owner>__<repo>/log/`.
- **Memory capture.** Claude Code's `~/.claude/projects/*/memory/*.md` notes are
  mirrored into the data repo as a third capture stream.
- **`devbrain import`.** One-time backfill of historical Claude Code transcripts
  into the data repo, with a confidence-tiered identity-resolution cascade
  (live `git remote` → disk-scan of clones → `gh` fallback → `miscellaneous`),
  dry-run by default and per-project opt-out.
- **gbrain integration.** Brain pages are loaded into gbrain with per-project
  slug namespacing (`<owner>__<repo>/<topic>`) and tags; semantic query with a
  keyless keyword/graph fallback. Every brain query is logged to
  `projects/<project>/gbrain-queries.log`.
- **Skills.** `/distill` (curate new log → brain pages + TODO tasks),
  `/continue` (fold in, pull brain context, then work the top task as a minimal
  MVP PR), and `/reconcile` (mark brain facts contradicted by the repo;
  auto-runs at most weekly from `/distill`).
- **TODO queue.** `devbrain-todo` (`add`/`list`/`next`/`show`/`claim`/`review`/
  `done`/`release`) — born from `/distill`, drained by `/continue`, with a
  `review` status for PRs awaiting merge.
- **nightshift** (experimental, opt-in). Autonomous overnight loop that drains
  the queue with parallel workers in git worktrees, gated-serialized into a
  disposable `staging` branch.
- **Install/setup.** `./setup` front-end over `scripts/install.sh` (capture
  hooks, launchd flusher, skills, `settings.json`, CLAUDE.md block); idempotent
  and reversible via `scripts/uninstall.sh`.
- **Secret redaction** in `capture.sh` before anything is written.
- **MIT License.**

### Fixed
- Collision-resistant project identity via `<owner>__<repo>` keys and per-folder
  `.identity` files (replacing basename-only routing).
- `install.sh` no longer strips the exec bit off pinned hooks.
- `install.sh` in-place `sed` edits made portable across BSD and GNU.

## Releasing

devbrain is tagged from `main`, on **no fixed calendar and not per-merge** (that's
too noisy) — a version is cut on judgment when a coherent batch has landed and is
worth marking. Reasonable triggers:

- a user-facing capability lands (new subcommand, skill, hook) → **minor** (`0.X.0`)
- a batch of fixes/docs accumulates → **patch** (`0.0.X`)
- before you share the repo, onboard someone, or announce — so they install a
  known-good tag, not a moving `main`
- after a change to install / hooks / data layout — so users can pin or roll back
- **`1.0.0`** once the install contract + data layout are stable enough to promise
  backward-compatibility

To cut one, run the maintainer script on a clean `main` checkout — it rolls the
`[Unreleased]` notes into a dated `[X.Y.Z]` section, bumps [`VERSION`](VERSION),
commits, and creates the annotated `vX.Y.Z` tag:

```sh
./scripts/release.sh minor          # or: patch · major · an explicit X.Y.Z
./scripts/release.sh minor --push   # push commit + tag AND publish a GitHub Release
./scripts/release.sh minor -n       # dry-run: show the diff, change nothing
```

(`scripts/release.sh` releases the devbrain *project* — it's a repo-checkout tool,
not an installed `devbrain` subcommand.) Without `--push` it stops after the local
commit + tag and prints the push command.
With `--push` it also runs `gh release create` from the new CHANGELOG section
(`--no-release` skips that); both skip gracefully if `gh` is unavailable.

`VERSION` is the machine-readable source of truth; the git tag (`vX.Y.Z`) is the
immutable marker. Keep them in lockstep.

[Unreleased]: https://github.com/TheWeiHu/devbrain/compare/v0.4.1...HEAD
[0.4.1]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.4.1
[0.4.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.4.0
[0.3.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.3.0
[0.2.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.2.0
[0.1.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.1.0
