#!/usr/bin/env bash
# devbrain — end-to-end install/uninstall exercise against a throwaway HOME.
# Full component set, stub `codex`/`claude` on PATH, schedulers stubbed
# (launchctl/systemctl/crontab) so the host machine is never touched. Asserts
# the complete wiring, then that uninstall reverses everything except the data
# repo. cwd is moved OUT of the checkout so git-gate skips (it would otherwise
# configure this repo — a host mutation).
set -u

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${DEVBRAIN_BIN:-$REPO/devbrain}"
[ -x "$BIN" ] || { echo "skip: devbrain binary not built (go build -o devbrain ./cmd/devbrain)"; exit 0; }

pass=0; fail=0
check() { if eval "$2"; then echo "  ok   — $1"; pass=$((pass+1)); else echo "  FAIL — $1"; fail=$((fail+1)); fi; }

SB="$(mktemp -d)"; trap 'rm -rf "$SB"' EXIT
mkdir -p "$SB/home" "$SB/stub"
for s in codex claude launchctl systemctl crontab loginctl; do
  printf '#!/bin/bash\necho "%s $*" >> "%s/stub-calls.log"\nexit 0\n' "$s" "$SB" > "$SB/stub/$s"
  chmod +x "$SB/stub/$s"
done

GITDIR="$(dirname "$(command -v git)")"
E="env -i HOME=$SB/home PATH=$SB/stub:/usr/bin:/bin:$GITDIR SHELL=/bin/zsh DEVBRAIN_NO_IMPORT=1"

# ── install --yes: the full default component set ─────────────────────────────
( cd "$SB" && $E "$BIN" install --yes ) > "$SB/install.log" 2>&1
check "install exits 0"               '[ "$?" -eq 0 ] || grep -q "Done." "$SB/install.log"'
S="$SB/home/.claude/settings.json"; CX="$SB/home/.codex/hooks.json"
check "data repo created"             '[ -d "$SB/home/devbrain-data/.git" ]'
check "config.json written"           'grep -qF "$SB/home/devbrain-data" "$SB/home/.config/devbrain/config.json"'
for h in capture gbrain response memory session-start; do
  check "claude hook $h registered"   "grep -qF '$BIN hook $h' '$S'"
done
for h in capture gbrain response session-start; do
  check "codex hook $h registered"    "grep -qF 'DEVBRAIN_HARNESS=codex $BIN hook $h' '$CX'"
done
check "codex feature enable invoked"  'grep -q "codex features enable hooks" "$SB/stub-calls.log"'
check "flusher scheduled (stubbed)"   'grep -qE "launchctl load|systemctl --user enable|crontab" "$SB/stub-calls.log"'
if [ "$(uname -s)" = Darwin ]; then
  P="$SB/home/Library/LaunchAgents/com.devbrain.flush.plist"
  check "plist runs '<binary> flush'"   'grep -qF "<string>$BIN</string>" "$P" && grep -qF "<string>flush</string>" "$P"'
  check "plist keeps 300s + RunAtLoad"  'grep -q "<integer>300</integer>" "$P" && grep -q "RunAtLoad" "$P"'
fi
for sk in continue distill work reconcile nightshift; do
  check "skill $sk installed (both roots)" '[ -f "$SB/home/.claude/skills/'$sk'/SKILL.md" ] && [ -f "$SB/home/.agents/skills/'$sk'/SKILL.md" ]'
done
check "skills carry no hook-copy paths" '! grep -rq "\.claude/hooks/devbrain-" "$SB/home/.claude/skills"'
check "CLAUDE.md devbrain block"      'grep -qF "<!-- devbrain:start -->" "$SB/home/.claude/CLAUDE.md"'
check "CLAUDE.md preferences @import" 'grep -qE "^@.*/preferences/global\.md$" "$SB/home/.claude/CLAUDE.md"'
check "codex AGENTS.md block"         'grep -qF "<!-- devbrain:start -->" "$SB/home/.codex/AGENTS.md"'
check "alias symlinks -> binary"      '[ "$(readlink "$SB/home/.local/bin/devbrain-todo")" = "$BIN" ]'
check "no shell rc mutation"          '[ ! -f "$SB/home/.zshrc" ] && [ ! -f "$SB/home/.profile" ]'

# re-run: idempotent
( cd "$SB" && $E "$BIN" install --yes ) >/dev/null 2>&1
check "second install idempotent"     '[ "$(grep -c "hook capture" "$S")" = 1 ]'

# ── uninstall: clean sweep, data repo intact ─────────────────────────────────
( cd "$SB" && $E "$BIN" uninstall ) > "$SB/uninstall.log" 2>&1
check "hooks gone from settings.json" '! grep -q "hook capture" "$S"'
check "hooks gone from codex"         '! grep -q "hook capture" "$CX"'
check "skills removed"                '[ ! -d "$SB/home/.claude/skills/continue" ] && [ ! -d "$SB/home/.agents/skills/continue" ]'
check "CLAUDE.md block stripped"      '! grep -q "devbrain:start" "$SB/home/.claude/CLAUDE.md"'
check "preferences import stripped"   '! grep -qE "^@.*/preferences/global\.md$" "$SB/home/.claude/CLAUDE.md"'
check "AGENTS.md block stripped"      '! grep -q "devbrain:start" "$SB/home/.codex/AGENTS.md"'
check "config.json removed"           '[ ! -f "$SB/home/.config/devbrain/config.json" ]'
check "alias symlinks removed"        '[ ! -e "$SB/home/.local/bin/devbrain-todo" ]'
if [ "$(uname -s)" = Darwin ]; then
  check "flusher plist removed"       '[ ! -f "$SB/home/Library/LaunchAgents/com.devbrain.flush.plist" ]'
fi
check "data repo untouched"           '[ -d "$SB/home/devbrain-data/.git" ]'
check "uninstall prints the brew note" 'grep -q "brew uninstall devbrain" "$SB/uninstall.log"'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
