#!/usr/bin/env bash
# devbrain — backfill-skill-names.py tests. Builds a fake captured log (bare Skill×N in
# the tools meta, plus a response-sample line that QUOTES "Skill×1" as prose) and a fake
# transcript, then checks the backfill names the meta token from the transcript without
# touching the prose, is order-correct, idempotent, and honest when no transcript exists.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; SCRIPT="$HERE/backfill-skill-names.py"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not installed"; exit 0; }

DATA="$(mktemp -d)"; CLAUDE="$(mktemp -d)"
trap 'rm -rf "$DATA" "$CLAUDE"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

sid="aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
logdir="$DATA/projects/proj__x/log/2026-06-01"; mkdir -p "$logdir"
LOG="$logdir/wt.$sid.md"
# Two skill turns (distill, then codex) + a prose line quoting Skill×1 that must NOT change.
cat > "$LOG" <<EOF
# proj log

## 09:00:00

ok distill?

↳ 09:01 — distilled the session
   touched: a.md  ·  tools: Skill×1, Bash×3

## 09:10:00

do a codex review

↳ 09:11 — ran codex review
   tools: Skill×1, Read×2
   ⤷ response sample:
   > I recorded tools: Skill×1 in the meta for that turn.
EOF

# Matching transcript: distill THEN codex (chronological order the backfill consumes).
txdir="$CLAUDE/projects/-Users-x-proj"; mkdir -p "$txdir"
cat > "$txdir/$sid.jsonl" <<EOF
{"type":"user","message":{"content":[{"type":"text","text":"ok distill?"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"distill"}}]}}
{"type":"user","message":{"content":[{"type":"text","text":"do a codex review"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"codex"}}]}}
EOF

out="$(python3 "$SCRIPT" --data "$DATA" --claude "$CLAUDE" 2>&1)"
check "reports 2 tokens named"        'printf "%s" "$out" | grep -q "named 2 bare"'
check "summary lists recovered names" 'printf "%s" "$out" | grep -q "distill" && printf "%s" "$out" | grep -q "codex"'
check "first meta named distill"      'grep -q "tools: Skill:distill×1, Bash×3" "$LOG"'
check "second meta named codex"       'grep -q "tools: Skill:codex×1, Read×2" "$LOG"'
check "prose quote left untouched"    'grep -q "I recorded tools: Skill×1 in the meta" "$LOG"'
check "no bare meta token remains"    '! grep -E "^[[:space:]]+(touched:|tools:).*[^:]Skill×" "$LOG"'

# Idempotent: a second run names nothing and changes nothing.
before="$(md5 -q "$LOG" 2>/dev/null || md5sum "$LOG")"
out2="$(python3 "$SCRIPT" --data "$DATA" --claude "$CLAUDE" 2>&1)"
after="$(md5 -q "$LOG" 2>/dev/null || md5sum "$LOG")"
check "re-run names 0 tokens"         'printf "%s" "$out2" | grep -q "named 0 bare"'
check "re-run leaves file byte-identical" '[ "$before" = "$after" ]'

# No transcript on disk -> reported, left bare (never invents a name).
sid2="ffffffff-0000-1111-2222-333333333333"
LOG2="$logdir/wt.$sid2.md"
printf '# p\n\n## 10:00:00\n\nhello\n\n↳ 10:01 — x\n   tools: Skill×1, Bash×1\n' > "$LOG2"
out3="$(python3 "$SCRIPT" --data "$DATA" --claude "$CLAUDE" 2>&1)"
check "no-transcript log reported"    'printf "%s" "$out3" | grep -q "no transcript on disk"'
check "no-transcript meta left bare"  'grep -q "tools: Skill×1, Bash×1" "$LOG2"'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
