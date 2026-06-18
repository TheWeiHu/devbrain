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

# ── components — decide what to wire on this machine ─────────────────────────
# Each piece is independently toggleable. Defaults: everything on EXCEPT the
# experimental nightshift loop. Flags: --with a,b · --without a,b · --only a,b
# (comma-separated). In a terminal you also get a quick y/n per piece; --yes (or
# any non-TTY run, e.g. CI/agent) takes the defaults without asking.
# Per-component on/off lives in plain ON_<name> vars (set/read by indirection) so
# this works on macOS's stock bash 3.2 — no associative arrays.
ALL="capture response-trace nudge flusher skills claude-md nightshift"
vof()   { printf 'ON_%s' "${1//-/_}"; }                  # component -> its flag var name
set_c() { printf -v "$(vof "$1")" '%s' "$2"; }           # set on(1)/off(0)
want()  { local v; v="$(vof "$1")"; [ "${!v}" = 1 ]; }
for c in $ALL; do set_c "$c" 1; done; set_c nightshift 0  # defaults: all on but nightshift
[ "${DEVBRAIN_NIGHTSHIFT:-0}" = 1 ] && set_c nightshift 1 # back-compat: env still opts in
EXPLICIT=" "; ASSUME_YES=0
set_list() { local val="$1" item oldIFS="$IFS"; IFS=,
  for item in $2; do IFS="$oldIFS"
    case " $ALL " in *" $item "*) set_c "$item" "$val"; EXPLICIT="$EXPLICIT$item ";;
      *) echo "install: unknown component '$item' (have: $ALL)" >&2; exit 1;; esac
  done; IFS="$oldIFS"; }
while [ $# -gt 0 ]; do case "$1" in
  --with)    set_list 1 "$2"; shift 2;;
  --without) set_list 0 "$2"; shift 2;;
  --only)    for c in $ALL; do set_c "$c" 0; done; set_list 1 "$2"; EXPLICIT=" $ALL "; shift 2;;
  --yes|-y)  ASSUME_YES=1; shift;;
  *) echo "install: unknown arg: $1 (use --with/--without/--only <components>, --yes)" >&2; exit 1;;
esac; done
if [ -t 0 ] && [ "$ASSUME_YES" != 1 ]; then
  echo "Choose components (Enter keeps the default):"
  for c in $ALL; do
    case "$EXPLICIT" in *" $c "*) continue;; esac        # already set by a flag → don't ask
    if want "$c"; then p="Y/n"; else p="y/N"; fi
    printf '  %-14s [%s] ' "$c" "$p"; read -r ans </dev/tty 2>/dev/null || ans=""
    case "$ans" in [Yy]*) set_c "$c" 1;; [Nn]*) set_c "$c" 0;; esac
  done
fi

echo "devbrain install"
echo "  system repo : $REPO"
echo "  data home   : $DATA"
echo "  components  : $(for c in $ALL; do want "$c" && printf '%s ' "$c"; done)"

# 1. Preconditions.
command -v jq >/dev/null || { echo "ERROR: jq required (brew install jq)"; exit 1; }
if [ ! -d "$DATA/.git" ]; then
  echo "ERROR: data repo missing at $DATA"
  echo "  The data repo is YOUR private prompt-log + brain store — create your own; don't"
  echo "  reuse someone else's. Easiest:  ./setup  (inits a fresh one, or clones"
  echo "  \$DEVBRAIN_DATA_REMOTE if set). Or by hand:"
  echo "      git init \"$DATA\"                              # a new private repo"
  echo "      DEVBRAIN_DATA_REMOTE=<your-git-url> ./setup    # clone an existing one"
  exit 1
fi

