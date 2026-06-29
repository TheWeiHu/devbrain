#!/usr/bin/env bash
# devbrain — import.py smoke test. Builds a fake ~/.claude (a transcript with a
# prompt+response and a memory file with a secret), runs the importer, and checks the
# dry-run writes nothing while --apply mirrors logs + memory, redacted.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; IMPORT="$HERE/import.py"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not installed"; exit 0; }

claude="$(mktemp -d)"; data="$(mktemp -d)"
trap 'rm -rf "$claude" "$data"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# A transcript: one user prompt + a final assistant message (with a fake secret), in a
# project dir that also has a memory/ store.
slug="$claude/projects/-tmp-acme-widgets"
mkdir -p "$slug/memory"
{
  printf '%s\n' '{"type":"user","isSidechain":false,"timestamp":"2026-05-20T10:00:00.000Z","cwd":"/tmp/acme/widgets","message":{"content":"add a healthcheck endpoint"}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-05-20T10:01:00.000Z","cwd":"/tmp/acme/widgets","message":{"model":"claude-opus-4-8","usage":{"input_tokens":120,"output_tokens":340,"cache_creation_input_tokens":0,"cache_read_input_tokens":7000},"content":[{"type":"text","text":"Added /healthz returning 200. Wired it into the router. Done."},{"type":"tool_use","name":"Edit","input":{"file_path":"/tmp/acme/widgets/app.py"}}]}}'
} > "$slug/session1.jsonl"
# A memory file with a FAKE secret — the bait for the redaction assertion below.
# `sk-abc…` is a dummy (not a real key) shaped to match the importer's sk-[…]{20,}
# pattern, so the test can prove tokens are scrubbed to [REDACTED] before anything is
# written to the (pushed) data repo.
{
  printf '%s\n' '---' 'name: deploy-note' 'type: reference' '---'
  printf '%s\n' 'Deploy via git only. Token sk-abcdefghijklmnopqrstuvwxyz0123 must be scrubbed.'
} > "$slug/memory/reference_deploy.md"

# Route the dead cwd (basename "widgets") deterministically with an alias — the only
# non-remote routing the importer does.
common="--data $data --claude $claude --alias widgets=acme__widgets"

# Dry-run writes nothing.
python3 "$IMPORT" $common >/dev/null
check "dry-run writes nothing" '[ -z "$(find "$data" -type f 2>/dev/null)" ]'

# Without an alias the dead cwd is unresolved -> miscellaneous, and the dry-run prompts
# the setting-up agent to alias it (text, not code, does the judgment call).
noalias="$(python3 "$IMPORT" --data "$data" --claude "$claude" 2>/dev/null)"
check "unrouted history names the dir for the agent" 'printf "%s" "$noalias" | grep -q "AGENT:" && printf "%s" "$noalias" | grep -q "widgets"'

# Apply.
python3 "$IMPORT" $common --apply >/dev/null
log="$(find "$data/projects/acme__widgets/log" -name '*.md' 2>/dev/null | head -1)"
mem="$data/projects/acme__widgets/memory/reference_deploy.md"

check "writes a log file"            '[ -n "$log" ]'
check "log has the prompt"           'grep -q "add a healthcheck endpoint" "$log"'
check "recap = closing sentence (#15)" 'grep -q "↳ .* —" "$log" && grep -q "Wired it into the router" "$log"'
check "log records touched file"     'grep -q "touched: app.py" "$log"'
check "log carries BACKFILLED banner" 'grep -q "BACKFILLED" "$log"'
check "mirrors the memory file"      '[ -f "$mem" ]'
check "redacts secret in memory"     'grep -q "REDACTED" "$mem" && ! grep -q "sk-abcdefghijklmnopqrstuvwxyz0123" "$mem"'

# Token backfill: --apply writes the tokens.jsonl sidecar with the turn's usage+model.
tok="$data/projects/acme__widgets/tokens.jsonl"
check "backfills tokens sidecar"     '[ -s "$tok" ]'
check "sidecar carries usage+model"  'grep -q "\"in\": 120" "$tok" && grep -q "\"out\": 340" "$tok" && grep -q "claude-opus-4-8" "$tok"'
check "sidecar marks interactive"    'grep -q "\"auto\": false" "$tok"'   # /tmp/acme/widgets is not a worker
# Idempotent: re-running --apply must not duplicate the session's records.
python3 "$IMPORT" $common --apply >/dev/null
check "re-apply does not duplicate"  '[ "$(wc -l < "$tok")" -eq 1 ]'

