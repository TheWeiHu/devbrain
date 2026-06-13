# devbrain — Stage B: Brain (gbrain)

**gbrain** (v0.18.2, "personal knowledge brain") is the organized,
queryable brain: markdown **pages** in local Postgres (PGLite) or Supabase,
with typed **links**, **backlinks**, **tags**, **timeline**, keyword + hybrid
semantic **search**, and an **MCP server** agents call. It round-trips with
markdown (`import`/`export`) and does git-to-brain `sync`.

**Role:** stages B and C, not the source of truth and not the lock — a fast,
**rebuildable projection** of the markdown logs. Tasks / requirements /
assumptions become linked, tagged pages, each carrying **provenance**
(→ log entry / issue). Updates are append events; pages are projected newest-wins.

**Distillation is explicit:** a `/checkpoint` step distills new log entries into
proposed page updates that you approve — no magic inference. "Improves, not
grows": each checkpoint supersedes, dedups (semantic match), prunes closed;
re-deriving from the log resets drift.

**Rebuild cost:** `import --no-embed` is instant (keyword + graph usable
immediately); embeddings backfill in the background via the OpenAI embedder
(~seconds for hundreds of pages, ~minutes / pennies for ~10k chunks).
`sync` / `embed --stale` are incremental — full cost paid only once per new
machine. This is why a per-machine brain is fine: losing it is cheap.

**PGLite vs Supabase:** PGLite local by default (own the file, rebuild per
machine from synced logs). Supabase only if you want one shared *live* brain and
gbrain-mediated leasing — at the cost of a hosted-DB dependency.

Source: design conversation, 2026-06-13.
See also: [[project/devbrain-overview]], [[project/devbrain-assemble]].
