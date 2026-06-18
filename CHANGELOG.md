# Changelog

All notable changes to devbrain are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

The single source of truth for the current version is the [`VERSION`](VERSION)
file at the repo root. See [Releasing](#releasing) for how a version is cut.

## [Unreleased]

### Added
- `devbrain release --push` now also publishes a **GitHub Release**
  (`gh release create`) from the new CHANGELOG section, so a release is one
  command end-to-end; `--no-release` opts out (tag only).

## [0.1.0] — 2026-06-18

First versioned release. Establishes devbrain's two-stage design — raw capture
(Stage A) feeding a curated, queryable brain (Stage B) — and the install/skill
machinery around it.

### Added
- **Unified `devbrain` command** — one dispatcher with subcommands (`todo`,
  `import`, `rebuild`, `flush`, `nightshift`, `release`, `version`, `help`);
  legacy names (`devbrain-todo`, `devbrain-import`, `nightshift`) keep working as
  back-compat aliases.
- **`devbrain release`** — one-command version cut: bump `VERSION`, roll the
  `[Unreleased]` notes into a dated section, commit, and tag `vX.Y.Z`.
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

To cut one, run the helper on a clean `main` checkout — it rolls the `[Unreleased]`
notes into a dated `[X.Y.Z]` section, bumps [`VERSION`](VERSION), commits, and
creates the annotated `vX.Y.Z` tag:

```sh
devbrain release minor          # or: patch · major · an explicit X.Y.Z
devbrain release minor --push   # push commit + tag AND publish a GitHub Release
devbrain release minor -n       # dry-run: show the diff, change nothing
```

Without `--push` it stops after the local commit + tag and prints the push command.
With `--push` it also runs `gh release create` from the new CHANGELOG section
(`--no-release` skips that); both skip gracefully if `gh` is unavailable.

`VERSION` is the machine-readable source of truth; the git tag (`vX.Y.Z`) is the
immutable marker. Keep them in lockstep.

[Unreleased]: https://github.com/TheWeiHu/devbrain/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.1.0
