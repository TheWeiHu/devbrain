#!/usr/bin/env bash
# devbrain — `devbrain import` smoke test (parity suite for the GO importer).
# Builds a fake ~/.claude (a transcript with a prompt+response and a memory file
# with a secret), runs the importer, and checks the dry-run writes nothing while
# --apply mirrors logs + memory, redacted.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ROOT="$(dirname "$HERE")"
BIN="${DEVBRAIN_BIN:-$ROOT/devbrain}"
[ -x "$BIN" ] || { echo "skip: devbrain binary not built (go build -o devbrain ./cmd/devbrain)"; exit 0; }
import_py() { "$BIN" import "$@"; }   # was: python3 "$HERE/import.py" "$@"

claude="$(mktemp -d)"; data="$(mktemp -d)"; codex_empty="$(mktemp -d)"
export CODEX_HOME="$codex_empty"
trap 'rm -rf "$claude" "$data" "$codex_empty"' EXIT
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
import_py $common >/dev/null
check "dry-run writes nothing" '[ -z "$(find "$data" -type f 2>/dev/null)" ]'

# Without an alias the dead cwd is unresolved -> miscellaneous, and the dry-run prompts
# the setting-up agent to alias it (text, not code, does the judgment call).
noalias="$(import_py --data "$data" --claude "$claude" 2>/dev/null)"
check "unrouted history names the dir for the agent" 'printf "%s" "$noalias" | grep -q "AGENT:" && printf "%s" "$noalias" | grep -q "widgets"'

# Apply.
import_py $common --apply >/dev/null
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
import_py $common --apply >/dev/null
check "re-apply does not duplicate"  '[ "$(wc -l < "$tok")" -eq 1 ]'

# Per-message dedup: Claude Code writes one transcript LINE per content block, each
# repeating the same message-level usage. A turn whose response has 3 blocks must bill
# its usage ONCE (cache_read 7000), not 3× (21000). Same message.id across the 3 lines.
dataB="$(mktemp -d)"; claudeB="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$codex_empty" "$data2" "$data3" "$dataB" "$claudeB"' EXIT
sb="$claudeB/projects/-tmp-acme-widgets"; mkdir -p "$sb"
{
  printf '%s\n' '{"type":"user","isSidechain":false,"timestamp":"2026-05-20T10:00:00.000Z","cwd":"/tmp/acme/widgets","message":{"content":"split a response into blocks"}}'
  for blk in '{"type":"thinking","thinking":"hm"}' '{"type":"text","text":"All set. Done."}' '{"type":"tool_use","name":"Edit","input":{"file_path":"/tmp/acme/widgets/a.py"}}'; do
    printf '%s\n' '{"type":"assistant","timestamp":"2026-05-20T10:01:00.000Z","cwd":"/tmp/acme/widgets","message":{"id":"msg_dup1","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":7000},"content":['"$blk"']}}'
  done
} > "$sb/sessionB.jsonl"
import_py --data "$dataB" --claude "$claudeB" --alias widgets=acme__widgets --apply >/dev/null
tokB="$dataB/projects/acme__widgets/tokens.jsonl"
check "dedup: usage billed once, not per-block"  'grep -q "\"cache_read\": 7000" "$tokB" && ! grep -q "21000" "$tokB"'

