# Changelog

Notable changes to devbrain. Headlines only — full detail is in the git history
and the [GitHub Releases](https://github.com/TheWeiHu/devbrain/releases). Format
loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) +
[SemVer](https://semver.org/spec/v2.0.0.html). [`VERSION`](VERSION) is the source
of truth for the current version.

## [Unreleased]

- **The runtime is a single Go binary.** The capture hooks, event shim, redaction,
  recap summarizer, todo queue, brain fallback, flusher, importer, dashboard server,
  install/uninstall, and the whole nightshift subsystem — all one static `devbrain`
  binary, installable with Homebrew. On-disk formats are unchanged and pinned
  byte-for-byte by goldens captured from the retired bash/python implementation
  (`testdata/golden/`), now the immutable behavioral spec. `devbrain install` replaces
  `./setup`; runtime dependencies drop to git alone.
- **Token accounting is accurate.** Subagent turns are captured (`SubagentStop`); import
  re-derives token rows from on-disk transcripts; a message's usage is counted from its
  largest snapshot; repeated Stop-hook captures are deduped by stable turn identity.
  `devbrain import --tokens-only --apply` heals historical over-counts.
- **Dashboard** surfaces cache-read cost (share-over-time + $/turn scatter) and per-turn
  response recaps.
- npm distribution removed — devbrain ships via Homebrew, `go install`, and release
  tarballs only. Python string-semantics helpers consolidated into `internal/pytext`.

## [0.5.1] — 2026-06-29
- Preferences edit history logs context-free (`n=0`) diffs, so every logged line is a real change.

## [0.5.0] — 2026-06-29
- `/distill` learns your durable preferences into `preferences/global.md` and wires them
  into Claude Code via `@import`; the page is dashboard-editable and converges instead of growing.
- gbrain is now optional — an offline grep over the on-disk brain pages backs `devbrain brain`.
- Dashboard Profile: agents-in-parallel panel, real skill-call counting, shared `/api/pricing`.
- Nightshift merge-retry/green-gate hardening; clean-room npm-tarball install test.

## [0.4.1] — 2026-06-24
- `devbrain uninstall` subcommand; dashboard lands on the Profile; `~/.local/bin` PATH fix.

## [0.4.0] — 2026-06-24
- One-line `npx getdevbrain install`; live Profile dashboard view; Typed/Bot/All + date filters;
  gbrain "Brain Value" cards; nightshift becomes a default (opt-out) component; nightshift cost dedup fix.

## [0.3.0] — 2026-06-21
- Nightshift integration branch renamed `staging` → `nightshift`; capture biases toward keeping
  (no per-harness special-casing); cross-platform clean-room test; read-a-found-page gbrain
  guidance; assorted headless-Linux install fixes.

## [0.2.0] — 2026-06-18
- `nudge` SessionStart hook (query-the-brain reminder); `release.sh` publishes GitHub Releases.

## [0.1.0] — 2026-06-18
First versioned release: unified `devbrain` command; prompt/response/memory capture; `import`
backfill; gbrain integration; skills (`/distill`, `/continue`, `/reconcile`); the TODO queue;
nightshift (experimental); install/uninstall; secret redaction; MIT license.

## Releasing

devbrain is tagged from `main` on judgment, not per-merge — cut a version when a coherent batch
has landed (minor for new user-facing capability, patch for fix/doc batches), before sharing or
onboarding, and after any install/data-layout change. `1.0.0` marks a stable install + data
contract. Keep [`VERSION`](VERSION) and the `vX.Y.Z` tag in lockstep.

[Unreleased]: https://github.com/TheWeiHu/devbrain/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.5.1
[0.5.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.5.0
[0.4.1]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.4.1
[0.4.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.4.0
[0.3.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.3.0
[0.2.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.2.0
[0.1.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.1.0
