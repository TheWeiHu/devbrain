#!/usr/bin/env bash
# devbrain — wire ~/.claude/CLAUDE.md to import the global preferences page.
#
# The clean, non-deprecated way to give Claude Code standing personal preferences is a
# single `@import` line in your USER memory (~/.claude/CLAUDE.md) pointing at a global
# preferences page in devbrain-data. /distill maintains that page — your durable,
# cross-project steers (design taste, scope, "don't regress", staging-not-prod, cost),
# with per-project `## <project>` subsections for project-specific rules. This script
# just ensures the one import line exists.
#
# Why user memory (not a repo file): it's loaded in every project, lives in your home
# dir (NEVER in a repo, so nothing is committed), and doesn't rely on the deprecated
# CLAUDE.local.md. Claude Code skips a missing @import, so this is safe to run before
# the page is ever created.
#
# Idempotent (adds the line once), preserves all other CLAUDE.md content, fails open.
# `--unlink` removes the managed lines (used by `devbrain uninstall`).
set -uo pipefail

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
CLAUDE_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
MEM="$CLAUDE_DIR/CLAUDE.md"

PAGE="$DATA/preferences/global.md"
# Show the import path with ~ when it's under $HOME — portable and readable.
disp="$PAGE"; case "$PAGE" in "$HOME"/*) disp="~/${PAGE#"$HOME"/}";; esac
IMPORT="@$disp"
MARK="<!-- devbrain: global preferences page (managed by /distill; \`devbrain uninstall\` removes) -->"

if [ "${1:-}" = "--unlink" ]; then
  [ -f "$MEM" ] || exit 0
  # `grep -v` exits 1 when it filters out every line; that's success here, so swallow it
  # (otherwise pipefail would abort and leave the import wired).
  { grep -vF "$MARK" "$MEM" 2>/dev/null || true; } | { grep -vF "$IMPORT" 2>/dev/null || true; } > "$MEM.tmp"
  mv "$MEM.tmp" "$MEM"
  echo "link-preferences: unwired from $MEM"
  exit 0
fi

mkdir -p "$CLAUDE_DIR" 2>/dev/null || true

if [ -f "$MEM" ] && grep -qF "$IMPORT" "$MEM"; then
  echo "link-preferences: already wired ($IMPORT)"
  exit 0
fi

# Append the marked import, preserving any existing memory content.
if [ -f "$MEM" ]; then
  { cat "$MEM"; printf '\n%s\n%s\n' "$MARK" "$IMPORT"; } > "$MEM.tmp"
else
  printf '%s\n%s\n' "$MARK" "$IMPORT" > "$MEM.tmp"
fi
mv "$MEM.tmp" "$MEM"
echo "link-preferences: wired $IMPORT into $MEM"
