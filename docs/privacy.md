# Privacy — the operator's guide

devbrain logs **every prompt you type**, verbatim, to a git repo you own. That is
the feature and the risk. [`SECURITY.md`](../SECURITY.md) covers *what* is captured,
*where*, *when it leaves*, and the threat model. This doc is the hands-on complement:
what an entry actually looks like on disk, what redaction does and doesn't catch, and
how to **delete**, **disable**, and **audit** your data.

Default store: `~/devbrain-data` (override with `$DEVBRAIN_DATA`). Everything below
assumes that path — substitute yours.

## What a captured entry looks like

One file per session per day: `projects/<owner>__<repo>/log/<YYYY-MM-DD>/<worktree>.<session>.md`.
Each prompt is an appended block (times UTC):

```markdown
# theweihu__devbrain — 2026-07-03 — session e694e7a2-…

> devbrain Stage A raw prompt log. Append-only, source of truth.
> agent: claude · worktree: devbrain-w1 · cwd: /Users/you/... · times in UTC

## 18:04:32

Fix the redaction regex so it also catches Stripe keys.
↳ 18:07:10 — Added an `sk_live_` rule to internal/redact and a golden test.
```

- The `## HH:MM:SS` line is your prompt, **verbatim** (after redaction).
- The `↳` line is the turn's recap plus a **bounded prose sample** of the agent's
  response — short turns whole, long ones head+middle-sampled to ~4,000 chars with
  `[…]` markers. Not the full transcript, but more than a headline.
- Assume anything you paste into a prompt lands here. Point the data repo at a
  **private** remote (or none).

## Redaction: what it catches, what it misses

Every capture path runs text through one redactor (`internal/redact`) before writing.
It replaces high-confidence, prefix-anchored secret shapes with `[REDACTED]`:

| Caught | Example |
|---|---|
| OpenAI keys | `sk-…` |
| GitHub tokens / PATs | `ghp_… gho_… ghu_… ghs_… ghr_… github_pat_…` |
| AWS access key IDs | `AKIA… ASIA…` |
| Slack tokens | `xoxb-… xoxp-… xoxa-… xoxr-… xoxs-…` |
| Bearer auth headers | `Bearer <token>` |

**It is a safety net, not a guarantee.** It will *not* catch:

- a password or API key typed in prose (`the db password is hunter2`)
- a private-key / cert blob pasted inline (`-----BEGIN … KEY-----`)
- any credential in a format not listed above (session cookies, DB URIs,
  cloud tokens other than the shapes above, custom internal token formats)

Don't rely on it for high-value secrets. Keep them out of your prompts.

## Delete data

The store is a plain git repo — delete by editing files and committing.

```bash
cd ~/devbrain-data

# One entry: open the session file and remove the `## HH:MM:SS` block.
$EDITOR projects/<owner>__<repo>/log/2026-07-03/<worktree>.<session>.md

# One project — logs and brain:
git rm -r projects/<owner>__<repo>

# Everything (nuke the store, keep the repo):
git rm -r projects && rm -rf brain

git commit -am "prune data"
git push          # only if you have a remote; propagates the deletion
```

> git keeps history: a pushed secret lives in earlier commits even after you delete
> the file. To purge it from history too, rewrite with `git filter-repo` (or
> `git filter-branch`) and force-push — or, simpler, delete and recreate the remote.

## Disable capture or a hook individually

Prompt/response/memory capture is sweep-based: `devbrain sweep` (run by every
flush) reads the agents' own transcripts on disk. **Turn off all capture:**
uninstall the flusher (`devbrain uninstall`, or `launchctl unload
~/Library/LaunchAgents/com.devbrain.flush.plist` on macOS) — nothing else writes
the log. Codex has NO devbrain hooks at all.

`devbrain install` registers only these two hooks in `~/.claude/settings.json`:

| Hook line | Event | Captures |
|---|---|---|
| `devbrain hook gbrain` | PostToolUse(Bash) | which brain searches you run |
| `devbrain hook session-start` | SessionStart | nothing — just the query nudge |

**Turn off one hook:** delete its `{ "type": "command", "command": "… hook <event>" }`
entry from `~/.claude/settings.json`.

**Turn off a group at reinstall:** `capture` (gbrain trace), `nudge`
(session-start), and `flusher` (the sweep+push timer) are installable components —
`devbrain install --without flusher` reinstalls with automatic capture off.

**Turn off everything (data untouched):** `devbrain uninstall` removes all hooks,
skills, the flusher, and the CLAUDE.md block. Your `~/devbrain-data` is left intact.

## Audit before anything leaves the machine

A launchd flusher sweeps new transcripts and commits/pushes `~/devbrain-data` every minute. To see
exactly what is staged to leave **before** it does:

```bash
cd ~/devbrain-data
git status                    # files changed since last push
git log origin/main..HEAD     # commits not yet pushed (empty if none)
git diff origin/main..HEAD    # the exact bytes that will be pushed
```

Redact or delete anything you don't want to ship, commit, then `devbrain flush` to
push on your own schedule. **With no remote configured, nothing is ever pushed** —
the flusher just commits locally. OpenAI only ever sees your text if *you* set an
OpenAI key (embeddings for semantic search); with no key, retrieval is fully local.

---

*See [`SECURITY.md`](../SECURITY.md) for the threat model and how to report a
vulnerability, and [`../DESIGN.md`](../DESIGN.md) for the architecture.*
