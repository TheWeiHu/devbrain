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
DATA_DISPLAY="${DATA/#$HOME/~}"
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
install -m 0755 "$REPO/hooks/capture.sh"          "$BIN/devbrain-capture.sh"
install -m 0755 "$REPO/hooks/capture-response.sh" "$BIN/devbrain-capture-response.sh"
install -m 0755 "$REPO/scripts/flush.sh"          "$BIN/devbrain-flush.sh"
install -m 0755 "$REPO/scripts/rebuild-brain.sh"  "$BIN/devbrain-rebuild.sh"
echo "  installed $BIN/devbrain-capture.sh"
echo "  installed $BIN/devbrain-capture-response.sh"
echo "  installed $BIN/devbrain-flush.sh"
echo "  installed $BIN/devbrain-rebuild.sh"

# 2a. Pin the resolved data home into the installed copies. The capture hook runs
# in Claude Code's environment with NO $DEVBRAIN_DATA set, so it must resolve the
# right path from its own default. This makes the system relocatable: move the
# data dir, re-run install with $DEVBRAIN_DATA, done — no source edits.
for f in "$BIN/devbrain-capture.sh" "$BIN/devbrain-capture-response.sh" "$BIN/devbrain-flush.sh" "$BIN/devbrain-rebuild.sh"; do
  sed -i '' "s|DATA=\"\${DEVBRAIN_DATA:-[^}]*}\"|DATA=\"\${DEVBRAIN_DATA:-$DATA}\"|" "$f"
done
echo "  pinned data home -> $DATA"

# 3. Register the capture hooks in settings.json (idempotent; backup first).
#    UserPromptSubmit -> prompt capture; Stop -> response trace.
settings="$CLAUDE/settings.json"
[ -f "$settings" ] || echo '{}' > "$settings"
cp "$settings" "$settings.bak.$(date +%s)"
tmp="$(mktemp)"
jq --arg prompt "$BIN/devbrain-capture.sh" --arg resp "$BIN/devbrain-capture-response.sh" '
  .hooks //= {} |
  .hooks.UserPromptSubmit //= [] |
  .hooks.Stop //= [] |
  (if any(.hooks.UserPromptSubmit[]?; (.hooks // [])[]?.command == $prompt) then .
   else .hooks.UserPromptSubmit += [{"hooks":[{"type":"command","command":$prompt}]}] end) |
  (if any(.hooks.Stop[]?; (.hooks // [])[]?.command == $resp) then .
   else .hooks.Stop += [{"hooks":[{"type":"command","command":$resp}]}] end)
' "$settings" > "$tmp" && mv "$tmp" "$settings"
echo "  registered UserPromptSubmit + Stop hooks -> $settings"

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

# 5. Install the user-level skills (/continue, /distill) so they work in any repo.
skills="$CLAUDE/skills"
mkdir -p "$skills"
for s in "$REPO"/skills/*/; do
  [ -d "$s" ] || continue
  name="$(basename "$s")"
  rm -rf "$skills/$name"
  cp -R "$s" "$skills/$name"
  echo "  installed skill /$name"
done

# 6. Standing instruction in ~/.claude/CLAUDE.md (idempotent; marker-delimited).
md="$CLAUDE/CLAUDE.md"
start="<!-- devbrain:start -->"
end="<!-- devbrain:end -->"
[ -f "$md" ] || : > "$md"
# Strip any prior block, then append a fresh one.
tmp="$(mktemp)"
awk -v s="$start" -v e="$end" '
  $0==s {skip=1} !skip {print} $0==e {skip=0}
' "$md" > "$tmp" && mv "$tmp" "$md"
{
  printf '%s\n' "$start"
  printf '## devbrain (cross-project brain)\n\n'
  printf 'Every prompt is captured to the private data repo at `%s`\n' "$DATA_DISPLAY"
  printf '(routing by git remote -> `projects/<project>/`). On resume or when the\n'
  printf 'user asks "where was I" / "continue", run `/continue` to pull this project'\''s\n'
  printf 'brain and refresh the live world. After meaningful progress, run `/distill`\n'
  printf 'to curate new log into brain pages. Query the brain with `gbrain search`.\n\n'
  printf '**Lead every response with a one-sentence summary** of what you did or\n'
  printf 'concluded this turn (then continue normally). devbrain'\''s Stop hook captures\n'
  printf 'that first sentence as the turn'\''s log summary — so make it a faithful,\n'
  printf 'self-contained recap, not a preamble like "Sure, let me...".\n'
  printf '%s\n' "$end"
} >> "$md"
echo "  wrote devbrain block -> $md"

echo "Done. Capture is live on your NEXT prompt; the flusher runs every 5 min."
echo "Skills: /continue, /distill (restart Claude Code to load them)."
echo "Logs: $logf   ·   Uninstall: $REPO/scripts/uninstall.sh"
