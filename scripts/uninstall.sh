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

# 2. Drop the capture hook entries (UserPromptSubmit + Stop; backup first).
if [ -f "$settings" ] && command -v jq >/dev/null; then
  cp "$settings" "$settings.bak.$(date +%s)"
  tmp="$(mktemp)"
  jq --arg prompt "$BIN/devbrain-capture.sh" --arg resp "$BIN/devbrain-capture-response.sh" '
    (if .hooks.UserPromptSubmit then
      .hooks.UserPromptSubmit |= map(select(((.hooks // [])[]?.command) != $prompt))
    else . end) |
    (if .hooks.Stop then
      .hooks.Stop |= map(select(((.hooks // [])[]?.command) != $resp))
    else . end)
  ' "$settings" > "$tmp" && mv "$tmp" "$settings"
  echo "removed UserPromptSubmit + Stop hooks from $settings"
fi

# 3. Remove installed scripts.
rm -f "$BIN/devbrain-capture.sh" "$BIN/devbrain-capture-response.sh" \
      "$BIN/devbrain-flush.sh" "$BIN/devbrain-rebuild.sh" && echo "removed installed scripts"

# 4. Remove installed skills.
rm -rf "$CLAUDE/skills/continue" "$CLAUDE/skills/distill" && echo "removed /continue and /distill skills"

# 5. Strip the devbrain block from ~/.claude/CLAUDE.md.
md="$CLAUDE/CLAUDE.md"
if [ -f "$md" ]; then
  tmp="$(mktemp)"
  awk -v s="<!-- devbrain:start -->" -v e="<!-- devbrain:end -->" '
    $0==s {skip=1} !skip {print} $0==e {skip=0}
  ' "$md" > "$tmp" && mv "$tmp" "$md"
  echo "removed devbrain block from $md"
fi

echo "Done. The data repo (~/devbrain-data) was left untouched."