# Per-message dedup: Claude Code writes one transcript LINE per content block, each
# repeating the same message-level usage. A turn whose response has 3 blocks must bill
# its usage ONCE (cache_read 7000), not 3× (21000). Same message.id across the 3 lines.
dataB="$(mktemp -d)"; claudeB="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$data2" "$data3" "$dataB" "$claudeB"' EXIT
sb="$claudeB/projects/-tmp-acme-widgets"; mkdir -p "$sb"
{
  printf '%s\n' '{"type":"user","isSidechain":false,"timestamp":"2026-05-20T10:00:00.000Z","cwd":"/tmp/acme/widgets","message":{"content":"split a response into blocks"}}'
  for blk in '{"type":"thinking","thinking":"hm"}' '{"type":"text","text":"All set. Done."}' '{"type":"tool_use","name":"Edit","input":{"file_path":"/tmp/acme/widgets/a.py"}}'; do
    printf '%s\n' '{"type":"assistant","timestamp":"2026-05-20T10:01:00.000Z","cwd":"/tmp/acme/widgets","message":{"id":"msg_dup1","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":7000},"content":['"$blk"']}}'
  done
} > "$sb/sessionB.jsonl"
python3 "$IMPORT" --data "$dataB" --claude "$claudeB" --alias widgets=acme__widgets --apply >/dev/null
tokB="$dataB/projects/acme__widgets/tokens.jsonl"
check "dedup: usage billed once, not per-block"  'grep -q "\"cache_read\": 7000" "$tokB" && ! grep -q "21000" "$tokB"'

# Global dedup: a turn already recorded under ANOTHER project must not be re-added when its
# routing changes (worktree deleted / remote now resolves elsewhere). Pre-seed session1's
# turn under miscellaneous; import routes it to acme__widgets but must skip it (seen globally).
dataG="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$data2" "$data3" "$dataB" "$claudeB" "$dataG"' EXIT
mkdir -p "$dataG/projects/miscellaneous"
printf '%s\n' '{"ts":"2026-05-20T10:01:00Z","session":"session1","model":"claude-opus-4-8","in":120,"out":340,"cache_create":0,"cache_read":7000,"auto":false}' > "$dataG/projects/miscellaneous/tokens.jsonl"
python3 "$IMPORT" --data "$dataG" --claude "$claude" --alias widgets=acme__widgets --tokens-only --apply >/dev/null
check "global dedup: not re-added under a new route"  '[ ! -e "$dataG/projects/acme__widgets/tokens.jsonl" ]'

# Exclude opts a project out.
data2="$(mktemp -d)"; data3="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$data2" "$data3"' EXIT
python3 "$IMPORT" --data "$data2" --claude "$claude" --alias widgets=acme__widgets --exclude acme__widgets --apply >/dev/null
check "--exclude skips the project"  '[ -z "$(find "$data2/projects/acme__widgets" -type f 2>/dev/null)" ]'

# Persistent alias file ($DATA/.import-aliases) routes the same way as --alias.
mkdir -p "$data3"
printf '%s\n' '# rename map' 'widgets=acme__widgets' > "$data3/.import-aliases"
python3 "$IMPORT" --data "$data3" --claude "$claude" --apply >/dev/null
check "alias file routes the project" '[ -n "$(find "$data3/projects/acme__widgets/log" -name "*.md" 2>/dev/null | head -1)" ]'

# --tokens-only: writes the token sidecar but NO prompt logs (cost-history backfill
# on an existing install without re-adding BACKFILLED logs).
data5="$(mktemp -d)"; trap 'rm -rf "$claude" "$data" "$data2" "$data3" "$data5"' EXIT
python3 "$IMPORT" --data "$data5" --claude "$claude" --alias widgets=acme__widgets --tokens-only --apply >/dev/null
check "tokens-only writes the sidecar"  '[ -s "$data5/projects/acme__widgets/tokens.jsonl" ]'
check "tokens-only writes NO logs"      '[ -z "$(find "$data5/projects/acme__widgets/log" -name "*.md" 2>/dev/null)" ]'

