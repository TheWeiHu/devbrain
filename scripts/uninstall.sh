#!/usr/bin/env bash
# devbrain — remove the machine wiring installed by install.sh.
# Leaves the data repo and its contents untouched.
set -uo pipefail

CLAUDE="$HOME/.claude"
BIN="$CLAUDE/hooks"
CODEX_DIR="${CODEX_HOME:-$HOME/.codex}"
CODEX_BIN="$CODEX_DIR/hooks"
CODEX_SKILLS="$HOME/.agents/skills"
settings="$CLAUDE/settings.json"
codex_settings="$CODEX_DIR/hooks.json"
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

if [ -f "$codex_settings" ] && [ -f "$LIB" ] && command -v python3 >/dev/null; then
  cp "$codex_settings" "$codex_settings.bak.$(date +%s)"
  python3 "$LIB" unregister-hook "$codex_settings" \
    "DEVBRAIN_HARNESS=codex $CODEX_BIN/devbrain-capture.sh" \
    "DEVBRAIN_HARNESS=codex $CODEX_BIN/devbrain-capture-response.sh" \
    "DEVBRAIN_HARNESS=codex $CODEX_BIN/devbrain-capture-gbrain.sh" \
    "DEVBRAIN_HARNESS=codex $CODEX_BIN/devbrain-session-start-nudge.sh" \
    && echo "removed Codex UserPromptSubmit + Stop + PostToolUse + SessionStart hooks from $codex_settings"
fi

# 3. Remove installed scripts.
rm -f "$BIN/devbrain_lib.py" "$BIN/devbrain-project-key.sh" "$BIN/devbrain-capture.sh" \
      "$BIN/devbrain-capture-response.sh" "$BIN/devbrain-capture-memory.sh" "$BIN/devbrain-flush.sh" \
      "$BIN/devbrain-rebuild.sh" "$BIN/devbrain-brain.sh" "$BIN/devbrain-todo.sh" "$BIN/devbrain-capture-gbrain.sh" \
      "$BIN/devbrain-session-start-nudge.sh" "$BIN/devbrain-link-preferences.sh" \
      "$BIN/devbrain-import" "$BIN/devbrain-queue.py" "$BIN/devbrain-dashboard.html" "$BIN/devbrain-queue-dashboard.html" \
      "$BIN/devbrain" "$BIN/devbrain.version" "$BIN/devbrain-uninstall.sh" \
      "$BIN/devbrain-release.sh" && echo "removed installed scripts"
rm -f "$CODEX_BIN/devbrain_lib.py" "$CODEX_BIN/devbrain-project-key.sh" \
      "$CODEX_BIN/devbrain-capture.sh" "$CODEX_BIN/devbrain-capture-response.sh" \
      "$CODEX_BIN/devbrain-capture-gbrain.sh" "$CODEX_BIN/devbrain-session-start-nudge.sh" \
      && echo "removed installed Codex hook scripts"
DBBIN="${DEVBRAIN_BIN:-$HOME/.local/bin}"
rm -f "$DBBIN/devbrain" "$DBBIN/devbrain-todo" "$DBBIN/devbrain-import"
rm -f "${NIGHTSHIFT_BIN:-$DBBIN}/nightshift"   # legacy standalone symlink (now reached via `devbrain nightshift`)
# Reverse the PATH line the installer may have added to a shell rc.
for rc in "$HOME/.zshrc" "$HOME/.bash_profile" "$HOME/.bashrc" "$HOME/.profile"; do
  [ -f "$rc" ] && grep -qF 'added by devbrain installer' "$rc" 2>/dev/null || continue
  # Drop our marker line and the export line that follows it.
  awk '/# added by devbrain installer/{skip=2} skip>0{skip--; next} {print}' "$rc" > "$rc.devbrain.tmp" \
    && mv "$rc.devbrain.tmp" "$rc" && echo "removed devbrain PATH entry from ${rc/#$HOME/~}"
done
rm -rf "$CLAUDE/nightshift" && echo "removed nightshift toolset"

# Reverse git-gate: install.sh recorded the checkout it set core.hooksPath on
# (BASH_SOURCE can't find it — uninstall may run as an installed copy or via the CLI).
gate_repo_file="$CLAUDE/.git-gate-repo"
if [ -f "$gate_repo_file" ]; then
  gate_repo="$(cat "$gate_repo_file" 2>/dev/null)"
  if [ -n "$gate_repo" ] && [ "$(git -C "$gate_repo" config --local core.hooksPath 2>/dev/null)" = "scripts/git-hooks" ]; then
    git -C "$gate_repo" config --local --unset core.hooksPath && echo "removed git-gate (core.hooksPath in $gate_repo)"
  fi
  rm -f "$gate_repo_file"
fi

# 4. Remove installed skills.
rm -rf "$CLAUDE/skills/continue" "$CLAUDE/skills/distill" "$CLAUDE/skills/work" \
       "$CLAUDE/skills/nightshift" "$CLAUDE/skills/reconcile" && echo "removed devbrain skills"
rm -rf "$CODEX_SKILLS/continue" "$CODEX_SKILLS/distill" "$CODEX_SKILLS/work" \
       "$CODEX_SKILLS/nightshift" "$CODEX_SKILLS/reconcile" && echo "removed Codex devbrain skills"

# 5. Strip the devbrain block from ~/.claude/CLAUDE.md.
md="$CLAUDE/CLAUDE.md"
if [ -f "$md" ]; then
  tmp="$(mktemp)"
  awk -v s="<!-- devbrain:start -->" -v e="<!-- devbrain:end -->" '
    $0==s {skip=1} !skip {print} $0==e {skip=0}
  ' "$md" > "$tmp" && mv "$tmp" "$md"
  # Also drop the managed global-preferences @import line (+ its marker). `grep -v`
  # exits 1 when it filters out every line, so swallow it to stay pipefail-safe.
  tmp2="$(mktemp)"
  { grep -vF 'devbrain: global preferences page' "$md" || true; } \
    | { grep -vE '^@.*/preferences/global\.md$' || true; } > "$tmp2"
  mv "$tmp2" "$md"
  echo "removed devbrain block from $md"
fi

codex_md="$CODEX_DIR/AGENTS.md"
if [ -f "$codex_md" ]; then
  tmp="$(mktemp)"
  awk -v s="<!-- devbrain:start -->" -v e="<!-- devbrain:end -->" '
    $0==s {skip=1} !skip {print} $0==e {skip=0}
  ' "$codex_md" > "$tmp" && mv "$tmp" "$codex_md"
  echo "removed devbrain block from $codex_md"
fi

echo "Done. The data repo (~/devbrain-data) was left untouched."
