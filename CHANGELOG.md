# Changelog

All notable changes to devbrain are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

The single source of truth for the current version is the [`VERSION`](VERSION)
file at the repo root. See [Releasing](#releasing) for how a version is cut.

## [Unreleased]

### Changed
- **The runtime is a single Go binary.** Everything that was bash + Python — the capture
  hooks, the event shim, redaction, the recap/sample summarizer, the todo queue, the brain
  fallback, the flusher, the importer, the dashboard server, install/uninstall, and the whole
  nightshift subsystem (orchestrator, both backends, status emitter) — now lives in one static
  `devbrain` binary, installable with Homebrew. On-disk data formats are unchanged and pinned
  byte-for-byte by goldens captured from the previous implementation; the dashboard HTML is
  embedded unmodified. `devbrain install` replaces `./setup`: it writes
  `~/.config/devbrain/config.json` instead of sed-pinning script copies, registers hooks as
  `<binary> hook <event>` commands, extracts the (embedded) skills, and auto-migrates any
  existing script-based install. The npm package becomes a thin forwarder. Runtime
  dependencies drop to git alone — python3, bash, and Node are no longer required.
  The legacy bash/python implementations have been removed from the tree; the goldens
  captured from them (`testdata/golden/`) are now the immutable behavioral spec.

### Added
- **Subagent token usage is captured.** Subagent transcripts are separate
  `subagents/agent-*.jsonl` files the Stop hook never reads, so their usage was invisible —
  a real under-count on fan-out-heavy days. A new `SubagentStop` hook writes each finished
  subagent turn to the token sidecar (billed to the parent session, like ccusage), and
  `devbrain import` backfills subagent turns from transcripts on disk. Rows carry an
  agent-prefixed `turn` key so live captures and backfills dedup against each other.

### Changed
- **Import re-derives token rows for sessions whose transcript is still on disk.** The
  transcript is authoritative: a session's existing sidecar rows are replaced by freshly
  computed per-turn rows on every `--apply`, which heals partial Stop-hook captures, rows
  predating the `turn` key, and rows stranded under a stale route (they move to the current
  route). Sessions whose transcripts were pruned keep their rows untouched. Running
  `devbrain import --tokens-only --apply` once after upgrading is the recommended way to
  correct historical over-counts.

### Added
- **Cache-read cost is visible in the dashboard, two ways.** The Profile card's Cost view gains
  *Cache-Read Share · Over Time* — a 100%-normalized area chart of `cache_read` as a share of each
  day's total spend (absolute daily magnitude already lives in the Cost Over Time panel above) —
  and *Cache-Read $ / Turn · By Session*, a scatter (x = turns, y = cache-read $/turn, one dot per
  session of ≥5 turns, colored by project) where a session re-reading a big cached prefix every
  turn sits top-right — high per-turn cost regardless of length, the signal total cache-read $
  couldn't flag. A shaded danger corner + an "efficient" hint make the axes self-reading. Clicking
  a dot opens that session's turns in the right-hand panel — each prompt with its one-line response
  recap — so a dot in the danger corner is explainable. `cache_read` is ~99% of token volume but
  cheap (~0.1× input). The `/api/tokens` records carry a `session` field for the grouping, and
  `/api/prompts` records now carry the per-turn `r` (recap), shown on every prompt card.

### Changed
- **The nightshift queue env can no longer leak into anything the orchestrator spawns.** The
  orchestrator used to `export` its operational queue env process-wide — `DEVBRAIN_TODO_DERIVE_GIT`
  always, `DEVBRAIN_TODO_ONLY` in fixed-set runs — so every child inherited it, and the green-gate's
  test suite was the recurring victim: the leaked fence/derive env broke tests that build their own
  throwaway queues, false-REDing the gate and deadlocking every merge (#164, then #169 for the second
  variable — each patched one leak at the call site). The exports are gone. All ~30 scattered
  `( cd "$BASE" && "$TODO" … )` queue calls now go through three wrappers that apply the env at
  exactly the calls that need it — `todo` (fleet view: fixed-set scoped, git-derived), `todo_all`
  (whole queue, for fence management), `todo_stored` (stored status, reconcile's input) — and
  workers get the same env deliberately, passed per-turn at launch (`env …` for headless
  `claude -p`, exported inside the tmux session for `--tmux`), instead of inherited by accident.
  The gate's `env -u` scrub stays as defense-in-depth against a launching shell that carries the
  vars. Same behavior for every flag and both backends; `test-nightshift-guards.sh` now pins that
  a fixed-set boot exports neither variable while the wrappers still scope correctly.
- **Codex skill calls now count on the dashboard.** A Codex skill run (`$distill`, `$continue`,
  `$work`, `$reconcile`, and any other skill) injects the SKILL.md as a role=user `<skill><name>…`
  response_item — Codex's equivalent of Claude's `Skill` tool_use. `_codex_details` now reads that
  marker and records `Skill:<name>` in the turn's `tools:` meta, so the Profile "Skills Called"
  charts count Codex invocations the same as Claude's. The dashboard's leading-token fallback also
  matches a `$`-prefix (not just `/`), so a typed `$distill` still counts on a turn with no response
  meta. Retrospectively, `import.py` now harvests Codex **logs** (prompt + response + tools), not
  just token records — Codex sessions were never captured live, so their prompts and skill calls
  lived only in `~/.codex/sessions` transcripts until now.
- **The dashboard's model-pricing table is no longer a second copy that drifts.** The Profile
  card's cost view used to carry its own JS `PRICE` literal (plus tier fallbacks) duplicating
  Python's `model_pricing.py`; the two were maintained by hand and would drift. The dashboard
  now fetches the table from `model_pricing.py` via a new `/api/pricing` endpoint on the queue
  server, so there is one pricing source shared by the nightshift monitor and the dashboard.
  Opus rates remain a last-ditch in-JS fallback if the fetch fails.
- **devbrain skills now install natively for Codex too.** The `skills` installer component
  copies the same `skills/*` workflows into Codex's user skill directory
  (`~/.agents/skills`) alongside Claude Code's `~/.claude/skills`, so Codex can invoke them
  as `$distill`, `$continue`, `$work`, and `$reconcile` from any repo. The npm tarball
  install test now asserts those Codex skill copies land, and uninstall removes them.
- **The nightshift orchestrator's worker state is dramatically simpler.** The per-worker
  `STATE[]` array (5 labels: `booting/idle/working/assigned/parked`) was write-only except a
  single read, so it's gone: idle-vs-running is read from the live signal each backend already
  has (headless → the turn PID; tmux → the marker + pane), and the tmux resend path keeps one
  `PENDING` bit instead. The assignment decision tree and the turn-harvest block — previously
  duplicated across the headless and tmux backends — are now one shared `pick_turn()` and
  `harvest_branch()` each (the latter including the empty-turn release and the one-worker-per-open-task
  assignment cap), so the two backends can't drift. No behavior change; all functionality (both
  backends, every fleet flag) is preserved, and `test-nightshift-policy.sh` pins the shared policy.

### Fixed
- **Output tokens no longer under-count ~35%.** A message's usage snapshot is repeated on
  every content-block transcript line and GROWS as output streams; the per-message dedup
  kept the FIRST snapshot, so multi-block responses recorded only their first block's
  output tokens (input/cache fields are set at request start and were unaffected). Each
  usage field now takes the per-message max, matching ccusage.
- **Token cost no longer over-counts turns the Stop hook captured more than once.** A
  still-growing turn can be captured repeatedly (each record is the turn's cumulative usage
  under a new last-assistant timestamp), and the dashboard's `(session, ts)` dedup kept every
  capture — inflating cache-read-heavy days by ~15-20% vs ccusage. Sidecar records now carry a
  stable `turn` key (the turn's user-prompt timestamp, stamped identically by live capture and
  `devbrain import`), and the reader dedups on `(session, turn)`, keeping only the latest —
  complete — capture. Records written before the field existed keep the old behavior.
- **Backfill no longer freezes a partially-captured multi-day session at its one live day.** `import.py`
  gated its prompt-log harvest on the session UUID: if a session had *any* live log, the whole session
  was treated as captured and its transcript was never re-read. But devbrain stores one log per session-
  *day*, so a session that ran across several days and was only captured live for one of them had its
  other days skipped forever — even with the transcript sitting on disk. The token harvest isn't gated,
  so those sessions showed complete cost but a fraction of their prompts (a real case: 108 token-turns,
  5 logged prompts across 4 days). The guard is now keyed on `(session, day)`: import backfills the days
  that have no live log and leaves the live days untouched, so a plain `import.py --apply` recovers the
  missing prompts from on-disk transcripts.
- **The Codex install now actually makes its hooks fire.** Codex 0.138+ gates hook execution
  behind a `hooks` feature flag that is **off by default**, so registering `~/.codex/hooks.json`
  alone did nothing — the capture hooks never ran and Codex never even prompted to trust them
  (every Codex log to date was a backfill, not live capture). `install.sh` now runs
  `codex features enable hooks` (TOML-safe + idempotent; backs up `config.toml` first) after
  registering the hooks, degrading to a printed manual step if `codex` is absent or too old to
  have the subcommand. A mock-`codex` test (`test-codex-hooks-feature.sh`) covers the enable,
  the graceful fallback, and the backup.
- **Historical prompt logs no longer hand a reader a ~2.85× inflated token count.** The
  2026-06-25 dedup fix (per-content-block double-count) corrected the `tokens.jsonl` sidecar
  and re-derived history, but the inline `tokens:` meta lines in the pre-fix prompt logs were
  left untouched and nothing on them said the deduped source is `tokens.jsonl` — so an analyst
  (human or agent) skimming the logs grabs a number that is ~2.85× too high. Two-part fix: (1)
  the log header now carries a cost caveat pointing at `tokens.jsonl` as authoritative, on
  every new log; (2) `scripts/annotate-token-logs.py` marks each pre-fix inline `tokens:` line
  with `⚠ pre-dedup (see tokens.jsonl)` in place (idempotent, dry-run by default, `--apply` to
  write). It marks rather than rewrites: the sidecar's timestamps drifted through the history
  re-derive, so a per-line join back to the deduped value is unreliable, and a marker on the
  number itself is the honest, safe fix.
- **A fixed-set nightshift run no longer idles forever on a stuck `review` task.** When a
  selected task's work merged into `nightshift` but its status never advanced to `done` and
  its `todo/<id>` branch was already pruned, it sat `review` permanently — and fixed-set
  wind-down counts `review`, so the whole fleet kept 8 idle workers alive (and pinned the
  dashboard's Nightshift tab) indefinitely. `reconcile()` now also heals these branchless
  orphans: it detects the merge in `nightshift`'s history (which survives the branch deletion)
  and marks the task `done`, so wind-down fires and the dashboard's done total is correct.

## [0.5.1] — 2026-06-29

### Fixed
- **The preferences edit history records context-free diffs.** A dashboard save logged its diff
  with `difflib`'s default 3 lines of context, so deleting one bullet showed that removal plus
  three unrelated unchanged lines — noise for a human, and worse, the `/distill` agent reading
  `preferences/edits.md` to find what you deleted could mistake a context line for part of the
  edit. Now built with `n=0` (zero context, matching the `/distill` side's `diff -U0`): every
  line in an entry is a real change (`-` removed / `+` added), nothing unchanged to misread.

## [0.5.0] — 2026-06-29

### Fixed
- **The Global Preferences "Done" button now saves.** The Profile-tab editor had a separate
  Save button plus an Edit/Done toggle, where "Done" only exited edit mode and re-rendered the
  preview from the textarea — it never persisted, so an edit followed by Done was silently
  discarded (it looked saved because the preview updated). Collapsed to one button: Edit opens
  the editor, **Done saves** (`saved · N bytes`) and closes, staying open on a failed save so
  the edit isn't lost. The separate Save button is gone.

### Added
- **Clean-room test that installs from the actual npm tarball, not the repo checkout.**
  Every other install test runs `scripts/install.sh` from the working tree, which contains
  every file — so a runtime reference to anything the published package omits (e.g. a path
  dropped by a `!scripts/...` rule in package.json `files`) would pass the suite yet break a
  real `npx getdevbrain install`. `scripts/test-npm-pack.sh` closes that gap: it builds the
  real tarball with `npm pack`, asserts the archive ships every file the installer copies at
  runtime (and still excludes `test-*` / `release.sh`), then installs **from the extracted
  package** into a throwaway `$HOME` and checks the hooks, CLI, skills, and nightshift toolset
  all land. A missing shipped file now fails CI instead of a user's terminal. Hermetic and
  network-free (`--without flusher,git-gate`, `DEVBRAIN_NO_PATH/NO_IMPORT`); skips if `npm` is
  absent.
- **`/distill` learns your preferences and wires them into Claude Code via `@import`.**
  Distill maintains a global preferences page at `preferences/global.md` in the data
  repo — your durable, repeated steers (design taste, scope, "don't regress",
  staging-not-prod, cost defaults), with per-project `## <project>` subsections — and
  ensures your user memory (`~/.claude/CLAUDE.md`) `@import`s it, so Claude Code injects
  those defaults as standing context in every project. The preferences refresh runs in the
  daily maintenance window alongside the brain reconcile, gated by the date of its newest
  refresh in the edit history — so the shared page refreshes **at most once a day no matter
  how many projects you distill in**, and never churns when `/distill` fires often via
  `/continue` or nightshift.
  The page is also **viewable and editable from the dashboard** (Profile tab → Global
  Preferences, rendered markdown with an Edit toggle) via a new `/api/preferences` GET/POST,
  so you can curate it by hand without finding the file. **Your hand-edits are authoritative:**
  every save appends a diff entry to `preferences/edits.md` (a readable history of who changed
  what — `· you` vs `· distill`), and `/distill` **consolidates** — it preserves your edits
  verbatim, folds a recurring steer into the bullet it already wrote (or skips it) rather than
  appending a near-duplicate, and because it can *see* your deletions in that history it never
  re-adds a rule you deliberately removed. The brain becomes the single
  source of truth for your preferences instead of a hand-maintained file. The import line
  lives in user memory (your home dir, never in a repo) — **nothing is committed**, and it
  doesn't rely on the deprecated `CLAUDE.local.md`. The helper
  (`scripts/link-preferences.sh`, wired at install and re-ensured each distill) is
  idempotent, preserves all other memory content, and no-ops on a not-yet-created page;
  `--unlink` (run by `devbrain uninstall`) removes it cleanly.
- **Install opens the dashboard automatically.** After a successful `./setup`
  (or `npx getdevbrain install`), devbrain launches the browser control plane
  (`devbrain queue` — Board · Nightshift · Profile) detached on
  `http://127.0.0.1:8799/` and pops your browser, so a fresh install has a face
  on day one instead of being an invisible set of hooks. It fires when the install
  is interactive (a real terminal) or driven by npm (the `npx` front-end exports
  `DEVBRAIN_FROM_NPM=1`), and stays quiet in headless/CI where there's no browser.
  Override any run with `--open` / `--no-open` or `DEVBRAIN_OPEN_DASHBOARD=1/0`.
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
- **Generated data files are no longer hidden, and the preferences bookkeeping collapsed to
  one readable log.** Everything devbrain writes into the data repo is now a plain, visible
  file. The preferences page used to be flanked by three opaque dotfile ledgers
  (`.edits.log` hashes, a `.known-steers` key list, a `.distilled` date stamp); those are
  replaced by a single human-readable **`preferences/edits.md`** — a diff history of every
  change (`· you` vs `· distill`, with the `+`/`-` lines). `/distill` reads that history to
  see your deletions (so it never re-adds a steer you removed), to tell its own bullets from
  yours (so it only rewords its own), and to gate the daily refresh (newest `· distill`
  entry) — no separate key ledger or stamp. The import routing map is likewise un-hidden
  (`import-aliases`), with `import.py` still falling back to a legacy `.import-aliases` if
  present. These all shipped only as unreleased dotfiles, so there is nothing to migrate. The
  only remaining dotfiles are genuine plumbing devbrain doesn't author as content
  (`.gitignore`, macOS `.DS_Store`, the gitignored `*.pglite` brain DB).
- **The global-preferences page now converges instead of growing without bound.** `/distill`
  Step 8(b) was append-only — every refresh could only ADD, so a page that's `@import`ed into
  every session grew monotonically. It now **consolidates**: a recurring steer is folded into
  the bullet `/distill` already wrote (or skipped if already covered) rather than appended as a
  near-duplicate, and two of its own bullets that say the same thing collapse into one. Your
  lines stay untouchable — it only rewords bullets it can see itself having added in the edit
  history (`preferences/edits.md`), and on any run where you've hand-edited since its last write
  it stays strictly additive and makes no in-place edits. Staleness-eviction was deliberately *not* added:
  preferences are sticky, so absence from the recent log is not evidence a steer is unwanted.
- **The global-preferences refresh now mines every project's log, not just the one you're
  distilling in.** `/distill` runs inside a single project's session, so Step 8(b) was judging
  "recurring steer" against only that project's recent log — a default you repeat once-per-repo
  across several repos looked like a one-off in each and never got promoted to the shared
  `preferences/global.md`. Step 8(b) now gathers the last 14 days of prompt log across **all**
  `projects/*/log/` and counts recurrence globally, so cross-repo steers are caught. Bounded
  by a date-dir string compare and scoped to the user-prompt blocks to keep the read cheap.
- **Dashboard word clouds filter against a comprehensive stopword list, not a hand-picked one.**
  Both the prompt word cloud and the gbrain "What You Search The Brain For" cloud were leaking
  generic English (`like`, `run`, `show`, `actually`, `where`, `again`, …). The shared `STOP`
  set now uses the 1298-word stopwords-iso English list as its base, with two small deliberate
  layers: `STOP_DENY` adds chat/affirmation filler the academic lists omit (`yup`, `lol`, `btw`,
  …), and `STOP_KEEP` rescues dev-domain words the general list over-strips (`fix`, `test`,
  `html`, `open`, `state`). Domain terms (nightshift, merge, branch, repo, gbrain, agent, …)
  are absent from the base list, so they remain the signal. Retune via the two small lists.
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
- **The Profile "Skills Called" charts now count the skills you actually ran, not just the
  ones you typed first.** A skill call was scored only when the prompt's first token was a
  slash-command, so a skill the model invoked on its own — "ok, distill?" runs `/distill`
  with no leading slash — counted as zero. The count now comes from each turn's `tools:`
  response meta, which records the real `Skill` tool-uses. To name them, `capture-response.sh`
  now writes the invoked skill into that meta (`Skill:distill×1`) instead of a nameless
  `Skill×N` — the only record of *which* skill an autonomous call ran. Older logs that saved
  only a bare `Skill×N` are unrecoverable, so an autonomous call with no leading slash to
  attribute it to is dropped rather than pooled under a meaningless "(autonomous)" chip;
  going forward every invocation is named and counted under its real skill.
- **Profile right column** now leads with the Prompts panel and puts Global Preferences
  below it.

### Added
- **`backfill-skill-names.py` recovers skill names already in your logs.** Turns captured
  before the rename above hold a nameless `Skill×N` in their `tools:` meta, so an autonomous
  call (no leading slash) was invisible on the Skills charts. The name isn't lost — the
  original Claude Code transcript on disk still has the `Skill` tool-use with its `input.skill`.
  This pass re-reads the transcripts and rewrites each bare `Skill×N` into the named
  `Skill:<name>×k` form (order-matched per session, meta-line only so quoted prose is never
  touched, idempotent). Calls whose transcript was pruned stay bare and are reported, never
  guessed. `import.py` names skills the same way when it re-derives a backfilled session.

### Removed
- **"How Terse, By Day" Profile chart** — retired.

### Fixed
- **`make test` no longer reports a spurious FAILURE when Docker isn't running.** The
  cross-platform clean-room test (`test-cross-platform-docker.sh`) bailed with exit 1 when the
  Docker daemon was absent, but `test-all.sh` classifies exit-code-first — so its bail masked as
  a suite FAIL even though `SKIP_RE` already recognizes the message. It now exits 0 on both
  Docker bails, so the documented skip convention fires and the test reports SKIP on a machine
  without Docker (e.g. macOS with Docker Desktop closed). CI runs Docker, so it still executes there.
- **The Profile "Token Cost · By Model" chart is no longer pinned to the top of its card.**
  The two cost panels share a grid row and stretch to equal height, but the shorter By-Model
  card's body kept its content height, so its few-row chart hugged the top with dead space below.
  The card is now a flex column whose body grows to fill, letting the existing `justify-content:center`
  actually center the chart vertically.
- **Nightshift can no longer report a fixed-set run "complete" while its output is missing.**
  A `--only` run now verifies an output post-condition before declaring success: every selected
  `done` task's work must still be present on `origin/nightshift`. Each merge records the
  nightshift SHA it landed at, and at wind-down the run asserts that SHA is still an ancestor —
  so a base reset that left tasks `done` but wiped their commits surfaces as a loud
  `INCOMPLETE: X/N` instead of silent data loss. Absent tasks are auto-reopened once (via a new
  `todo reopen` verb that force-reopens a `done` task) so the fleet regenerates them.
- **An empty/unparseable `--only` is now a hard error, not a silent unfenced run.** `--only ""`
  (e.g. an id-extraction that yielded an empty string) used to be accepted as "no fence" — which
  reads as "run only these" but means "run the whole queue, forever". Nightshift now requires
  `--only` to resolve to ≥1 existing task id, echoes the resolved fence at startup, and refuses
  to start otherwise.
- **Token cost was inflated ~2–3×.** Claude Code writes one transcript line per content
  block, each repeating the message-level `usage`; both writers summed per line. Now deduped
  by `message.id` (re-harvest corrects history).
- **No-prompt-log turns are now captured.** `capture-response.sh` exited before the token
  write when no prompt was logged, silently dropping nightshift workers; the harvest now runs
  on every `Stop`.
- **`import.py` dedup is now global, not per-project** — a session whose routing changed is
  no longer re-added (double-counted) under a new project.
- **Killed worker turns no longer leak out of the Profile cost.** A nightshift worker that's
  SIGKILLed mid-turn (turn timeout / hang-restart / fleet shutdown) can't run its own
  `Stop` hook, so its spend never reached the per-turn token sidecar the Profile cost card
  reads — leaving autonomous cost silently undercounted versus the Nightshift dashboard's
  transcript-sourced figure. The orchestrator's teardown now runs an idempotent
  `import.py --tokens-only` backfill that re-derives those turns straight from the
  transcripts (routing dead worktrees by path), so the Profile sidecar converges to the
  true spend without double-counting the rows the live hook did capture.
- **The Nightshift dashboard no longer goes silently stale on a run restart.** Every restart
  (orphaned-task recovery, usage-limit stalls, adding tasks mid-run) is a *new* run, and the
  open dashboard tab had no way to tell it from the old one — it would keep showing the prior
  run's worker states, "merged" count, and throughput chart until a hard-refresh. Now
  `status.json` carries a **run id** (the orchestrator PID, new on every (re)start) and a
  **start time**, the throughput chart **resets on a fresh run** instead of inheriting the old
  run's curve, and the monitor shows a live **staleness badge** ("live" → "N min ago — stale,
  hard-refresh if a run restarted") that counts up off the client clock — so a frozen tab,
  dead server, or stalled feed is obvious at a glance instead of quietly lying.
- **`nightshift watch` reaps a foreign queue squatting the port.** A stale `devbrain queue`
  from another session/workspace (pointed at a different `$DEVBRAIN_DATA`) could hold port 8799
  and serve a dead dashboard for the current run. `watch` now probes the new `/api/whoami`
  identity endpoint and, only on a positively-identified data-dir mismatch, kills that server so
  a fresh one binds — a legitimately-shared queue is never touched.

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

[Unreleased]: https://github.com/TheWeiHu/devbrain/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.5.1
[0.5.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.5.0
[0.4.1]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.4.1
[0.4.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.4.0
[0.3.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.3.0
[0.2.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.2.0
[0.1.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.1.0