# 2. Install the runtime scripts (stable copies).
mkdir -p "$BIN"
install -m 0755 "$REPO/hooks/devbrain_lib.py"     "$BIN/devbrain_lib.py"   # shared rules (redact/synthetic/recap+sample/routing)
install -m 0755 "$REPO/hooks/project-key.sh"      "$BIN/devbrain-project-key.sh"
install -m 0755 "$REPO/hooks/capture.sh"          "$BIN/devbrain-capture.sh"
install -m 0755 "$REPO/hooks/capture-response.sh" "$BIN/devbrain-capture-response.sh"
install -m 0755 "$REPO/hooks/capture-memory.sh"   "$BIN/devbrain-capture-memory.sh"
install -m 0755 "$REPO/hooks/capture-gbrain.sh"   "$BIN/devbrain-capture-gbrain.sh"   # PostToolUse: log gbrain calls
install -m 0755 "$REPO/hooks/session-start-nudge.sh" "$BIN/devbrain-session-start-nudge.sh" # SessionStart: query-brain nudge
install -m 0755 "$REPO/scripts/flush.sh"          "$BIN/devbrain-flush.sh"
install -m 0755 "$REPO/scripts/rebuild-brain.sh"  "$BIN/devbrain-rebuild.sh"
install -m 0755 "$REPO/scripts/todo.sh"           "$BIN/devbrain-todo.sh"
install -m 0755 "$REPO/scripts/import.py"         "$BIN/devbrain-import"
install -m 0755 "$REPO/scripts/devbrain"          "$BIN/devbrain"          # the unified `devbrain <verb>` dispatcher
install -m 0644 "$REPO/VERSION"                   "$BIN/devbrain.version"  # so `devbrain version` works installed
# NOTE: scripts/release.sh is deliberately NOT installed — it releases the devbrain
# PROJECT (maintainer-only), so it stays a repo-checkout script, not a user command.
rm -f "$BIN/devbrain-release.sh"   # clean up the stray copy older installs shipped
echo "  installed $BIN/devbrain_lib.py"
echo "  installed $BIN/devbrain-project-key.sh"
echo "  installed $BIN/devbrain-capture.sh"
echo "  installed $BIN/devbrain-capture-response.sh"
echo "  installed $BIN/devbrain-capture-memory.sh"
echo "  installed $BIN/devbrain-capture-gbrain.sh"
echo "  installed $BIN/devbrain-session-start-nudge.sh"
echo "  installed $BIN/devbrain-flush.sh"
echo "  installed $BIN/devbrain-rebuild.sh"
echo "  installed $BIN/devbrain-todo.sh"
echo "  installed $BIN/devbrain-import"
echo "  installed $BIN/devbrain (unified CLI)"

# Put `devbrain` on PATH — the hooks dir usually isn't on it. The unified command
# is the front door (`devbrain todo`, `devbrain import`, …); the legacy bare names
# (devbrain-todo, devbrain-import) stay linked as back-compat aliases so nothing
# that called them breaks.
DBBIN="${DEVBRAIN_BIN:-$HOME/.local/bin}"; mkdir -p "$DBBIN"
ln -sf "$BIN/devbrain"         "$DBBIN/devbrain"
ln -sf "$BIN/devbrain-todo.sh" "$DBBIN/devbrain-todo"     # back-compat alias of `devbrain todo`
ln -sf "$BIN/devbrain-import"  "$DBBIN/devbrain-import"   # back-compat alias of `devbrain import`
echo "  linked devbrain (+ legacy devbrain-todo / devbrain-import) -> $DBBIN"
case ":$PATH:" in *":$DBBIN:"*) ;; *) echo "  NOTE: add $DBBIN to your PATH to use the devbrain command";; esac

