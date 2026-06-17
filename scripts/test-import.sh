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
  printf '%s\n' '{"type":"assistant","timestamp":"2026-05-20T10:01:00.000Z","cwd":"/tmp/acme/widgets","message":{"content":[{"type":"text","text":"Added /healthz returning 200. Wired it into the router. Done."},{"type":"tool_use","name":"Edit","input":{"file_path":"/tmp/acme/widgets/app.py"}}]}}'
} > "$slug/session1.jsonl"
# A memory file with a FAKE secret — the bait for the redaction assertion below.
# `sk-abc…` is a dummy (not a real key) shaped to match the importer's sk-[…]{20,}
# pattern, so the test can prove tokens are scrubbed to [REDACTED] before anything is
# written to the (pushed) data repo.
{
  printf '%s\n' '---' 'name: deploy-note' 'type: reference' '---'
  printf '%s\n' 'Deploy via git only. Token sk-abcdefghijklmnopqrstuvwxyz0123 must be scrubbed.'
} > "$slug/memory/reference_deploy.md"

# Route the dead cwd (basename "widgets") deterministically with an alias.
# --no-gh keeps the test hermetic (no network / real `gh` calls).
common="--data $data --claude $claude --roots $claude --alias widgets=acme__widgets --no-gh"

# Dry-run writes nothing.
python3 "$IMPORT" $common >/dev/null
check "dry-run writes nothing" '[ -z "$(find "$data" -type f 2>/dev/null)" ]'

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

# Exclude opts a project out.
data2="$(mktemp -d)"; data3="$(mktemp -d)"
trap 'rm -rf "$claude" "$data" "$data2" "$data3"' EXIT
python3 "$IMPORT" --data "$data2" --claude "$claude" --roots "$claude" --alias widgets=acme__widgets --no-gh --exclude acme__widgets --apply >/dev/null
check "--exclude skips the project"  '[ -z "$(find "$data2/projects/acme__widgets" -type f 2>/dev/null)" ]'

# Persistent alias file ($DATA/.import-aliases) routes the same way as --alias.
mkdir -p "$data3"
printf '%s\n' '# rename map' 'widgets=acme__widgets' > "$data3/.import-aliases"
python3 "$IMPORT" --data "$data3" --claude "$claude" --roots "$claude" --no-gh --apply >/dev/null
check "alias file routes the project" '[ -n "$(find "$data3/projects/acme__widgets/log" -name "*.md" 2>/dev/null | head -1)" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
