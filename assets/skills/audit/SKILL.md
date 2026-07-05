---
name: audit
description: |
  Spot-check the most recently finished delegated runs (/continue, /work,
  nightshift) against the working protocol and write a short report. Evidence-
  only: every flag cites the artifact that proves the drift (a task file, a PR,
  git history). Report-only: it never reverts work, closes or reopens tasks, or
  edits brain pages. Use when asked to "audit", "check recent runs", "did the
  agents follow protocol", or "any drift". Also auto-runs at most daily from
  `/distill`.
---

# /audit — spot-check delegated runs for protocol drift

Delegated runs drift: one skips the gbrain context read, one pushes a branch and
never opens a PR, one closes a task by hand, one branches off `nightshift`
instead of the base branch. Each looks fine in the moment — the task still ends
`done` — so drift compounds until someone samples finished runs. Audit is that
sample. Two hard rules, same as `/reconcile`:

1. **Evidence-only.** A run is flagged only when an artifact contradicts the
   protocol — never on suspicion or age.
2. **Report-only.** Audit writes one report file; it never reverts work, changes
   task state, or rewrites pages. Correcting drift is the human's call.

## Step 1 — Resolve identity + pick the sample
```bash
cwd="$(pwd)"
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
project="$(devbrain project-key "$cwd")"   # shared identity resolver (devbrain on PATH)
TODODIR="$DATA/projects/$project/todo"
git -C "$DATA" pull --rebase --autostash --quiet 2>/dev/null || true
# Sample = up to 5 finished tasks: every in-review task first, newest done to fill.
grep -l '^status: review' "$TODODIR"/*.md 2>/dev/null
grep -l '^status: done' "$TODODIR"/*.md 2>/dev/null \
  | while read -r f; do echo "$(sed -n 's/^done_at: //p' "$f" | head -1) $f"; done | sort -r | head -5
```
Cap the combined sample at 5 (review tasks take the slots first).
No finished tasks → say so and stop. The base branch to check against is the
repo's default (`git -C "$cwd" symbolic-ref -q --short refs/remotes/origin/HEAD`,
usually `origin/main`).

## Step 2 — Check each sampled task (evidence-only)
| Protocol invariant | Evidence | Flag when |
|---|---|---|
| PR before done | `pr:` frontmatter; `gh pr view <pr> --json state` | `done` with no `pr:` AND no nightshift landing (`git log origin/nightshift --oneline \| grep "merge todo/<id> "`) |
| Branched from the base (PR runs only) | `gh pr view <pr> --json commits` | the PR carries commits that are on `nightshift` but not on the base (e.g. `nightshift: merge …` subjects) — the branch was cut from the wrong base. Skip for nightshift-landed tasks: branching from `origin/nightshift` is their protocol |
| Brain context pulled | task body | no `## Context (synthesized …)` section |
| Acceptance honored | `Acceptance:` line in the task body; PR body | a taste-dependent task (writing, grading, design, UX copy) has no acceptance line, or the PR body doesn't restate/answer it |
| Conventional commits | non-merge commit subjects on the PR/branch | count subjects matching `^(feat\|fix\|docs\|test\|refactor\|perf\|chore)(\(.+\))?: ` — report N/M; flag 0/M |

A check you can't make cheaply (branch deleted, squash-merge hid the commits, no
`gh`) is recorded as "unverifiable", never guessed.

## Step 3 — Write the report + tell the user
Overwrite `$DATA/projects/$project/audit.md` — git history keeps prior reports:
```markdown
# audit — <project> — YYYY-MM-DD (sample: N finished tasks)

| task | pr | verdict |
|---|---|---|
| 0042-… | #87 | ✓ clean |
| 0044-… | — | ⚠ closed with no PR and no nightshift landing (pr: empty) |

Conventional commits: 7/9 non-merge subjects.
<1-3 lines: clean, or the drift pattern to correct — name the run type it came from.>
```
Every ⚠ cites its evidence. Print the table (or the "clean" line) back to the
user, then `DEVBRAIN_DATA="$DATA" devbrain flush audit 2>/dev/null || true`.

## Notes
- The daily auto-run and its `$DATA/projects/$project/audited.md` stamp are
  `/distill` Step 8's job; a manual `/audit` just runs and reports.
- CLI guards already block the crudest drift (`todo done` refuses without a
  recorded PR unless `--force`; `todo review` requires the PR ref) — audit
  covers what a guard can't see.