# 2-ns. nightshift — EXPERIMENTAL autonomous overnight loop. OFF BY DEFAULT: it is
# installed ONLY when you opt in with DEVBRAIN_NIGHTSHIFT=1, so a normal devbrain
# install never puts the `nightshift` command on your PATH. Nothing else depends on
# it. Default backend is headless `claude -p`; tmux is needed only for `--tmux`.
if want nightshift; then
  NS="$CLAUDE/nightshift"; mkdir -p "$NS"
  for s in nightshift nightshift-orchestrate.sh nightshift-status.py nightshift-serve.py; do
    install -m 0755 "$REPO/scripts/$s" "$NS/$s"
  done
  install -m 0644 "$REPO/scripts/nightshift-dashboard.html" "$NS/nightshift-dashboard.html"
  install -m 0755 "$REPO/scripts/todo.sh"      "$NS/todo.sh"        # sibling fallback for the CLI/orchestrator
  install -m 0755 "$REPO/hooks/turn-marker.sh" "$NS/turn-marker.sh" # the --tmux backend installs this Stop hook globally on first run
  NSBIN="${NIGHTSHIFT_BIN:-$HOME/.local/bin}"; mkdir -p "$NSBIN"
  ln -sf "$NS/nightshift" "$NSBIN/nightshift"
  echo "  installed $NS/ (nightshift toolset — EXPERIMENTAL)"
  echo "  linked    $NSBIN/nightshift  ->  run: nightshift start <repo>  (or: devbrain nightshift start <repo>)"
  case ":$PATH:" in *":$NSBIN:"*) ;; *) echo "  NOTE: add $NSBIN to your PATH to use the 'nightshift' command";; esac
else
  echo "  nightshift (experimental autonomous loop): off — enable with --with nightshift (or DEVBRAIN_NIGHTSHIFT=1)"
fi

# 2a. Pin the resolved data home into the installed copies. The capture hook runs
# in Claude Code's environment with NO $DEVBRAIN_DATA set, so it must resolve the
# right path from its own default. This makes the system relocatable: move the
# data dir, re-run install with $DEVBRAIN_DATA, done — no source edits.
for f in "$BIN/devbrain-capture.sh" "$BIN/devbrain-capture-response.sh" "$BIN/devbrain-capture-memory.sh" "$BIN/devbrain-capture-gbrain.sh" "$BIN/devbrain-flush.sh" "$BIN/devbrain-rebuild.sh" "$BIN/devbrain-todo.sh"; do
  # Portable across BSD (macOS) + GNU sed (`sed -i ''` is BSD-only): write to a
  # temp, then `cat >` it BACK into $f (not `mv`). mktemp makes the temp 0600, so
  # mv-ing it over $f would strip the 0755 exec bit `install` set and break the
  # hooks with "Permission denied"; redirecting preserves $f's mode + inode.
  tmp="$(mktemp)"
  sed "s|DATA=\"\${DEVBRAIN_DATA:-[^}]*}\"|DATA=\"\${DEVBRAIN_DATA:-$DATA}\"|" "$f" > "$tmp" && cat "$tmp" > "$f" && rm -f "$tmp"
done
echo "  pinned data home -> $DATA"

