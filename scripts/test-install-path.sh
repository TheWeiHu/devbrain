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

# 6. Empty/unknown SHELL: must not abort under set -u (the ${SHELL:-} guard) and
#    falls back to ~/.profile. (bash repopulates an *unset* SHELL, so an empty
#    string is the reachable variant of the codex set-u concern.)
SB4="$(mktemp -d)"; mkdir -p "$SB4/data/projects"; git init -q "$SB4/data"
env -i HOME="$SB4" PATH="/usr/bin:/bin:$PYDIR" SHELL= DEVBRAIN_DATA="$SB4/data" \
  bash "$REPO/scripts/install.sh" --only capture </dev/null >/dev/null 2>&1
check "empty SHELL: no abort, uses ~/.profile" '[ -L "$SB4/.local/bin/devbrain" ] && grep -q "export PATH" "$SB4/.profile"'

# 7. bash with no ~/.bash_profile: writes a login file, NOT ~/.bashrc
SB5="$(mktemp -d)"; mkdir -p "$SB5/data/projects"; git init -q "$SB5/data"
env -i HOME="$SB5" PATH="/usr/bin:/bin:$PYDIR" SHELL=/bin/bash DEVBRAIN_DATA="$SB5/data" \
  bash "$REPO/scripts/install.sh" --only capture </dev/null >/dev/null 2>&1
check "bash login: writes .bash_profile not .bashrc" '[ -f "$SB5/.bash_profile" ] && [ ! -f "$SB5/.bashrc" ]'

# 8. queue's pricing module lands beside the installed queue and imports. queue.py has a
#    top-level `from model_pricing import ...`; if install.sh doesn't copy the module into
#    $BIN, `devbrain queue` dies with ModuleNotFoundError before the server can start.
SB6="$(mktemp -d)"; run_install "$SB6"; NSB="$SB6/.claude/hooks"
check "model_pricing.py installed beside queue" '[ -f "$NSB/model_pricing.py" ] && [ -f "$NSB/devbrain-queue.py" ]'
check "installed queue imports model_pricing"   'env -i PATH="/usr/bin:/bin:$PYDIR" python3 -c "import sys;sys.path.insert(0,\"$NSB\");import model_pricing;assert model_pricing.MODEL_PRICING" 2>/dev/null'
env -i HOME="$SB6" PATH="/usr/bin:/bin:$PYDIR" SHELL=/bin/zsh bash "$REPO/scripts/uninstall.sh" </dev/null >/dev/null 2>&1
check "uninstall removes model_pricing.py"       '[ ! -f "$NSB/model_pricing.py" ]'

rm -rf "$SB" "$SB2" "$SB3" "$SB4" "$SB5" "$SB6"
echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
