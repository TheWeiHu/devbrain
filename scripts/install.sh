#!/usr/bin/env bash
# devbrain — machine wiring installer.
#
# Installs the per-machine runtime: the capture hook (Stage A) and the flusher
# LaunchAgent. Idempotent and reversible (see scripts/uninstall.sh). Installs
# STABLE copies into ~/.claude so the runtime does not depend on where this
# system repo happens to live (Desktop, Conductor worktree, etc.).
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
CLAUDE="$HOME/.claude"
BIN="$CLAUDE/hooks"

echo "devbrain install"
echo "  system repo : $REPO"
echo "  data home   : $DATA"

# 1. Preconditions.
command -v jq >/dev/null || { echo "ERROR: jq required (brew install jq)"; exit 1; }
if [ ! -d "$DATA/.git" ]; then
  echo "ERROR: data repo missing at $DATA"
  echo "  clone it first:  git clone git@github.com:TheWeiHu/devbrain-data.git \"$DATA\""
  exit 1
fi

# 2. Install the runtime scripts (stable copies).
mkdir -p "$BIN"
install -m 0755 "$REPO/hooks/capture.sh"  "$BIN/devbrain-capture.sh"
install -m 0755 "$REPO/scripts/flush.sh"  "$BIN/devbrain-flush.sh"
echo "  installed $BIN/devbrain-capture.sh"
echo "  installed $BIN/devbrain-flush.sh"

# 3. Register the UserPromptSubmit hook in settings.json (idempotent; backup first).
settings="$CLAUDE/settings.json"
[ -f "$settings" ] || echo '{}' > "$settings"
cp "$settings" "$settings.bak.$(date +%s)"
tmp="$(mktemp)"
jq --arg cmd "$BIN/devbrain-capture.sh" '
  .hooks //= {} |
  .hooks.UserPromptSubmit //= [] |
  if any(.hooks.UserPromptSubmit[]?; (.hooks // [])[]?.command == $cmd)
  then .
  else .hooks.UserPromptSubmit += [{"hooks":[{"type":"command","command":$cmd}]}]
  end
' "$settings" > "$tmp" && mv "$tmp" "$settings"
echo "  registered UserPromptSubmit hook -> $settings"

# 4. Install + load the flusher LaunchAgent.
plist="$HOME/Library/LaunchAgents/com.devbrain.flush.plist"
logf="$HOME/Library/Logs/devbrain-flush.log"
mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"
sed -e "s|__FLUSH__|$BIN/devbrain-flush.sh|g" \
    -e "s|__DATA__|$DATA|g" \
    -e "s|__LOG__|$logf|g" \
    "$REPO/scripts/com.devbrain.flush.plist" > "$plist"
launchctl unload "$plist" 2>/dev/null || true
launchctl load "$plist"
echo "  loaded flusher LaunchAgent (every 5 min) -> $plist"

echo "Done. Capture is live on your NEXT prompt; the flusher runs every 5 min."
echo "Logs: $logf   ·   Uninstall: $REPO/scripts/uninstall.sh"