# 3. Register the capture hooks in settings.json (idempotent; backup first).
#    capture -> UserPromptSubmit (prompt log) + PostToolUse/Bash (gbrain call log);
#    response-trace -> Stop (turn trace) + SessionEnd (Claude's memory store mirror);
#    nudge -> SessionStart (query-brain nudge with live page/task counts).
if want capture || want response-trace || want nudge; then
  settings="$CLAUDE/settings.json"
  [ -f "$settings" ] || echo '{}' > "$settings"
  cp "$settings" "$settings.bak.$(date +%s)"
  if want capture; then
    tmp="$(mktemp)"
    # UserPromptSubmit logs prompts; PostToolUse(Bash) logs every gbrain call (the
    # "Bash" matcher fires only on shell calls, the only way an agent runs gbrain).
    jq --arg c "$BIN/devbrain-capture.sh" --arg gb "$BIN/devbrain-capture-gbrain.sh" '
      .hooks //= {} | .hooks.UserPromptSubmit //= [] | .hooks.PostToolUse //= [] |
      (if any(.hooks.UserPromptSubmit[]?; (.hooks // [])[]?.command == $c) then .
       else .hooks.UserPromptSubmit += [{"hooks":[{"type":"command","command":$c}]}] end) |
      (if any(.hooks.PostToolUse[]?; (.hooks // [])[]?.command == $gb) then .
       else .hooks.PostToolUse += [{"matcher":"Bash","hooks":[{"type":"command","command":$gb}]}] end)
    ' "$settings" > "$tmp" && mv "$tmp" "$settings"
    echo "  registered UserPromptSubmit + PostToolUse(Bash) hooks (capture + gbrain) -> $settings"
  fi
  if want response-trace; then
    tmp="$(mktemp)"
    jq --arg resp "$BIN/devbrain-capture-response.sh" --arg mem "$BIN/devbrain-capture-memory.sh" '
      .hooks //= {} | .hooks.Stop //= [] | .hooks.SessionEnd //= [] |
      (if any(.hooks.Stop[]?; (.hooks // [])[]?.command == $resp) then .
       else .hooks.Stop += [{"hooks":[{"type":"command","command":$resp}]}] end) |
      (if any(.hooks.SessionEnd[]?; (.hooks // [])[]?.command == $mem) then .
       else .hooks.SessionEnd += [{"hooks":[{"type":"command","command":$mem}]}] end)
    ' "$settings" > "$tmp" && mv "$tmp" "$settings"
    echo "  registered Stop + SessionEnd hooks (response-trace + memory) -> $settings"
  fi
  if want nudge; then
    tmp="$(mktemp)"
    # SessionStart on startup|resume injects a per-session "query the brain first"
    # nudge with live counts; other sources (clear/compact) are skipped to avoid
    # re-nudging mid-session.
    jq --arg n "$BIN/devbrain-session-start-nudge.sh" '
      .hooks //= {} | .hooks.SessionStart //= [] |
      (if any(.hooks.SessionStart[]?; (.hooks // [])[]?.command == $n) then .
       else .hooks.SessionStart += [{"matcher":"startup|resume","hooks":[{"type":"command","command":$n}]}] end)
    ' "$settings" > "$tmp" && mv "$tmp" "$settings"
    echo "  registered SessionStart hook (query-brain nudge) -> $settings"
  fi
fi

# 4. Install the flusher on a 5-min schedule: launchd on macOS, a systemd user
#    timer on Linux (falling back to cron, then to a manual note).
if want flusher; then
  case "$(uname -s)" in
    Darwin)
      logf="$HOME/Library/Logs/devbrain-flush.log"
      plist="$HOME/Library/LaunchAgents/com.devbrain.flush.plist"
      mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"
      sed -e "s|__FLUSH__|$BIN/devbrain-flush.sh|g" \
          -e "s|__DATA__|$DATA|g" \
          -e "s|__LOG__|$logf|g" \
          "$REPO/scripts/com.devbrain.flush.plist" > "$plist"
      launchctl unload "$plist" 2>/dev/null || true
      launchctl load "$plist"
      echo "  loaded flusher LaunchAgent (every 5 min) -> $plist"
      ;;
    *)
      # Linger first so the user manager runs without an active login session
      # (headless box), then the user-timer detection below can see the bus.
      loginctl enable-linger "$(id -un)" >/dev/null 2>&1 || true
      if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
        sd="$HOME/.config/systemd/user"; mkdir -p "$sd"
        cat > "$sd/devbrain-flush.service" <<EOF
[Unit]
Description=devbrain flush — commit+push the prompt-log data repo
[Service]
Type=oneshot
Environment=DEVBRAIN_DATA=$DATA
ExecStart=$BIN/devbrain-flush.sh
EOF
        cat > "$sd/devbrain-flush.timer" <<EOF
[Unit]
Description=devbrain flush every 5 minutes
[Timer]
OnBootSec=2min
OnUnitActiveSec=5min
Persistent=true
[Install]
WantedBy=timers.target
EOF
        systemctl --user daemon-reload
        systemctl --user enable --now devbrain-flush.timer >/dev/null 2>&1
        echo "  enabled systemd user timer (every 5 min) -> devbrain-flush.timer"
      elif command -v crontab >/dev/null 2>&1; then
        line="*/5 * * * * DEVBRAIN_DATA=$DATA $BIN/devbrain-flush.sh >/dev/null 2>&1"
        ( crontab -l 2>/dev/null | grep -vF 'devbrain-flush.sh'; echo "$line" ) | crontab -
        echo "  installed cron entry (every 5 min) -> devbrain-flush.sh"
      else
        echo "  NOTE: no systemd --user or cron — run $BIN/devbrain-flush.sh on your own schedule to auto-flush"
      fi
      ;;
  esac
fi

# 5. Install the user-level skills (/continue, /distill) so they work in any repo.
if want skills; then
  skills="$CLAUDE/skills"
  mkdir -p "$skills"
  for s in "$REPO"/skills/*/; do
    [ -d "$s" ] || continue
    name="$(basename "$s")"
    rm -rf "$skills/$name"
    cp -R "$s" "$skills/$name"
    echo "  installed skill /$name"
  done
fi

# 6. Standing instruction in ~/.claude/CLAUDE.md (idempotent; marker-delimited).
if want claude-md; then
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
fi

# 7. FIRST-RUN seed: on a fresh brain only, offer to seed from existing Claude Code
#    history (transcripts + history.jsonl + memory) so devbrain has VALUE on day one
#    instead of starting empty — the one-time batch counterpart to live capture. Runs
#    ONLY when the brain is empty, so a reinstall over an existing brain never re-scans
#    the cache. To re-import deliberately: `devbrain-import --apply`, or point
#    DEVBRAIN_DATA at a new folder. Consent-gated (preview, then apply on a yes when
#    interactive; never headless). Skip entirely with DEVBRAIN_NO_IMPORT=1.
brain_has_content=""
[ -n "$(find "$DATA/projects" -mindepth 2 -name '*.md' -print -quit 2>/dev/null)" ] && brain_has_content=1
if [ -n "$brain_has_content" ]; then
  echo ""
  echo "  brain already has content — skipping first-run import (this keeps reinstall safe)."
  echo "  to re-import deliberately:  devbrain-import --apply"
elif command -v python3 >/dev/null 2>&1 && [ -z "${DEVBRAIN_NO_IMPORT:-}" ]; then
  echo ""
  echo "  Fresh brain — devbrain import preview of existing Claude Code history that can seed it:"
  python3 "$BIN/devbrain-import" --data "$DATA" 2>/dev/null | sed 's/^/    /' || true
  if [ -t 0 ]; then
    printf "  Seed the brain from this history now? [Y/n] "
    read -r _ans </dev/tty 2>/dev/null || _ans=""
    case "$_ans" in
      [Nn]*) echo "  skipped — seed later:  devbrain-import --apply" ;;
      *) if python3 "$BIN/devbrain-import" --data "$DATA" --apply >/dev/null 2>&1; then
           echo "  seeded. Run /distill (or /continue) per project to build searchable brain pages."
         else echo "  import had an issue — run manually:  devbrain-import --apply"; fi ;;
    esac
  else
    echo "  (non-interactive shell — not auto-seeding) seed later:  devbrain-import --apply"
  fi
fi

echo "Done."
want capture && echo "  capture is live on your NEXT prompt"
want nudge   && echo "  nudge fires at the START of your next session (query-brain reminder)"
want flusher && echo "  flusher runs every 5 min (commits/pushes the data repo)"
want skills  && echo "  skills: /continue, /distill (restart Claude Code to load them)"
echo "  onboard older history anytime:  devbrain-import --apply"
echo "  uninstall: $REPO/scripts/uninstall.sh"
