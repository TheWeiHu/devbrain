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

# 2. Drop the capture hook entries (UserPromptSubmit + Stop + SessionEnd +
#    PostToolUse + SessionStart; backup first).
# devbrain_lib.py strips the hook entries by command (no jq). Prefer THIS uninstaller's
# OWN repo copy — it always supports `unregister-hook`; the installed $BIN copy may be an
# OLDER build that predates the mode and would fail, leaving stale settings.json entries.
HERE="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
LIB="$HERE/../hooks/devbrain_lib.py"
[ -f "$LIB" ] || LIB="$BIN/devbrain_lib.py"   # fallback to the installed copy
if [ -f "$settings" ] && [ -f "$LIB" ] && command -v python3 >/dev/null; then
  cp "$settings" "$settings.bak.$(date +%s)"
  python3 "$LIB" unregister-hook "$settings" \
    "$BIN/devbrain-capture.sh" "$BIN/devbrain-capture-response.sh" \
    "$BIN/devbrain-capture-memory.sh" "$BIN/devbrain-capture-gbrain.sh" \
    "$BIN/devbrain-session-start-nudge.sh" \
    && echo "removed UserPromptSubmit + Stop + SessionEnd + PostToolUse + SessionStart hooks from $settings"
fi

# 3. Remove installed scripts.
rm -f "$BIN/devbrain_lib.py" "$BIN/devbrain-project-key.sh" "$BIN/devbrain-capture.sh" \
      "$BIN/devbrain-capture-response.sh" "$BIN/devbrain-capture-memory.sh" "$BIN/devbrain-flush.sh" \
      "$BIN/devbrain-rebuild.sh" "$BIN/devbrain-todo.sh" "$BIN/devbrain-capture-gbrain.sh" \
      "$BIN/devbrain-session-start-nudge.sh" \
      "$BIN/devbrain-import" "$BIN/devbrain-queue.py" "$BIN/devbrain-dashboard.html" "$BIN/devbrain-queue-dashboard.html" \
      "$BIN/devbrain" "$BIN/devbrain.version" \
      "$BIN/devbrain-release.sh" && echo "removed installed scripts"
DBBIN="${DEVBRAIN_BIN:-$HOME/.local/bin}"
rm -f "$DBBIN/devbrain" "$DBBIN/devbrain-todo" "$DBBIN/devbrain-import"
rm -f "${NIGHTSHIFT_BIN:-$DBBIN}/nightshift"   # legacy standalone symlink (now reached via `devbrain nightshift`)
rm -rf "$CLAUDE/nightshift" && echo "removed nightshift toolset"

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
