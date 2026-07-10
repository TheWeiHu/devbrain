---
name: reconcile
description: |
  Check this project's brain pages against reality and MARK what's contradicted.
  Reconcile re-reads the brain, verifies each concrete claim against the live repo
  (and against the other pages), and appends a dated `⚠ stale` note to any fact that
  is CONTRADICTED — by the code, a PR/issue state, or another page. Mark-only: it
  never deletes or rewrites page content. Evidence-only: no mark without a cited
  contradiction (never just because a page is old). Use when asked to "reconcile",
  "check the brain", "is the brain still accurate", or "find stale brain pages".
  Also auto-runs at most daily from `/distill`.
---

# /reconcile — check the brain against reality, mark contradictions

The brain drifts: code changes, PRs merge, decisions reverse — but the pages that
described the old world stay put. Reconcile catches that drift. Two hard rules:

1. **Evidence-based, never age-based.** A page is marked only when something
   *contradicts* it. An old-but-true fact is fine; leave it alone.
2. **No mark without a cited contradiction.** Every `⚠` names what disproves the
   claim (a file, a PR, another page).

**Mark-only.** Reconcile *annotates*; it never deletes or rewrites page content.
Everything it writes is a git-tracked addition — review/undo with
`git -C "$DATA" diff`. (Merging/pruning is a deliberate future step, not this one.)

## Step 1 — Resolve identity + load the pages
```bash
cwd="$(pwd)"
DATA="$(devbrain config data-dir)" || exit 1
project="$(devbrain project-key "$cwd")"   # shared identity resolver (devbrain on PATH)
BRAINDIR="$DATA/projects/$project/brain"
DEVBRAIN_DATA="$DATA" devbrain flush reconcile-sync || { echo "data sync failed; stop reconciliation"; exit 1; }
ls "$BRAINDIR"/*.md 2>/dev/null || { echo "no brain pages for $project — nothing to reconcile"; exit 0; }
```
Read every page in `$BRAINDIR`. The repo to check claims against is the working repo
at `$cwd` (for cross-project brains, reconcile runs from a checkout of that project).

## Step 2 — Find contradictions (evidence-only)
For each concrete claim a page makes, try to disprove it with a cheap check:

| Claim mentions… | Check |
|---|---|
| a file / function / flag / path | does it still exist? (`grep`/`ls` in `$cwd`) |
| a PR or issue (`#N`) | its state — merged / closed / open (`gh pr view N` / `gh issue view N`) |
| "X was added / removed / renamed" | is X actually present / gone now? |
| something another page asserts | does that other page still say the same thing? |

A claim only counts as stale when a check **contradicts** it (file gone, PR closed
unmerged, behavior reversed, two pages disagree). Claims you can't cheaply verify,
or that are merely old, are **left untouched**.

## Step 3 — Mark (mark-only, idempotent)
For each contradicted fact, append a note on its own line right after it:
```
⚠ stale (YYYY-MM-DD): <what contradicts it — cite the file / PR# / page>
```
- Cite the evidence every time (e.g. "code removed this in #12", "PR #9 closed
  unmerged", "contradicts <owner>__<repo>/implementation").
- **Skip facts that already carry a `⚠ stale` note** — don't re-mark; reconcile must
  converge, not thrash.
- Change **nothing else**: no deletions, no rewrites, no reordering.

## Step 4 — Load + flush + report
```bash
# Re-index the marked pages — only when gbrain is installed. The marks are already
# written to disk (the source of truth); this just refreshes the optional index.
if command -v gbrain >/dev/null 2>&1; then
  for f in "$BRAINDIR"/*.md; do
    base="$(basename "$f" .md)"; gbrain put "$project/${base#"$project"-}" < "$f" >/dev/null 2>&1
  done
fi
DEVBRAIN_DATA="$DATA" devbrain flush reconcile 2>/dev/null || true
```
Report the marks you added (page + the contradiction each cites), or say the brain
checked out clean. End with the `git -C "$DATA" diff` pointer — that's the undo.

## Notes
- If nothing is contradicted, write nothing and say so.
- Reconcile is the *global, cross-page, repo-verified* check; `/distill`'s light
  dedup/supersede on the page it's already writing is the *local* tidy. They pair.
