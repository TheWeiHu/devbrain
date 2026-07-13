# Security Policy

devbrain captures the prompts you write in Claude Code or Codex, recaps of what
the agent did, your `/memory` notes, imported Claude Code history, and the tool outputs that flow
through those — then stores them in a git repo you own. For most tools a security
policy is boilerplate; for this one it is the point. This document describes,
plainly, **what is captured, where it is stored, when it leaves your machine, and
who can see it** — so you can decide what to point it at.

> **One-line model:** the only thing that ships in *this* repo is the system
> (hooks, skills, installer). Your prompts and brain live in a **separate private
> store you own** (`~/devbrain-data` by default) — devbrain never sends your data
> anywhere except the git remote *you* configure, plus OpenAI *only* if *you* set
> an API key.

## What is captured

| Source | Mechanism | What it records |
|---|---|---|
| **Your prompts** | model-free transcript sweep (`devbrain sweep`, run by every flush) | every prompt you type in Claude Code or Codex, verbatim, with a UTC timestamp — the append-only "source of truth" |
| **Response recaps + samples** | the same sweep | a one-line recap (the agent's last sentence, capped at 500 chars) **and** a bounded sample of the turn's prose — short turns kept whole, longer ones head+middle sampled to ~4,000 chars with `[…]` gap markers. Not the full transcript, but more than a headline. |
| **`/memory` notes** | the same sweep (Claude memory dir mirror) | memory notes you write during a Claude Code session |
| **Imported history** | `devbrain import` (`scripts/import.py`), opt-in | your existing Claude Code transcripts/history, seeded into the brain |
| **gbrain queries** | `PostToolUse(Bash)` hook (`devbrain hook gbrain`) | the brain searches you run (for hit-rate metrics) |

Tool outputs are not captured separately — they are recorded only insofar as they
appear in a prompt or in the agent's recap, and pass through the same redaction.

### Secret redaction at capture time

Before anything is written to disk, capture passes the text through a single
redactor (`internal/redact` in the Go binary), shared by every capture path so
they cannot drift. It strips high-confidence secret shapes:

- OpenAI keys (`sk-…`) and other `sk<alnum>` tokens (e.g. Sanity)
- GitHub tokens and PATs (`ghp_…`, `gho_…`, `ghu_…`, `ghs_…`, `ghr_…`, `github_pat_…`)
- AWS access key IDs (`AKIA…`, `ASIA…`)
- Slack tokens (`xoxb-…`, `xoxp-…`, …)
- `Bearer <token>` authorization headers
- Vendor prefixes: Vercel (`vcp_…`), Firecrawl (`fc-…`), Perplexity (`pplx-…`)
- PEM private-key blocks (`-----BEGIN … PRIVATE KEY-----` … `-----END …-----`)
- **Any `NAME=value` env line** whose name has a secret-shaped segment
  (`…KEY`, `…TOKEN`, `SECRET`, `PASSWORD`, `CREDENTIAL`, `AUTH`) — this catches
  pasted `.env` files from unknown vendors by variable name, value redacted whole

This is **best-effort, pattern-based** redaction — a safety net, not a guarantee.
It will not catch a password typed in prose, a secret assigned to a non-obvious
variable name, or a credential in an unrecognized format. Treat the data store as
containing whatever you put in your prompts.

## Where it is stored

- **Location:** `~/devbrain-data` by default, overridable via `$DEVBRAIN_DATA` or
  at `./setup` time. It is a **git repo you own**.
- **Layout:** `projects/<owner>__<repo>/log/<YYYY-MM-DD>/<worktree>.<session>.md`.
  The `<owner>__<repo>` key is derived from the working repo's **git remote**;
  local or no-owner repos fall back to a shared `miscellaneous` bucket.
- **Isolation:** the sweep reads identity *from* the working repo but writes
  *to* the absolute data path. The two git repos never entangle — your prompts
  physically cannot leak into the repo you are working on.

## When it leaves your machine

- **The git remote you configure.** A per-machine flusher commits the data repo
  and pushes it roughly every minute. **If you give the data repo no remote, it
  never leaves your machine** — local-only works; the flusher just skips the push.
  Where it goes is entirely your choice of remote.
- **OpenAI — only if you opt in.** Semantic `gbrain query` and embeddings run
  **only when an OpenAI key is configured** — either `OPENAI_API_KEY` in the
  environment or a key stored via `gbrain config set openai_api_key` (in
  `~/.gbrain/config.json`); setup detects and enables both. When enabled, brain
  page text and log text are sent to OpenAI's embeddings API to build the search
  index. With **no key configured by either route**, devbrain falls back to
  keyword search and **nothing leaves the machine** for retrieval. The key is an
  optional enhancement, never required.

That is the complete list of parties in the data flow: you, your git remote host,
and (optionally) OpenAI. devbrain has no server, no telemetry, and no other
network egress.

## Threat model (STRIDE-lite)

The data flow is **capture → store → push → embed**. The dominant risk is
**information disclosure**, because the data is sensitive by design.

| Threat | Surface | Mitigation / residual risk |
|---|---|---|
| **Spoofing** | n/a — no auth surface; no server | devbrain runs locally as you; trust boundary is your shell account |
| **Tampering** | local data repo, sweep timer | git history is your audit trail; the flusher job and remaining hooks live under `~/` you control. A local attacker with your account can edit either |
| **Repudiation** | append-only log + git | the log is append-only and timestamped (UTC); git commits attribute changes |
| **Information disclosure** | the data store; the git remote; OpenAI | redaction strips known secret shapes (best-effort); **the remote you pick must be private**; OpenAI egress is opt-in via key. Residual: unredacted secrets in prose, a misconfigured-public remote, anything embedded once a key is set |
| **Denial of service** | sweep + hooks | the sweep runs outside agent sessions (a failure delays capture, never blocks a turn); the two remaining hooks fail *open* — no availability impact on Claude Code |
| **Elevation of privilege** | local hooks/scripts | no privilege escalation; everything runs with your existing user permissions |

**Your responsibilities as the operator:**

1. **Point the data repo at a *private* remote.** A public remote publishes every
   prompt you have ever typed. This is the single highest-impact setting.
2. **Decide on OpenAI.** Configuring an OpenAI key — via `OPENAI_API_KEY` or
   `gbrain config set openai_api_key` — enables embeddings and sends page/log
   text to OpenAI. Leave **both** unset to keep retrieval fully local.
3. **Don't rely on redaction for high-value secrets.** It catches known token
   formats only. Avoid pasting raw credentials into prompts.

## Reporting a vulnerability

If you find a security issue in devbrain (e.g. a redaction bypass, an unintended
network egress, a path that writes outside the data repo, or a way one repo's data
leaks into another), please report it **privately**:

- Open a **GitHub private security advisory** at
  <https://github.com/TheWeiHu/devbrain/security/advisories/new>, or
- Open a regular issue **only if it contains no sensitive details**.

Please do not disclose the issue publicly until it has been addressed. Include
repro steps and the affected version (`./setup --version`). We aim to acknowledge
reports within a few days.

## Scope

In scope: the devbrain hooks, skills, installer, and CLI in this repo — anything
that captures, stores, pushes, or embeds your data. Out of scope: Claude Code
itself, the OpenAI API, your chosen git host, and the security of the machine
devbrain runs on.

---

*See also: [`README.md`](README.md) for the data-flow overview,
[`DESIGN.md`](DESIGN.md) for the architecture, and
[`docs/privacy.md`](docs/privacy.md) for the hands-on guide to deleting,
disabling, and auditing your captured data.*
