# devbrain — Concurrency & Sync

**Locking (no locks, after `tk`):** one worktree ↔ one branch ↔ one issue.
**Branch existence is the claim** — `git checkout -b feat/issue-N`; first push /
GitHub issue-assignment wins; the loser re-picks. gbrain is the *wrong* place for
an authoritative lock (per-machine, disposable); it only mirrors **advisory**
status (`status`, `claimed_by`), refreshed from the world. A true gbrain lease
needs a shared Supabase backend — re-introducing the coordination point we
designed away.

**Append-only everywhere → concurrency for free:**
- Logs **shard per session** → one writer per file → git pulls only *add* files.
- Brain updates are **append events**, projected newest-wins by (timestamp,
  provenance); gbrain dedups. Contradictions keep history.
- The gbrain index is **eventually-consistent / self-healing** (re-derivable).
- Genuine code overlap is a normal git merge — git owns it.

**Cross-machine log sync:** logs live in the `~/devbrain` git repo; sync is
`git push`/`pull`. Per-session sharding makes merges always "add new files." The
"all prompts by date" view is a read-time projection (sort shards by timestamp;
minor cross-machine clock-skew caveat).

**Cross-repo write:** absolute-path append (cwd-independent) + a **single
per-machine flusher** running `git -C ~/devbrain pull --rebase && add && commit &&
push`. One writer avoids `.git/index.lock` contention; pull-then-push + sharding
make it conflict-free. Alternative: put `~/devbrain` in Dropbox/Syncthing and skip
git (no conflicted copies, because no shared-file writes).

Source: design conversation, 2026-06-13.
See also: [[project/devbrain-capture]], [[project/devbrain-overview]].
