#!/usr/bin/env bash
# devbrain — install wiring/PATH tests, updated for the Go installer. The old
# copy-install model (script copies in ~/.claude/hooks + a shell-rc PATH line)
# is dead: settings.json now points straight at the devbrain binary, config.json
# carries the data home, and fresh installs never touch shell rc files (brew
# owns PATH). Legacy rc lines are still REMOVED by the migration sweep. Runs
# install through the shim against a throwaway HOME; no services, no network.
set -u

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${DEVBRAIN_BIN:-$REPO/devbrain}"
[ -x "$BIN" ] || { echo "skip: devbrain binary not built (go build -o devbrain ./cmd/devbrain)"; exit 0; }
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not available"; exit 0; }
PYDIR="$(dirname "$(command -v python3)")"

pass=0; fail=0
check() { if eval "$2"; then echo "  ok   — $1"; pass=$((pass+1)); else echo "  FAIL — $1"; fail=$((fail+1)); fi; }

# Run install in a sandboxed HOME with ~/.local/bin deliberately OFF PATH.
run_install() { # $1=HOME  $2..=extra env assignments / flags
  local home="$1"; shift
  mkdir -p "$home/data/projects"; git init -q "$home/data"
  env -i HOME="$home" PATH="/usr/bin:/bin:$PYDIR" SHELL=/bin/zsh \
    DEVBRAIN_BIN="$BIN" DEVBRAIN_NO_IMPORT=1 \
    DEVBRAIN_DATA="$home/data" "$@" "${DEVBRAIN_BIN:-$REPO/devbrain}" install --only capture </dev/null >/dev/null 2>&1
}

# 1. default: settings.json points at the BINARY (was: rc-file export + hook copies)
SB="$(mktemp -d)"
run_install "$SB"
check "settings.json points at the binary"  'grep -qF "$BIN hook capture" "$SB/.claude/settings.json"'
check "no shell rc written (brew owns PATH)" '[ ! -f "$SB/.zshrc" ] && [ ! -f "$SB/.bash_profile" ] && [ ! -f "$SB/.bashrc" ]'
check "no hook copies under ~/.claude/hooks" '! ls "$SB/.claude/hooks/devbrain-"* >/dev/null 2>&1'
check "config.json records the data home"    'grep -qF "$SB/data" "$SB/.config/devbrain/config.json"'
check "back-compat alias symlinks installed" '[ -L "$SB/.local/bin/devbrain-todo" ] && [ -L "$SB/.local/bin/devbrain-import" ]'

# 2. idempotent: a second run does not duplicate the hook entry
run_install "$SB"
check "idempotent (one capture entry)"       '[ "$(grep -c "hook capture" "$SB/.claude/settings.json")" = 1 ]'

# 3. uninstall reverses it (data repo untouched)
env -i HOME="$SB" PATH="/usr/bin:/bin:$PYDIR" SHELL=/bin/zsh DEVBRAIN_BIN="$BIN" \
  "${DEVBRAIN_BIN:-$REPO/devbrain}" uninstall </dev/null >/dev/null 2>&1
check "uninstall drops the hook entry"       '! grep -qF "hook capture" "$SB/.claude/settings.json"'
check "uninstall removes config.json"        '[ ! -f "$SB/.config/devbrain/config.json" ]'
check "uninstall removes alias symlinks"     '[ ! -e "$SB/.local/bin/devbrain-todo" ]'
check "uninstall keeps the data repo"        '[ -d "$SB/data/.git" ]'

# 4. migration: a legacy rc PATH line ("# added by devbrain installer" + export)
#    is removed by the sweep — the one shell-rc behavior that survives.
SB2="$(mktemp -d)"
printf '# my stuff\n\n# added by devbrain installer\nexport PATH="$HOME/.local/bin:$PATH"\n' > "$SB2/.zshrc"
run_install "$SB2"
check "legacy rc marker+export removed"      '! grep -q "devbrain installer" "$SB2/.zshrc" && ! grep -q "local/bin" "$SB2/.zshrc"'
check "user rc content preserved"            'grep -qF "# my stuff" "$SB2/.zshrc"'

# 5. migration: a legacy sed-pinned capture copy seeds config.json with its
#    pinned data path (recovered BEFORE the copy is deleted).
SB3="$(mktemp -d)"; mkdir -p "$SB3/.claude/hooks" "$SB3/olddata/projects"; git init -q "$SB3/olddata"
printf '#!/bin/bash\nDATA="${DEVBRAIN_DATA:-%s}"\n' "$SB3/olddata" > "$SB3/.claude/hooks/devbrain-capture.sh"
mkdir -p "$SB3/data/projects"; git init -q "$SB3/data"   # unused default; pinned path must win
env -i HOME="$SB3" PATH="/usr/bin:/bin:$PYDIR" SHELL=/bin/zsh DEVBRAIN_BIN="$BIN" DEVBRAIN_NO_IMPORT=1 \
  "${DEVBRAIN_BIN:-$REPO/devbrain}" install --only capture </dev/null >/dev/null 2>&1
check "pinned data path seeds config.json"   'grep -qF "$SB3/olddata" "$SB3/.config/devbrain/config.json"'
check "legacy capture copy deleted"          '[ ! -f "$SB3/.claude/hooks/devbrain-capture.sh" ]'

# 6. component scoping: --only capture must not install skills or the flusher.
check "--only capture: no skills"            '[ ! -d "$SB/.claude/skills/continue" ]'
check "--only capture: no flusher plist"     '[ ! -f "$SB/Library/LaunchAgents/com.devbrain.flush.plist" ]'

rm -rf "$SB" "$SB2" "$SB3"
echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
