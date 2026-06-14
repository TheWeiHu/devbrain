#!/usr/bin/env bash
# devbrain — remove the machine wiring installed by install.sh.
# Leaves the data repo and its contents untouched.
set -uo pipefail

CLAUDE="$HOME/.claude"
BIN="$CLAUDE/hooks"
settings="$CLAUDE/settings.json"
plist="$HOME/Library/LaunchAgents/com.devbrain.flush.plist"

# 1. Stop + remove the flusher LaunchAgent.
launchctl unload "$plist" 2>/dev/null || true
rm -f "$plist" && echo "removed flusher LaunchAgent"

# 2. Drop the UserPromptSubmit hook entry (backup first).
if [ -f "$settings" ] && command -v jq >/dev/null; then
  cp "$settings" "$settings.bak.$(date +%s)"
  tmp="$(mktemp)"
  jq --arg cmd "$BIN/devbrain-capture.sh" '
    if .hooks.UserPromptSubmit then
      .hooks.UserPromptSubmit |= map(select(((.hooks // [])[]?.command) != $cmd))
    else . end
  ' "$settings" > "$tmp" && mv "$tmp" "$settings"
  echo "removed UserPromptSubmit hook from $settings"
fi

# 3. Remove installed scripts.
rm -f "$BIN/devbrain-capture.sh" "$BIN/devbrain-flush.sh" && echo "removed installed scripts"

echo "Done. The data repo (~/devbrain-data) was left untouched."
