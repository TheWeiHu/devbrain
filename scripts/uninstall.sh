#!/usr/bin/env bash
# devbrain — remove the machine wiring installed by install.sh.
# Leaves the data repo and its contents untouched.
set -uo pipefail

CLAUDE="$HOME/.claude"
BIN="$CLAUDE/hooks"
settings="$CLAUDE/settings.json"
plist="$HOME/Library/LaunchAgents/com.devbrain.flush.plist"

# 1. Stop + remove the flusher (launchd on macOS; systemd user timer / cron on Linux).
case "$(uname -s)" in
  Darwin)
    launchctl unload "$plist" 2>/dev/null || true
    rm -f "$plist" && echo "removed flusher LaunchAgent" ;;
  *)
    if command -v systemctl >/dev/null 2>&1; then
      systemctl --user disable --now devbrain-flush.timer >/dev/null 2>&1 || true
      rm -f "$HOME/.config/systemd/user/devbrain-flush.timer" "$HOME/.config/systemd/user/devbrain-flush.service"
      systemctl --user daemon-reload >/dev/null 2>&1 || true
      echo "removed systemd flush timer"
    fi
    command -v crontab >/dev/null 2>&1 && { crontab -l 2>/dev/null | grep -vF 'devbrain-flush.sh' | crontab - 2>/dev/null || true; } ;;
esac

# 2. Drop the capture hook entries (UserPromptSubmit + Stop + SessionEnd; backup first).
if [ -f "$settings" ] && command -v jq >/dev/null; then
  cp "$settings" "$settings.bak.$(date +%s)"
  tmp="$(mktemp)"
  jq --arg prompt "$BIN/devbrain-capture.sh" --arg resp "$BIN/devbrain-capture-response.sh" --arg mem "$BIN/devbrain-capture-memory.sh" '
    (if .hooks.UserPromptSubmit then
      .hooks.UserPromptSubmit |= map(select(((.hooks // [])[]?.command) != $prompt))
    else . end) |
    (if .hooks.Stop then
      .hooks.Stop |= map(select(((.hooks // [])[]?.command) != $resp))
    else . end) |
    (if .hooks.SessionEnd then
      .hooks.SessionEnd |= map(select(((.hooks // [])[]?.command) != $mem))
    else . end)
  ' "$settings" > "$tmp" && mv "$tmp" "$settings"
  echo "removed UserPromptSubmit + Stop + SessionEnd hooks from $settings"
fi

# 3. Remove installed scripts.
rm -f "$BIN/devbrain_lib.py" "$BIN/devbrain-project-key.sh" "$BIN/devbrain-capture.sh" \
      "$BIN/devbrain-capture-response.sh" "$BIN/devbrain-capture-memory.sh" "$BIN/devbrain-flush.sh" \
      "$BIN/devbrain-rebuild.sh" "$BIN/devbrain-todo.sh" "$BIN/devbrain-import" && echo "removed installed scripts"
DBBIN="${DEVBRAIN_BIN:-$HOME/.local/bin}"
rm -f "$DBBIN/devbrain-todo" "$DBBIN/devbrain-import"

# 4. Remove installed skills.
rm -rf "$CLAUDE/skills/continue" "$CLAUDE/skills/distill" \
       "$CLAUDE/skills/nightshift" "$CLAUDE/skills/reconcile" && echo "removed devbrain skills"

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