# Sidechain/sub-agent entries stay inside the parent turn for token parity with live
# Stop capture. A trailing isSidechain user event must not become the turn boundary.
dataS="$(mktemp -d)"; claudeS="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$codex_empty" "$data2" "$data3" "$dataB" "$claudeB" "$dataS" "$claudeS"' EXIT
ss="$claudeS/projects/-tmp-acme-widgets"; mkdir -p "$ss"
{
  printf '%s\n' '{"type":"user","isSidechain":false,"timestamp":"2026-05-22T12:00:00.000Z","cwd":"/tmp/acme/widgets","message":{"content":"run parent import task"}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-05-22T12:00:10.000Z","cwd":"/tmp/acme/widgets","message":{"id":"msg_parent_a","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Started parent import work."}]}}'
  printf '%s\n' '{"type":"user","isSidechain":true,"timestamp":"2026-05-22T12:00:15.000Z","cwd":"/tmp/acme/widgets","message":{"content":"sub-agent prompt"}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-05-22T12:00:20.000Z","cwd":"/tmp/acme/widgets","message":{"id":"msg_side","model":"claude-opus-4-8","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Sub-agent imported context."}]}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-05-22T12:00:30.000Z","cwd":"/tmp/acme/widgets","message":{"id":"msg_parent_b","model":"claude-opus-4-8","usage":{"input_tokens":3,"output_tokens":4,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Finished parent import turn."}]}}'
} > "$ss/sessionS.jsonl"
import_py --data "$dataS" --claude "$claudeS" --alias widgets=acme__widgets --apply >/dev/null
logS="$(find "$dataS/projects/acme__widgets/log" -name '*.md' 2>/dev/null | head -1)"
tokS="$dataS/projects/acme__widgets/tokens.jsonl"
check "sidechain import writes one parent prompt" '[ "$(grep -c "^## 12:00:00" "$logS")" -eq 1 ]'
check "sidechain import recap uses parent final"  'grep -q "Finished parent import turn." "$logS"'
check "sidechain import tokens include whole turn" 'grep -q "\"in\": 14" "$tokS" && grep -q "\"out\": 26" "$tokS"'
check "sidechain import ts is final parent response" 'grep -q "\"ts\": \"2026-05-22T12:00:30Z\"" "$tokS"'

# Malformed/timestamp-less transcript events should not crash the transcript parse; real
# Claude transcripts carry timestamps, but the shared parser is defensive and import must
# preserve that: the turn falls back to the epoch instead of aborting the whole harvest.
dataM="$(mktemp -d)"; claudeM="$(mktemp -d)"
mkdir -p "$claudeM/projects/-tmp-acme-widgets"
{
  printf '%s\n' '{"type":"user","isSidechain":false,"cwd":"/tmp/acme/widgets","message":{"content":"timestamp missing"}}'
  printf '%s\n' '{"type":"assistant","cwd":"/tmp/acme/widgets","message":{"content":[{"type":"text","text":"Handled missing timestamp."}]}}'
} > "$claudeM/projects/-tmp-acme-widgets/missing-ts.jsonl"
import_py --data "$dataM" --claude "$claudeM" --alias widgets=acme__widgets --apply >/dev/null
logM="$dataM/projects/acme__widgets/log/1970-01-01/widgets.missing-ts.md"
check "timestamp-less transcript falls back instead of crashing" '[ -f "$logM" ] && grep -q "^## 00:00:00" "$logM" && grep -q "timestamp missing" "$logM"'
rm -rf "$dataM" "$claudeM"

# Route heal: a session whose transcript is still on disk gets its rows RE-DERIVED on
# import — a row stranded under a stale route (worktree deleted / remote now resolves
# elsewhere) is stripped and the turn re-lands under the current route, exactly once.
dataG="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$codex_empty" "$data2" "$data3" "$dataB" "$claudeB" "$dataS" "$claudeS" "$dataG"' EXIT
mkdir -p "$dataG/projects/miscellaneous"
printf '%s\n' '{"ts":"2026-05-20T10:01:00Z","session":"session1","model":"claude-opus-4-8","in":120,"out":340,"cache_create":0,"cache_read":7000,"auto":false}' > "$dataG/projects/miscellaneous/tokens.jsonl"
import_py --data "$dataG" --claude "$claude" --alias widgets=acme__widgets --tokens-only --apply >/dev/null
check "route heal: stale-route row stripped"  '! grep -q "session1" "$dataG/projects/miscellaneous/tokens.jsonl"'
check "route heal: turn re-lands once under the current route"  '[ "$(grep -c "session1" "$dataG/projects/acme__widgets/tokens.jsonl")" -eq 1 ]'

