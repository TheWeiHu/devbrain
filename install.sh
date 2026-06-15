#!/usr/bin/env bash
# devbrain — installer. Idempotent; safe to re-run.
#
# Deploys the running system from THIS repo to the machine level (~/.claude),
# turns the brain data dir into a real git repo, prompts to connect a remote so
# the brain is durable off-machine (not just temp files), and installs the
# launchd flusher that commits + pushes every 5 minutes.
#
# The brain *data* (your prompts + pages) lives in a SEPARATE private repo
# ($DEVBRAIN_DATA, default ~/devbrain-data) — never inside this system repo.
#
# Env overrides:
#   DEVBRAIN_DATA=/path      where the brain lives (default ~/devbrain-data)
#   DEVBRAIN_REMOTE=<giturl> connect this remote non-interactively
#   DEVBRAIN_NO_REMOTE=1     skip the remote prompt (local-only brain)
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAUDE_HOME="${CLAUDE_HOME:-$HOME/.claude}"
HOOKS_DST="$CLAUDE_HOME/hooks"
SKILLS_DST="$CLAUDE_HOME/skills"
SETTINGS="$CLAUDE_HOME/settings.json"
CLAUDE_MD="$CLAUDE_HOME/CLAUDE.md"
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
LA_DIR="$HOME/Library/LaunchAgents"
PLIST="$LA_DIR/com.devbrain.flush.plist"
LOG="$HOME/Library/Logs/devbrain-flush.log"
LABEL="com.devbrain.flush"

say() { printf '  %s\n' "$*"; }
hdr() { printf '\n== %s ==\n' "$*"; }

case "$DATA" in
  "$HOME/Desktop/"*|"$HOME/Documents/"*|"$HOME/Downloads/"*)
    say "WARNING: \$DEVBRAIN_DATA is under a macOS TCC-protected folder ($DATA)."
    say "         The launchd flusher will be DENIED access there. Pick a path"
    say "         outside Desktop/Documents/Downloads (default ~/devbrain-data),"
    say "         and symlink it under Desktop for visibility if you want."
    ;;
esac

# 1. Deploy hooks --------------------------------------------------------------
hdr "Hooks -> $HOOKS_DST"
mkdir -p "$HOOKS_DST"
for f in devbrain-capture.sh devbrain-capture-response.sh devbrain-flush.sh devbrain-rebuild.sh; do
  cp "$REPO/hooks/$f" "$HOOKS_DST/$f" && chmod +x "$HOOKS_DST/$f" && say "installed $f"
done

# 2. Deploy skills ------------------------------------------------------------
hdr "Skills -> $SKILLS_DST"
mkdir -p "$SKILLS_DST"
for s in continue distill; do
  mkdir -p "$SKILLS_DST/$s"
  cp "$REPO/skills/$s/SKILL.md" "$SKILLS_DST/$s/SKILL.md" && say "installed /$s"
done

# 3. Brain data repo + remote -------------------------------------------------
hdr "Brain data repo -> $DATA"
mkdir -p "$DATA"
if [ ! -d "$DATA/.git" ]; then
  ( cd "$DATA" && git init --quiet )
  say "git init"
fi
[ -f "$DATA/.gitignore" ] || printf '*.pglite\n.DS_Store\n' > "$DATA/.gitignore"
[ -f "$DATA/README.md" ]  || cat > "$DATA/README.md" <<EOF
# devbrain-data

