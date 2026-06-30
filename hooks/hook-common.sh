#!/usr/bin/env bash
# Shared hook bootstrap. Sourced by installed hooks and checkout-run hooks.

devbrain_hook_dir() {
  cd "$(dirname "${BASH_SOURCE[1]:-${BASH_SOURCE[0]:-$0}}")" 2>/dev/null && pwd
}

DEVBRAIN_HOOK_DIR="${DEVBRAIN_HOOK_DIR:-$(devbrain_hook_dir)}"
DEVBRAIN_LIB="$DEVBRAIN_HOOK_DIR/devbrain_lib.py"
[ -f "$DEVBRAIN_LIB" ] || DEVBRAIN_LIB="$HOME/.claude/hooks/devbrain_lib.py"

devbrain_has_python_lib() {
  command -v python3 >/dev/null 2>&1 && [ -f "$DEVBRAIN_LIB" ]
}

devbrain_read_event() {
  devbrain_has_python_lib || return 0
  printf '%s' "${payload:-}" | python3 "$DEVBRAIN_LIB" read-event "$1" 2>/dev/null
}

devbrain_source_project_key() {
  local c
  for c in "$DEVBRAIN_HOOK_DIR/devbrain-project-key.sh" \
           "$DEVBRAIN_HOOK_DIR/project-key.sh" \
           "$DEVBRAIN_HOOK_DIR/../hooks/project-key.sh" \
           "$HOME/.claude/hooks/devbrain-project-key.sh"; do
    [ -f "$c" ] && { . "$c"; return 0; }
  done
  return 1
}

devbrain_sanitize() {
  printf '%s' "$1" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-'
}

devbrain_worktree_slug() {
  local cwd="$1" toplevel worktree
  toplevel="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)"
  worktree="$(basename "${toplevel:-$cwd}")"
  worktree="$(devbrain_sanitize "$worktree")"
  printf '%s' "${worktree:-unknown}"
}