# Exclude opts a project out.
data2="$(mktemp -d)"; data3="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$codex_empty" "$data2" "$data3"' EXIT
import_py --data "$data2" --claude "$claude" --alias widgets=acme__widgets --exclude acme__widgets --apply >/dev/null
check "--exclude skips the project"  '[ -z "$(find "$data2/projects/acme__widgets" -type f 2>/dev/null)" ]'

# Persistent alias file ($DATA/import-aliases) routes the same way as --alias.
mkdir -p "$data3"
printf '%s\n' '# rename map' 'widgets=acme__widgets' > "$data3/import-aliases"
import_py --data "$data3" --claude "$claude" --apply >/dev/null
check "alias file routes the project" '[ -n "$(find "$data3/projects/acme__widgets/log" -name "*.md" 2>/dev/null | head -1)" ]'

# back-compat: the legacy hidden .import-aliases still routes when the visible one is absent.
dlegacy="$(mktemp -d)"
printf '%s\n' 'widgets=acme__widgets' > "$dlegacy/.import-aliases"
import_py --data "$dlegacy" --claude "$claude" --apply >/dev/null
check "legacy .import-aliases still routes" '[ -n "$(find "$dlegacy/projects/acme__widgets/log" -name "*.md" 2>/dev/null | head -1)" ]'
rm -rf "$dlegacy"

# --tokens-only: writes the token sidecar but NO prompt logs (cost-history backfill
# on an existing install without re-adding BACKFILLED logs).
data5="$(mktemp -d)"; trap 'rm -rf "$claude" "$data" "$data2" "$data3" "$data5"' EXIT
import_py --data "$data5" --claude "$claude" --alias widgets=acme__widgets --tokens-only --apply >/dev/null
check "tokens-only writes the sidecar"  '[ -s "$data5/projects/acme__widgets/tokens.jsonl" ]'
check "tokens-only writes NO logs"      '[ -z "$(find "$data5/projects/acme__widgets/log" -name "*.md" 2>/dev/null)" ]'

# LIVE session: a session already captured live (its log exists, no BACKFILLED banner)
# must STILL get its tokens backfilled — token logging is new, so a live log says nothing
# about whether tokens were recorded. The log harvest is skipped (no duplicate), the token
# sidecar is not.
data4="$(mktemp -d)"; trap 'rm -rf "$claude" "$data" "$data2" "$data3" "$data4" "$data5"' EXIT
livelog="$data4/projects/acme__widgets/log/2026-05-20"; mkdir -p "$livelog"
printf '# live\n\n## 10:00:00\n\nadd a healthcheck endpoint\n\n↳ 10:01:00 — pre-existing live recap\n\n' > "$livelog/widgets.session1.md"
import_py --data "$data4" --claude "$claude" --alias widgets=acme__widgets --apply >/dev/null
tok4="$data4/projects/acme__widgets/tokens.jsonl"
check "live session: tokens still backfilled" '[ -s "$tok4" ] && grep -q "\"in\": 120" "$tok4"'
check "live session: log NOT duplicated"      '! grep -q "BACKFILLED" "$livelog/widgets.session1.md" && grep -c "## 10:00:00" "$livelog/widgets.session1.md" | grep -qx 1'

# Replace: a session whose transcript is on disk is authoritative — a stale sidecar row the
# transcript can't reproduce (a partial Stop-hook capture of a turn that later grew) is
# stripped and only the re-derived complete rows remain. Rows of sessions whose transcripts
# were pruned are untouched (no transcript -> no replace; covered by the pruned path below).
data6="$(mktemp -d)"; trap 'rm -rf "$claude" "$data" "$data2" "$data3" "$data4" "$data5" "$data6"' EXIT
mkdir -p "$data6/projects/acme__widgets"
printf '%s\n' '{"ts": "2026-05-20T09:00:00Z", "session": "session1", "model": "claude-opus-4-8", "in": 1, "out": 1, "cache_create": 0, "cache_read": 0, "auto": false}' > "$data6/projects/acme__widgets/tokens.jsonl"
import_py --data "$data6" --claude "$claude" --alias widgets=acme__widgets --tokens-only --apply >/dev/null
tok6="$data6/projects/acme__widgets/tokens.jsonl"
check "replace: stale partial row stripped"  '! grep -q "2026-05-20T09:00:00Z" "$tok6"'
check "replace: transcript turn re-derived"  'grep -q "2026-05-20T10:01:00Z" "$tok6"'
check "replace: exactly the transcript rows" '[ "$(wc -l < "$tok6")" -eq 1 ]'
check "replace: rows carry the turn key"     'grep -q "\"turn\": \"2026-05-20T10:00:00Z\"" "$tok6"'

