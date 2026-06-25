# Security Policy

devbrain captures the prompts you type, short recaps of what the agent did, your
`/memory` notes, and imported Claude Code history, then distills them into a
searchable brain. Because the hooks read genuinely sensitive material — prompts,
transcripts, memory, and tool outputs — this document spells out **what is
captured, where it goes, who can see it, and how to report a problem.** It
describes the system as it actually behaves; it does not change behavior.

If you only read one section, read [Threat model](#threat-model) and
[Reporting a vulnerability](#reporting-a-vulnerability).

## What is captured

| Source | Hook / tool | What it records |
| --- | --- | --- |
| Your prompts | `UserPromptSubmit` → `capture.sh` | Every prompt you submit, **verbatim**, with a UTC timestamp. Model-free and append-only — the raw log is the source of truth. |
| Agent recaps | `Stop` → `capture-response.sh` | The **last sentence of the agent's final message** (capped at 500 chars). Not full responses, not intermediate tool output. |
| `/memory` notes | `SessionEnd` → `capture-memory.sh` | Notes you saved via `/memory`, mirrored into the log. |
| Imported history | `scripts/import.py` | Existing Claude Code transcripts/history you choose to backfill. |
| The brain | `/distill`, gbrain pages | Topic pages distilled from the above — a projection, not a new source of data. |

Tool outputs are not captured as a separate stream; only the prompt log and the
one-sentence recap above are written automatically.

### Secret redaction at capture

Before anything is written to disk, every captured string passes through
`redact()` in `hooks/devbrain_lib.py` (the single chokepoint shared by
`capture.sh`, `capture-response.sh`, `capture-memory.sh`, and `import.py`). It
strips **high-confidence, prefix-anchored secret shapes**:

- OpenAI-style keys (`sk-…`)
- GitHub tokens (`ghp_/gho_/ghu_/ghs_/ghr_…`, `github_pat_…`)
- AWS access key IDs (`AKIA…`, `ASIA…`)
- Slack tokens (`xoxb-/xoxp-/…`)
- `Bearer <token>` authorization values

**This is a safety net, not a guarantee.** Redaction matches known token *shapes*;
it does not catch passwords, free-form secrets, private keys pasted as blocks, or
anything without a recognizable prefix. Treat the data store as if it contains
everything you typed.

## Where it is stored, and when it is pushed

- **Location:** `~/devbrain-data` by default (override with `$DEVBRAIN_DATA`), a
  plain git repository that **you own**. Layout:
  `projects/<project>/log/<YYYY-MM-DD>/<worktree>.<session>.md`, where
  `<project>` is derived from the git remote of your working repo. Local repos
  with no owner collapse into a shared `miscellaneous` bucket.
- **On disk, local first:** captures are appended locally and are durable on your
  machine immediately, with no network involved.
- **Push cadence:** a single per-machine flusher commits and pushes to your data
  repo's git remote roughly **every 5 minutes** — *only if you connected a
  remote.* Local-only installs never push; the flusher simply skips the push step.
- **Isolation:** the capture hook reads identity *from* your working repo but
  writes to the absolute `~/devbrain-data` path. The two git repos never
  entangle, so prompts cannot leak into the project you are working on.

## Who can see it / third parties in the data flow

devbrain sends data off your machine in **at most two** places, both under your
control:

1. **Your git remote host.** Whoever hosts the `~/devbrain-data` remote you
   configured (e.g. a private GitHub repo) can see everything pushed there. If you
   run local-only, nothing leaves the machine. **Keep this repo private.**
2. **OpenAI — only if you opt in.** Embeddings and semantic `gbrain query` require
   an explicit `OPENAI_API_KEY`. When set, brain **page and log text** is sent to
   OpenAI's embeddings API to build the search index, and your **query text** is
   sent when you run a semantic query. With **no key**, gbrain falls back to local
   keyword `search` and **nothing is sent to OpenAI.** The key is documented as an
   optional enhancement; put it in `~/.zshenv` so non-interactive hooks see it.

No other third parties are in the data flow. devbrain itself has no server, no
telemetry, and no "phone home."

## Threat model

Scope: the capture → store → push → embed pipeline above. STRIDE-lite, with the
mitigation devbrain already provides and the residual risk you own.

| Threat | Vector | Mitigation in devbrain | Residual risk (your responsibility) |
| --- | --- | --- | --- |
| **Information disclosure** | Secrets land in the log and sync to a remote | Prefix-anchored secret redaction at the single capture chokepoint | Non-prefixed secrets, passwords, and pasted private keys are **not** redacted — assume the store holds them |
| **Information disclosure** | A public or over-shared data remote exposes prompts | Remote is opt-in; local-only never pushes | You must keep `~/devbrain-data` **private** and access-controlled |
| **Information disclosure** | Query/page text sent to OpenAI | Embeddings are off unless `OPENAI_API_KEY` is set; keyless = fully local | If you enable it, page/log/query text leaves your machine to OpenAI |
| **Tampering** | Edited or forged log/brain entries | Log is append-only and git-tracked; history is auditable | Anyone with write access to the data repo can rewrite it — restrict collaborators |
| **Spoofing / Elevation** | Malicious hook or skill runs in your shell | Hooks are vendored from the repo and installed by `./setup`; capture is model-free with no eval of prompt content | Review the repo before `./setup`; only install skills/hooks you trust |
| **Repudiation** | "Who wrote this / when?" | UTC timestamps + git commit history on every entry | — |
| **Denial of service** | Capture failure blocks your agent | Capture fails **open** — if `python3`/redaction is unavailable it does not block the session | A failed-open capture may drop an entry rather than halt work |

**Out of scope:** the security of Claude Code itself, your OS account, your git
host's access controls, and OpenAI's handling of submitted text. devbrain inherits
the trust boundary of the machine and accounts it runs under.

## Reducing your exposure

- Keep `~/devbrain-data` a **private** repository; review who has access.
- Run **local-only** (no remote, no OpenAI key) if you never want data to leave
  the machine — capture and keyword search still work.
- Don't paste secrets into prompts. Redaction is a backstop, not a guarantee.
- Periodically review the store: `git -C ~/devbrain-data log` and `grep` for
  anything that should not be there. You own the repo and can rewrite history.

## Reporting a vulnerability

If you find a security issue in devbrain — a redaction bypass, an unintended data
path, a hook that writes outside its boundary, or anything that leaks captured
data — please report it **privately**:

1. Open a **GitHub private security advisory** on the repository
   (`Security` → `Report a vulnerability`) so the details are not public while a
   fix is prepared.
2. If advisories are unavailable to you, open a minimal GitHub issue that says you
   found a security problem and requests a private channel — **do not** include
   the exploit details, captured data, or secrets in a public issue.

Please include the version/commit, a description of the issue, and a minimal
reproduction. We aim to acknowledge reports promptly and will credit reporters who
want it. Do not disclose publicly until a fix is available.

---

See also: [DESIGN.md](DESIGN.md) for the capture/distill architecture, and the
project README for install and daily use.
