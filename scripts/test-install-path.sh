#!/usr/bin/env bash
# devbrain — install.sh PATH-wiring tests. Guards the regression where `devbrain`
# was "command not found" after install because ~/.local/bin isn't on PATH by
# default (macOS). Runs install.sh against a throwaway HOME + data repo; no
# services, no network.
set -u

REPO="$(cd "$(dirname "$0")/.." && pwd)"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not available"; exit 0; }
PYDIR="$(dirname "$(command -v python3)")"

pass=0; fail=0
check() { if eval "$2"; then echo "  ok   — $1"; pass=$((pass+1)); else echo "  FAIL — $1"; fail=$((fail+1)); fi; }

# Run install.sh in a sandboxed HOME with ~/.local/bin deliberately OFF PATH.
run_install() { # $1=HOME  $2..=extra env assignments / flags
  local home="$1"; shift
  mkdir -p "$home/data/projects"; git init -q "$home/data"
  env -i HOME="$home" PATH="/usr/bin:/bin:$PYDIR" SHELL=/bin/zsh \
    DEVBRAIN_DATA="$home/data" "$@" bash "$REPO/scripts/install.sh" --only capture </dev/null >/dev/null 2>&1
}

# 1. default: adds the export to ~/.zshrc and the symlink exists
SB="$(mktemp -d)"
run_install "$SB"
check "adds export to ~/.zshrc"          'grep -qF '\''export PATH="$HOME/.local/bin:$PATH"'\'' "$SB/.zshrc"'
check "devbrain symlink installed"        '[ -L "$SB/.local/bin/devbrain" ]'

# 2. idempotent: a second run does not duplicate the line
run_install "$SB"
check "idempotent (one export line)"      '[ "$(grep -c "export PATH" "$SB/.zshrc")" = 1 ]'

# 3. uninstall reverses it
env -i HOME="$SB" PATH="/usr/bin:/bin:$PYDIR" SHELL=/bin/zsh bash "$REPO/scripts/uninstall.sh" </dev/null >/dev/null 2>&1
check "uninstall removes the marker"      '! grep -q "devbrain installer" "$SB/.zshrc" 2>/dev/null'

# 4. opt-out: DEVBRAIN_NO_PATH=1 never touches the rc
SB2="$(mktemp -d)"
run_install "$SB2" DEVBRAIN_NO_PATH=1
check "DEVBRAIN_NO_PATH skips rc edit"    '[ ! -f "$SB2/.zshrc" ]'

# 5. already-on-PATH: no rc written when ~/.local/bin is already on PATH
SB3="$(mktemp -d)"; mkdir -p "$SB3/data/projects" "$SB3/.local/bin"; git init -q "$SB3/data"
env -i HOME="$SB3" PATH="$SB3/.local/bin:/usr/bin:/bin:$PYDIR" SHELL=/bin/zsh \
  DEVBRAIN_DATA="$SB3/data" bash "$REPO/scripts/install.sh" --only capture </dev/null >/dev/null 2>&1
check "no rc edit when already on PATH"    '[ ! -f "$SB3/.zshrc" ]'

rm -rf "$SB" "$SB2" "$SB3"
echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