# Subagent transcripts: <dir>/<sid>/subagents/agent-*.jsonl are separate files the Stop
# hook never sees; import bills their turns to the PARENT session, one row per turn, with
# an agent-prefixed turn key so live SubagentStop captures dedup against the backfill.
data7="$(mktemp -d)"; trap 'rm -rf "$claude" "$data" "$data2" "$data3" "$data4" "$data5" "$data6" "$data7"' EXIT
mkdir -p "$slug/session1/subagents"
{
  printf '%s\n' '{"type":"user","isSidechain":true,"timestamp":"2026-05-20T10:00:30.000Z","cwd":"/tmp/acme/widgets","message":{"content":"scan the repo"}}'
  printf '%s\n' '{"type":"assistant","isSidechain":true,"timestamp":"2026-05-20T10:00:50.000Z","cwd":"/tmp/acme/widgets","message":{"id":"sa1","model":"claude-haiku-4-5","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":600},"content":[{"type":"text","text":"Scanned."}]}}'
} > "$slug/session1/subagents/agent-x1.jsonl"
import_py --data "$data7" --claude "$claude" --alias widgets=acme__widgets --tokens-only --apply >/dev/null
tok7="$data7/projects/acme__widgets/tokens.jsonl"
check "subagent turn billed to parent session" 'grep -q "\"turn\": \"agent-x1:2026-05-20T10:00:30Z\"" "$tok7" && [ "$(grep -c session1 "$tok7")" -eq 2 ]'
check "subagent re-import stays deduped"       'import_py --data "$data7" --claude "$claude" --alias widgets=acme__widgets --tokens-only --apply >/dev/null; [ "$(wc -l < "$tok7")" -eq 2 ]'
rm -rf "$slug/session1"   # keep the fixture single-transcript for the tests below

# Killed-turn backfill (the orchestrator's teardown path): a worker worktree with no live
# remote and NO alias must route by path (match_known) and be marked auto=true.
dataK="$(mktemp -d)"; claudeK="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$codex_empty" "$data2" "$data3" "$data4" "$data5" "$data6" "$dataS" "$claudeS" "$dataG" "$dataK" "$claudeK"' EXIT
mkdir -p "$dataK/projects/acme__widgets"        # makes "widgets" a KNOWN repo for match_known
sk="$claudeK/projects/-tmp-nightshift-widgets-w3"; mkdir -p "$sk"
{
  printf '%s\n' '{"type":"user","isSidechain":false,"timestamp":"2026-05-21T02:00:00.000Z","cwd":"/tmp/nightshift/widgets-w3","message":{"content":"/continue"}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-05-21T02:05:00.000Z","cwd":"/tmp/nightshift/widgets-w3","message":{"id":"msg_killed","model":"claude-opus-4-8","usage":{"input_tokens":500,"output_tokens":900,"cache_creation_input_tokens":0,"cache_read_input_tokens":40000},"content":[{"type":"text","text":"Drained a task. Done."}]}}'
} > "$sk/sessionK.jsonl"
import_py --data "$dataK" --claude "$claudeK" --tokens-only --apply >/dev/null   # NO --alias
tokK="$dataK/projects/acme__widgets/tokens.jsonl"
check "killed turn: routed to project by PATH (no alias)" '[ -s "$tokK" ] && grep -q "\"in\": 500" "$tokK"'
check "killed turn: marked auto (nightshift worker)"      'grep -q "\"auto\": true" "$tokK"'
check "killed turn: NOT pooled in miscellaneous"          '[ ! -e "$dataK/projects/miscellaneous/tokens.jsonl" ]'

