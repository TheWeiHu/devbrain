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
install -m 0755 "$REPO/hooks/project-key.sh"      "$BIN/devbrain-project-key.sh"
install -m 0755 "$REPO/hooks/capture.sh"          "$BIN/devbrain-capture.sh"
install -m 0755 "$REPO/hooks/capture-response.sh" "$BIN/devbrain-capture-response.sh"
install -m 0755 "$REPO/scripts/flush.sh"          "$BIN/devbrain-flush.sh"
install -m 0755 "$REPO/scripts/rebuild-brain.sh"  "$BIN/devbrain-rebuild.sh"
install -m 0755 "$REPO/scripts/todo.sh"           "$BIN/devbrain-todo.sh"
echo "  installed $BIN/devbrain-project-key.sh"
echo "  installed $BIN/devbrain-capture.sh"
echo "  installed $BIN/devbrain-capture-response.sh"
echo "  installed $BIN/devbrain-flush.sh"
echo "  installed $BIN/devbrain-rebuild.sh"
echo "  installed $BIN/devbrain-todo.sh"

# 2a. Pin the resolved data home into the installed copies. The capture hook runs
# in Claude Code's environment with NO $DEVBRAIN_DATA set, so it must resolve the
# right path from its own default. This makes the system relocatable: move the
# data dir, re-run install with $DEVBRAIN_DATA, done — no source edits.
for f in "$BIN/devbrain-capture.sh" "$BIN/devbrain-capture-response.sh" "$BIN/devbrain-flush.sh" "$BIN/devbrain-rebuild.sh" "$BIN/devbrain-todo.sh"; do
  # In-place edit that works on both BSD (macOS) and GNU sed: `sed -i ''` is
  # BSD-only and breaks on Linux, so write to a temp file and move it back —
  # the same mktemp+mv pattern used in todo.sh and uninstall.sh.
  tmp="$(mktemp)"
  sed "s|DATA=\"\${DEVBRAIN_DATA:-[^}]*}\"|DATA=\"\${DEVBRAIN_DATA:-$DATA}\"|" "$f" > "$tmp" && mv "$tmp" "$f"
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
  printf 'to curate new log into brain pages.\n\n'
  printf '**Query the brain before you answer or ask — make it your first lookup, not a\n'
  printf 'last resort.** Before answering a non-trivial question about a project, before\n'
  printf 'asking the user something the brain may already record, and whenever you pick\n'
  printf 'up or resume work, run `gbrain search "<terms>"` (or `gbrain query "<question>"`\n'
  printf 'with an OpenAI key) FIRST. The brain is usually faster and more current than\n'
  printf 're-deriving from the code or asking — even mid-task, not just on `/continue`.\n\n'
  printf '**End your final message of each turn with a one-sentence recap** of what\n'
  printf 'you actually did or concluded this turn — outcome, not preamble. devbrain'\''s\n'
  printf 'Stop hook captures the last sentence of your final message as the turn'\''s log\n'
  printf 'summary, so it must stand alone: name the concrete thing you changed (file,\n'
  printf 'flag, function) and the result, so a future session reading only that line\n'
  printf 'knows what happened without the surrounding conversation.\n'
  printf '  Good: "Capped the captured recap at 500 chars and added a good/bad example to\n'
  printf '  the install prompt; synced the live hook and CLAUDE.md."\n'
  printf '  Bad:  "Done." / "Here'\''s the summary above." / "Let me know if you need\n'
  printf '  anything else." — a sign-off, a bare status, or a question is useless as a log\n'
  printf '  line. Write the recap last; everything above it is working notes.\n'
  printf '%s\n' "$end"
} >> "$md"
echo "  wrote devbrain block -> $md"

echo "Done. Capture is live on your NEXT prompt; the flusher runs every 5 min."
echo "Skills: /continue, /distill (restart Claude Code to load them)."
echo "Logs: $logf   ·   Uninstall: $REPO/scripts/uninstall.sh"
