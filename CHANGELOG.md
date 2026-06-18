# Changelog

All notable changes to devbrain are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

The single source of truth for the current version is the [`VERSION`](VERSION)
file at the repo root. See [Releasing](#releasing) for how a version is cut.

## [Unreleased]

### Fixed
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

[Unreleased]: https://github.com/TheWeiHu/devbrain/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.2.0
[0.1.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.1.0
