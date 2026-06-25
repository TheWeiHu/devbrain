# Security

devbrain captures what you type into Claude Code, distills it into a searchable
brain, and syncs it to a git repo you own. That makes the data flow itself the
security surface — more than any single line of code. This document describes,
plainly, **what is captured, where it goes, who can see it, and how to report a
problem.** It describes the system as it ships; it does not change behavior.

If you only read one thing: **your prompts and brain live in a separate private
store at `~/devbrain-data` that you own and point at your own git remote. The
devbrain repo (this one) is just the installer, hooks, and skills — it never
contains your data.**

## Reporting a vulnerability

Please report security issues **privately** — do not open a public issue for a
vulnerability.

- Open a [GitHub private security advisory](https://github.com/TheWeiHu/devbrain/security/advisories/new)
  (Security → Report a vulnerability), or
- email the maintainer listed on the GitHub profile.

Include what you observed, how to reproduce it, and the impact. We aim to
acknowledge within a few days. Because devbrain runs entirely on the user's own
machine and writes to a repo the user controls, there is no hosted service to
patch — fixes ship as a new release that users pull and re-run `setup` to apply.

## What is captured

All capture is **model-free and local first** — a shell hook writes to a file on
your disk. No capture step sends your data anywhere on its own; syncing is a
separate step (see *Where it is stored*).

| Source | Hook / mechanism | What it records |
|---|---|---|
| **Your prompts** | `UserPromptSubmit` hook (`capture.sh`) | Every prompt **verbatim**, with a UTC timestamp. This is the lossless, append-only "source of truth." |
| **Response recaps** | `Stop` hook (`capture-response.sh`) | Only the **last sentence** of the assistant's final message (capped at 500 chars) — *not* full responses. |
| **`/memory` notes** | `SessionEnd` hook (`capture-memory.sh`) | A redacted mirror of memory files you write, only when they change. |
| **Shell-tool traces** | `PostToolUse(Bash)` hook (`capture-gbrain.sh`) | `gbrain` invocations: the redacted, truncated command and a short output snippet. |
| **Imported history** | `devbrain import` (opt-in, manual) | Your existing Claude Code transcripts/history, seeded into the brain on demand. |

Synthetic, host-generated prompts (system reminders, command wrappers, title
generation) are filtered out at capture and are **not** recorded as your prompts.

### Redaction at capture

Before any captured text is written, it passes through a shared redactor
(`devbrain_lib.redact`) that masks **high-confidence, prefix-anchored secret
shapes**: OpenAI keys (`sk-…`), GitHub tokens (`ghp_`/`gho_`/… and
`github_pat_…`), AWS access key IDs (`AKIA…`/`ASIA…`), Slack tokens (`xox…`), and
`Bearer …` tokens.

This is **best-effort, not comprehensive.** It catches well-known token formats by
shape; it does **not** detect passwords, free-form secrets, private data pasted
into a prompt, or any credential that doesn't match a known prefix. Treat the log
as containing whatever you typed. If you paste a secret that isn't one of the
recognized shapes, it will be captured verbatim — rotate it and scrub the log.

## Where it is stored, and when it is pushed

- **Location:** `~/devbrain-data` (overridable with `$DEVBRAIN_DATA`) — a git
  repository **you own**. Layout:
  `projects/<project>/log/<YYYY-MM-DD>/<worktree>.<session>.md`.
- **Routing:** `<project>` is derived from the **git remote** of your working
  directory. Repos with no owner/remote fall into a shared `miscellaneous`
  bucket. The capture hook reads identity *from* your working repo but writes
  *to* the absolute `~/devbrain-data` path — the two git repos never entangle, so
  your prompts cannot leak into the repo you happen to be working in.
- **When it leaves your machine:** a per-machine flusher commits and pushes
  `~/devbrain-data` to its git remote roughly **every 5 minutes**. Until then,
  everything is local to your disk. If you never give `~/devbrain-data` a remote,
  nothing is pushed off-machine at all.

## Who can see it / third parties in the data flow

devbrain adds **no servers of its own**. The only parties that can see your data
are ones you configure:

1. **The git remote host for `~/devbrain-data`.** Whoever you point that repo at
   (GitHub, a private server, nothing) can see everything the flusher pushes.
   Use a **private** remote. This is the primary place your data goes off-machine.
2. **OpenAI — only if you set `OPENAI_API_KEY`, and only for embeddings/semantic
   search.** See below.
3. **Anthropic (Claude Code itself).** Your prompts already go to Claude as part
   of using Claude Code; devbrain does not add to or change that relationship — it
   only keeps a local copy.

### OpenAI embeddings (opt-in)

Semantic search (`gbrain query`) and the embedding index are an **optional
enhancement that is off unless you set `OPENAI_API_KEY`.**

- **With a key:** brain **page text and log text are sent to OpenAI's embeddings
  API** to build the semantic index, and query text is sent at search time. This
  is the text that leaves your machine for OpenAI.
- **Without a key:** devbrain falls back to keyword search + graph ranking.
  **Nothing is sent to OpenAI** — no text leaves the machine via this path.

The key is opt-in by design; if you don't want any brain text sent to OpenAI,
simply don't set `OPENAI_API_KEY` and you keep full search via keywords.

## Threat model (STRIDE-lite)

Scope: the capture → store → push → embed pipeline on a single user's machine.
The trust boundary is your machine and the git remote you choose.

| Threat | Surface | Mitigation / residual risk |
|---|---|---|
| **Information disclosure** | Prompts and recaps stored in plain markdown; pushed to a git remote. | Use a **private** remote. Redaction masks common token shapes at capture, but is best-effort — non-standard secrets are stored verbatim. Data at rest is **not** encrypted beyond your disk/remote's own protections. |
| **Disclosure to third parties** | OpenAI embeddings when a key is set. | Fully opt-in; keyless installs send nothing to OpenAI. |
| **Tampering** | Hooks run shell on every prompt / tool call. | Hooks are part of this repo — review them before install. `setup` only wires the local machine and is idempotent. |
| **Repudiation / integrity** | The log is the source of truth. | Append-only, one writer per session file → conflict-free git merges; brain pages are a rebuildable projection (a bad page is reverted; the log is never touched). |
| **Spoofing / leakage across repos** | Working in repo A while writing to `~/devbrain-data`. | Identity is read from the working repo but writes go to the absolute data path; prompts physically cannot land in the repo you're editing. |
| **Elevation of privilege** | nightshift runs autonomous git ops (force-pushes a branch, opens PRs). | Opt-in, started deliberately, never auto-runs; scoped to the throwaway `nightshift` branch. |

## Your responsibilities

- Point `~/devbrain-data` at a **private** remote (or none).
- Don't set `OPENAI_API_KEY` if you don't want brain/log text sent to OpenAI.
- Remember the log captures prompts verbatim: if you paste a secret that isn't a
  recognized token shape, **rotate it and scrub the log** — redaction won't catch
  it.
- Review the hooks in this repo before installing; they run on your machine.

See also: the privacy documentation (companion to this file) and `DESIGN.md` for
the full capture/store/sync architecture.