# LIVE session: a session already captured live (its log exists, no BACKFILLED banner)
# must STILL get its tokens backfilled — token logging is new, so a live log says nothing
# about whether tokens were recorded. The log harvest is skipped (no duplicate), the token
# sidecar is not.
data4="$(mktemp -d)"; trap 'rm -rf "$claude" "$data" "$data2" "$data3" "$data4" "$data5"' EXIT
livelog="$data4/projects/acme__widgets/log/2026-05-20"; mkdir -p "$livelog"
printf '# live\n\n## 10:00:00\n\nadd a healthcheck endpoint\n\n↳ 10:01:00 — pre-existing live recap\n\n' > "$livelog/widgets.session1.md"
python3 "$IMPORT" --data "$data4" --claude "$claude" --alias widgets=acme__widgets --apply >/dev/null
tok4="$data4/projects/acme__widgets/tokens.jsonl"
check "live session: tokens still backfilled" '[ -s "$tok4" ] && grep -q "\"in\": 120" "$tok4"'
check "live session: log NOT duplicated"      '! grep -q "BACKFILLED" "$livelog/widgets.session1.md" && grep -c "## 10:00:00" "$livelog/widgets.session1.md" | grep -qx 1'

# Per-turn (not per-session) dedup: a sidecar already holding ONE (session, ts) must still
# gain that session's OTHER turns on import, deduping only the exact (session, ts) present.
# Per-session dedup would skip the whole session and miss turns it didn't yet have.
data6="$(mktemp -d)"; trap 'rm -rf "$claude" "$data" "$data2" "$data3" "$data4" "$data5" "$data6"' EXIT
mkdir -p "$data6/projects/acme__widgets"
printf '%s\n' '{"ts": "2026-05-20T09:00:00Z", "session": "session1", "model": "claude-opus-4-8", "in": 1, "out": 1, "cache_create": 0, "cache_read": 0, "auto": false}' > "$data6/projects/acme__widgets/tokens.jsonl"
python3 "$IMPORT" --data "$data6" --claude "$claude" --alias widgets=acme__widgets --tokens-only --apply >/dev/null
tok6="$data6/projects/acme__widgets/tokens.jsonl"
check "per-turn dedup keeps seeded ts"  'grep -q "2026-05-20T09:00:00Z" "$tok6"'   # different ts, same session
check "per-turn dedup adds new turn ts"  'grep -q "2026-05-20T10:01:00Z" "$tok6"'   # the transcript turn, backfilled
check "per-turn dedup: two records"      '[ "$(wc -l < "$tok6")" -eq 2 ]'

# Killed-turn backfill (the orchestrator's teardown path): a worker worktree with no live
# remote and NO alias must route by path (match_known) and be marked auto=true.
dataK="$(mktemp -d)"; claudeK="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$data2" "$data3" "$data4" "$data5" "$data6" "$dataK" "$claudeK"' EXIT
mkdir -p "$dataK/projects/acme__widgets"        # makes "widgets" a KNOWN repo for match_known
sk="$claudeK/projects/-tmp-nightshift-widgets-w3"; mkdir -p "$sk"
{
  printf '%s\n' '{"type":"user","isSidechain":false,"timestamp":"2026-05-21T02:00:00.000Z","cwd":"/tmp/nightshift/widgets-w3","message":{"content":"/continue"}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-05-21T02:05:00.000Z","cwd":"/tmp/nightshift/widgets-w3","message":{"id":"msg_killed","model":"claude-opus-4-8","usage":{"input_tokens":500,"output_tokens":900,"cache_creation_input_tokens":0,"cache_read_input_tokens":40000},"content":[{"type":"text","text":"Drained a task. Done."}]}}'
} > "$sk/sessionK.jsonl"
python3 "$IMPORT" --data "$dataK" --claude "$claudeK" --tokens-only --apply >/dev/null   # NO --alias
tokK="$dataK/projects/acme__widgets/tokens.jsonl"
check "killed turn: routed to project by PATH (no alias)" '[ -s "$tokK" ] && grep -q "\"in\": 500" "$tokK"'
check "killed turn: marked auto (nightshift worker)"      'grep -q "\"auto\": true" "$tokK"'
check "killed turn: NOT pooled in miscellaneous"          '[ ! -e "$dataK/projects/miscellaneous/tokens.jsonl" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