# Codex stores one token_count event per model request under ~/.codex/sessions, but
# devbrain's token sidecar is one row per user turn, same as Claude and live Stop capture.
# Import must aggregate request usage by turn and replace older partial Codex rows.
dataC="$(mktemp -d)"; claudeC="$(mktemp -d)"; codexC="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$codex_empty" "$data2" "$data3" "$data4" "$data5" "$data6" "$dataS" "$claudeS" "$dataG" "$dataK" "$claudeK" "$dataC" "$claudeC" "$codexC"' EXIT
mkdir -p "$dataC/projects/acme__widgets" "$codexC/sessions/2026/06/30"
printf '%s\n' '{"ts":"2026-06-30T10:09:00Z","session":"codex-session","model":"gpt-5.5","in":999,"out":999,"cache_create":0,"cache_read":999,"auto":false}' > "$dataC/projects/acme__widgets/tokens.jsonl"
{
  printf '%s\n' '{"timestamp":"2026-06-30T10:00:00.000Z","type":"session_meta","payload":{"id":"codex-session","cwd":"/tmp/acme/widgets"}}'
  printf '%s\n' '{"timestamp":"2026-06-30T10:00:01.000Z","type":"turn_context","payload":{"turn_id":"turn-a","model":"gpt-5.5","cwd":"/tmp/acme/widgets"}}'
  printf '%s\n' '{"timestamp":"2026-06-30T10:00:01.500Z","type":"event_msg","payload":{"type":"user_message","message":"do codex work"}}'
  printf '%s\n' '{"timestamp":"2026-06-30T10:00:02.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":5,"total_tokens":105}}}}'
  printf '%s\n' '{"timestamp":"2026-06-30T10:00:03.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":120,"cached_input_tokens":50,"output_tokens":7,"total_tokens":127}}}}'
  printf '%s\n' '{"timestamp":"2026-06-30T10:00:04.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-a","last_agent_message":"Codex work done.","completed_at":1782813604}}'
} > "$codexC/sessions/2026/06/30/rollout-2026-06-30T10-00-00-codex-session.jsonl"
import_py --data "$dataC" --claude "$claudeC" --codex "$codexC" --alias widgets=acme__widgets --tokens-only --apply >/dev/null
tokC="$dataC/projects/acme__widgets/tokens.jsonl"
check "codex token backfill writes one turn row" '[ "$(wc -l < "$tokC")" -eq 1 ] && grep -q "\"in\": 130" "$tokC" && grep -q "\"out\": 12" "$tokC"'
check "codex token backfill replaces stale partial rows" '! grep -q "999" "$tokC"'
check "codex token backfill carries cached input" 'grep -q "\"cache_read\": 90" "$tokC"'

