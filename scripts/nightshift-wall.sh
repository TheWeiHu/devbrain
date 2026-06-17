#!/usr/bin/env bash
# devbrain/nightshift — the wall: N read-only worker mirrors + 1 CONTROL pane.
#   ┌──────────┬──────────┐
#   │ worker 0 │ worker 1 │      worker panes: live read-only mirrors (capture-pane,
#   ├──────────┼──────────┤      so they never resize/disturb the running sessions)
#   │ worker 2 │ CONTROL  │      control pane: interactive shell preloaded with
#   └──────────┴──────────┘      scoreboard + fleet-management commands (type 'help')
#
# Usage:  nightshift-wall.sh [N_WORKERS] [BASE_REPO]   (defaults: 3  ~/drain/chess-equity)
# Detach: Ctrl-b d   ·   move panes: Ctrl-b arrows   ·   zoom one: Ctrl-b z

set -euo pipefail
N="${1:-3}"
BASE="${2:-$HOME/drain/chess-equity}"
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
DASH="ns-wall"
VIEWDIR="$BASE/.nightshift"
mkdir -p "$VIEWDIR"
command -v tmux >/dev/null 2>&1 || { echo "ns-wall: tmux not found" >&2; exit 1; }

if tmux has-session -t "$DASH" 2>/dev/null; then exec tmux attach -t "$DASH"; fi

# one read-only mirror viewer per worker
for i in $(seq 0 $((N-1))); do
  cat > "$VIEWDIR/wall-w$i.sh" <<EOF
#!/usr/bin/env bash
while :; do clear; echo "── worker $i (ns-w$i) ──"; tmux capture-pane -t ns-w$i -p -e 2>/dev/null || echo "ns-w$i not running"; sleep 1; done
EOF
  chmod +x "$VIEWDIR/wall-w$i.sh"
done

# Big virtual size + re-tile after each split so every pane has room.
tmux new-session -d -s "$DASH" -n wall -x 240 -y 70 "bash '$VIEWDIR/wall-w0.sh'"
for i in $(seq 1 $((N-1))); do
  tmux split-window -t "$DASH" "bash '$VIEWDIR/wall-w$i.sh'"; tmux select-layout -t "$DASH" tiled
done
# control pane: interactive shell with the nightshift-ctl command library loaded
tmux split-window -t "$DASH" \
  "NIGHTSHIFT_BASE='$BASE' NIGHTSHIFT_N='$N' NIGHTSHIFT_SCRIPTS='$SELF_DIR' bash --rcfile '$SELF_DIR/nightshift-ctl.sh' -i"
tmux select-layout -t "$DASH" tiled
tmux select-pane -t "$DASH" -l 2>/dev/null || true   # focus the control pane (last)
exec tmux attach -t "$DASH"
