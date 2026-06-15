#!/usr/bin/env bash
# devbrain — uninstaller. Removes the machine-level wiring. NEVER touches your
# brain DATA repo ($DEVBRAIN_DATA) — your prompts and pages are left intact.
set -uo pipefail

CLAUDE_HOME="${CLAUDE_HOME:-$HOME/.claude}"
HOOKS_DST="$CLAUDE_HOME/hooks"
SKILLS_DST="$CLAUDE_HOME/skills"
SETTINGS="$CLAUDE_HOME/settings.json"
CLAUDE_MD="$CLAUDE_HOME/CLAUDE.md"
PLIST="$HOME/Library/LaunchAgents/com.devbrain.flush.plist"
LABEL="com.devbrain.flush"

say() { printf '  %s\n' "$*"; }

# 1. Unload + remove the flusher.
launchctl bootout "gui/$(id -u)/$LABEL" >/dev/null 2>&1 || launchctl unload "$PLIST" >/dev/null 2>&1 || true
rm -f "$PLIST" && say "removed flusher plist"

# 2. Remove hooks + skills.
for f in devbrain-capture.sh devbrain-capture-response.sh devbrain-flush.sh devbrain-rebuild.sh; do
  rm -f "$HOOKS_DST/$f"
done
rm -rf "$SKILLS_DST/continue" "$SKILLS_DST/distill"
say "removed hooks + skills"

# 3. Unregister hooks from settings.json.
if [ -f "$SETTINGS" ]; then
  python3 - "$SETTINGS" <<'PY'
import sys, json, io
p = sys.argv[1]
try: d = json.load(io.open(p, encoding="utf-8"))
except Exception: d = {}
hooks = d.get("hooks", {})
for event in ("UserPromptSubmit", "Stop"):
    groups = hooks.get(event, [])
    kept = []
    for g in groups:
        if isinstance(g, dict):
            g["hooks"] = [h for h in g.get("hooks", [])
                          if not (isinstance(h, dict) and "devbrain-capture" in str(h.get("command","")))]
            if g.get("hooks"): kept.append(g)
        else:
            kept.append(g)
    if kept: hooks[event] = kept
    elif event in hooks: del hooks[event]
io.open(p, "w", encoding="utf-8").write(json.dumps(d, indent=2) + "\n")
print("  unregistered hooks")
PY
fi

# 4. Strip the CLAUDE.md block.
if [ -f "$CLAUDE_MD" ]; then
  python3 - "$CLAUDE_MD" <<'PY'
import sys, io
p = sys.argv[1]
cur = io.open(p, encoding="utf-8").read()
s, e = "<!-- devbrain:start -->", "<!-- devbrain:end -->"
if s in cur and e in cur:
    cur = (cur[:cur.index(s)] + cur[cur.index(e)+len(e):]).strip() + "\n"
    io.open(p, "w", encoding="utf-8").write(cur)
    print("  removed devbrain block")
PY
fi

say "Done. Your brain data repo was left untouched."