# Codex sessions were never captured live (their UserPromptSubmit hook is newer) and no
# other path imports their prompts, so import must also harvest the LOG (prompt + response
# + tools) from the transcript — including the Skill:<name> a `$skill` invocation records.
dataL="$(mktemp -d)"; claudeL="$(mktemp -d)"; codexL="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$codex_empty" "$data2" "$data3" "$data4" "$data5" "$data6" "$dataS" "$claudeS" "$dataG" "$dataK" "$claudeK" "$dataC" "$claudeC" "$codexC" "$dataL" "$claudeL" "$codexL"' EXIT
mkdir -p "$dataL/projects/acme__widgets" "$codexL/sessions/2026/06/30"
{
  printf '%s\n' '{"timestamp":"2026-06-30T12:00:00.000Z","type":"session_meta","payload":{"id":"codexlog","cwd":"/tmp/acme/widgets"}}'
  printf '%s\n' '{"timestamp":"2026-06-30T12:00:01.000Z","type":"turn_context","payload":{"turn_id":"turn-a","model":"gpt-5.5","cwd":"/tmp/acme/widgets"}}'
  printf '%s\n' '{"timestamp":"2026-06-30T12:00:01.500Z","type":"event_msg","payload":{"type":"user_message","message":"$distill"}}'
  printf '%s\n' '{"timestamp":"2026-06-30T12:00:01.700Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<skill>\n<name>distill</name>\n<path>/x/distill/SKILL.md</path>\n"}]}}'
  printf '%s\n' '{"timestamp":"2026-06-30T12:00:02.000Z","type":"event_msg","payload":{"type":"exec_command_begin"}}'
  printf '%s\n' '{"timestamp":"2026-06-30T12:00:03.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":5,"total_tokens":105}}}}'
  printf '%s\n' '{"timestamp":"2026-06-30T12:00:04.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-a","last_agent_message":"Folded the log into the brain.","completed_at":1782820804}}'
} > "$codexL/sessions/2026/06/30/rollout-2026-06-30T12-00-00-codexlog.jsonl"
import_py --data "$dataL" --claude "$claudeL" --codex "$codexL" --alias widgets=acme__widgets --apply >/dev/null
logL="$dataL/projects/acme__widgets/log/2026-06-30/widgets.codexlog.md"
check "codex log imported"              '[ -f "$logL" ]'
check "codex log has the typed prompt"  'grep -q "^\$distill$" "$logL"'
check "codex log names the skill run"   'grep -q "tools: Skill:distill" "$logL"'
check "codex log has recap"             'grep -q "Folded the log into the brain." "$logL"'
check "codex log marked BACKFILLED"     'grep -q "BACKFILLED" "$logL"'

# Per-DAY (not per-session) log backfill: a session captured live for only SOME of its days
# must still get its MISSING days backfilled from the transcript. The old per-session guard froze
# a multi-day session at its one live day (real case: 108 token-turns but 5 logged prompts). The
# log harvest now gates on (session, DAY), not the session UUID.
claudeD="$(mktemp -d)"; dataD="$(mktemp -d)"
slugD="$claudeD/projects/-tmp-acme-widgets"; mkdir -p "$slugD"
{
  printf '%s\n' '{"type":"user","isSidechain":false,"timestamp":"2026-05-20T10:00:00.000Z","cwd":"/tmp/acme/widgets","message":{"content":"day one prompt"}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-05-20T10:01:00.000Z","cwd":"/tmp/acme/widgets","message":{"model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":100},"content":[{"type":"text","text":"Did day one. Done."}]}}'
  printf '%s\n' '{"type":"user","isSidechain":false,"timestamp":"2026-05-21T10:00:00.000Z","cwd":"/tmp/acme/widgets","message":{"content":"day two prompt"}}'
  printf '%s\n' '{"type":"assistant","timestamp":"2026-05-21T10:01:00.000Z","cwd":"/tmp/acme/widgets","message":{"model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":100},"content":[{"type":"text","text":"Did day two. Done."}]}}'
} > "$slugD/multi.jsonl"
liveD="$dataD/projects/acme__widgets/log/2026-05-20"; mkdir -p "$liveD"   # live log exists for day ONE only
printf '# live\n\n## 10:00:00\n\nday one prompt\n\n↳ 10:01:00 — live day-one recap\n\n' > "$liveD/widgets.multi.md"
import_py --data "$dataD" --claude "$claudeD" --alias widgets=acme__widgets --apply >/dev/null
dayTwo="$dataD/projects/acme__widgets/log/2026-05-21/widgets.multi.md"
check "per-day: missing day backfilled from transcript" '[ -f "$dayTwo" ] && grep -q "day two prompt" "$dayTwo" && grep -q "BACKFILLED" "$dayTwo"'
check "per-day: live day left untouched"                '! grep -q "BACKFILLED" "$liveD/widgets.multi.md" && grep -q "live day-one recap" "$liveD/widgets.multi.md"'
rm -rf "$claudeD" "$dataD"

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