Private cross-project brain: append-only prompt logs (\`projects/<project>/log/\`)
and distilled brain pages (\`projects/<project>/brain/\`). The log is the source of
truth; brain pages are a rebuildable projection. Synced by the devbrain flusher.
EOF
if [ -z "$(cd "$DATA" && git log -1 --oneline 2>/dev/null)" ]; then
  ( cd "$DATA" && git add -A && \
    git -c user.name=devbrain -c user.email=devbrain@localhost commit --quiet -m "init devbrain-data" )
  say "initial commit"
fi

# Connect a remote so the brain is durable off-machine.
if cd "$DATA" && git remote get-url origin >/dev/null 2>&1; then
  say "remote: $(git -C "$DATA" remote get-url origin)"
elif [ -n "${DEVBRAIN_REMOTE:-}" ]; then
  git -C "$DATA" remote add origin "$DEVBRAIN_REMOTE"
  git -C "$DATA" push -u origin HEAD >/dev/null 2>&1 && say "connected + pushed: $DEVBRAIN_REMOTE" \
    || say "remote added ($DEVBRAIN_REMOTE) — push it manually: git -C $DATA push -u origin HEAD"
elif [ "${DEVBRAIN_NO_REMOTE:-0}" = "1" ] || [ ! -t 0 ]; then
  say "No remote configured — brain is LOCAL-ONLY (commits happen, nothing pushed)."
  say "Make it durable later:  git -C $DATA remote add origin <url> && git -C $DATA push -u origin HEAD"
else
  echo
  say "Your brain has no remote yet — it would live only on this machine."
  say "Connect one now so every distill is backed up off-machine?"
  if command -v gh >/dev/null 2>&1; then
    printf '    [g] gh repo create (private)   [u] paste a git URL   [s] skip: '
  else
    printf '    [u] paste a git URL   [s] skip: '
  fi
  read -r choice
  case "$choice" in
    g|G)
      if command -v gh >/dev/null 2>&1; then
        printf '    repo name [devbrain-data]: '; read -r rname; rname="${rname:-devbrain-data}"
        ( cd "$DATA" && gh repo create "$rname" --private --source . --remote origin --push ) \
          && say "created private repo + pushed" || say "gh repo create failed — add a remote manually later."
      fi
      ;;
    u|U)
      printf '    git remote URL: '; read -r url
      if [ -n "$url" ]; then
        git -C "$DATA" remote add origin "$url"
        git -C "$DATA" push -u origin HEAD >/dev/null 2>&1 && say "connected + pushed" \
          || say "remote added — push manually: git -C $DATA push -u origin HEAD"
      fi
      ;;
    *)
      say "Skipped. Brain is LOCAL-ONLY for now."
      say "Connect later: git -C $DATA remote add origin <url> && git -C $DATA push -u origin HEAD"
      ;;
  esac
fi

# 4. Global CLAUDE.md standing instruction ------------------------------------
hdr "Standing instruction -> $CLAUDE_MD"
BLOCK="$(sed "s#@DEVBRAIN_DATA@#$DATA#g" "$REPO/templates/CLAUDE.md.block")"
python3 - "$CLAUDE_MD" <<PY
import sys, io
path = sys.argv[1]
block = """$BLOCK"""
start, end = "<!-- devbrain:start -->", "<!-- devbrain:end -->"
try:
    cur = io.open(path, encoding="utf-8").read()
except FileNotFoundError:
    cur = ""
if start in cur and end in cur:
    pre = cur[:cur.index(start)]
    post = cur[cur.index(end)+len(end):]
    new = pre + block.strip() + post
else:
    sep = "" if cur == "" or cur.endswith("\n\n") else ("\n" if cur.endswith("\n") else "\n\n")
    new = cur + sep + block.strip() + "\n"
io.open(path, "w", encoding="utf-8").write(new)
print("  updated devbrain block")
PY

# 5. Register hooks in settings.json ------------------------------------------
hdr "Hook registration -> $SETTINGS"
python3 - "$SETTINGS" "$HOOKS_DST" <<'PY'
import sys, json, io, os
settings_path, hooks_dst = sys.argv[1], sys.argv[2]
try:
    d = json.load(io.open(settings_path, encoding="utf-8"))
except Exception:
    d = {}
hooks = d.setdefault("hooks", {})
wanted = {
    "UserPromptSubmit": os.path.join(hooks_dst, "devbrain-capture.sh"),
    "Stop":             os.path.join(hooks_dst, "devbrain-capture-response.sh"),
}
for event, cmd in wanted.items():
    groups = hooks.setdefault(event, [])
    have = any(
        h.get("command") == cmd
        for g in groups if isinstance(g, dict)
        for h in g.get("hooks", []) if isinstance(h, dict)
    )
    if not have:
        groups.append({"hooks": [{"type": "command", "command": cmd}]})
        print(f"  registered {event} -> {os.path.basename(cmd)}")
    else:
        print(f"  {event} already registered")
os.makedirs(os.path.dirname(settings_path), exist_ok=True)
io.open(settings_path, "w", encoding="utf-8").write(json.dumps(d, indent=2) + "\n")
PY

# 6. Install + (re)load the launchd flusher -----------------------------------
hdr "Flusher (launchd, every 5 min) -> $PLIST"
mkdir -p "$LA_DIR" "$(dirname "$LOG")"
sed -e "s#@FLUSH_SH@#$HOOKS_DST/devbrain-flush.sh#g" \
    -e "s#@DEVBRAIN_DATA@#$DATA#g" \
    -e "s#@LOG@#$LOG#g" \
    "$REPO/templates/com.devbrain.flush.plist.template" > "$PLIST"
say "wrote plist"
launchctl bootout "gui/$(id -u)/$LABEL" >/dev/null 2>&1 || true
if launchctl bootstrap "gui/$(id -u)" "$PLIST" >/dev/null 2>&1; then
  say "loaded ($LABEL)"
else
  launchctl unload "$PLIST" >/dev/null 2>&1 || true
  launchctl load "$PLIST" >/dev/null 2>&1 && say "loaded ($LABEL, legacy)" || say "could not load — load manually: launchctl bootstrap gui/$(id -u) $PLIST"
fi

# 7. Rebuild the gbrain index (optional) --------------------------------------
hdr "gbrain index"
if command -v gbrain >/dev/null 2>&1; then
  DEVBRAIN_DATA="$DATA" bash "$HOOKS_DST/devbrain-rebuild.sh" >/dev/null 2>&1 \
    && say "rebuilt from $DATA" || say "rebuild skipped (no pages yet, or gbrain not initialized)"
else
  say "gbrain not on PATH — skipped (keyword/semantic search unavailable until installed)"
fi

hdr "Done"
say "Data repo:   $DATA"
say "Flusher log: $LOG"
say "Verify:      tail -f \"$LOG\"   (should show no 'Operation not permitted')"
