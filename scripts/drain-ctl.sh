#!/usr/bin/env bash
# devbrain/drain — control library for the wall's control pane.
# Sourced into an interactive shell (bash --rcfile drain-ctl.sh -i): gives you a
# scoreboard + commands to monitor, steer, and manage the agent fleet from one pane.
# Config via env (set by drain-wall.sh): DRAIN_BASE, DRAIN_N, DRAIN_SCRIPTS.

DRAIN_BASE="${DRAIN_BASE:-$HOME/drain/chess-equity}"
DRAIN_N="${DRAIN_N:-3}"
DRAIN_SCRIPTS="${DRAIN_SCRIPTS:-$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)}"
_TODO="$HOME/.claude/hooks/devbrain-todo.sh"; [ -x "$_TODO" ] || _TODO="$DRAIN_SCRIPTS/todo.sh"
_SLUG="$(git -C "$DRAIN_BASE" remote get-url origin 2>/dev/null | sed -E 's#(\.git)?$##; s#.*[:/]([^/]+/[^/]+)$#\1#')"

status() {                                   # one-shot scoreboard
  local i sess br st
  echo "── workers ($_SLUG) ──"
  for i in $(seq 0 $((DRAIN_N - 1))); do
    sess="drain-w$i"
    if ! tmux has-session -t "$sess" 2>/dev/null; then printf "  w%s  %-8s —\n" "$i" "(down)"; continue; fi
    br="$(git -C "$DRAIN_BASE-w$i" branch --show-current 2>/dev/null)"
    if tmux capture-pane -t "$sess" -p 2>/dev/null | grep -q "esc to interrupt"; then st="working"; else st="idle"; fi
    printf "  w%s  %-8s %s\n" "$i" "$st" "${br:-—}"
  done
  printf "── queue: %s open ──\n" "$( cd "$DRAIN_BASE" && "$_TODO" list 2>/dev/null | grep -cE '^[[:space:]]*\[' )"
  echo "── open PRs ──"; GH_PAGER=cat gh -R "$_SLUG" pr list 2>/dev/null | head -6 || echo "  (gh n/a)"
  echo "── staging (commits ahead of main) ──"
  git -C "$DRAIN_BASE" fetch -q origin 2>/dev/null
  git -C "$DRAIN_BASE" log --oneline origin/main..origin/staging 2>/dev/null | head -6 | sed 's/^/  /' || echo "  (no staging yet)"
}
s()    { status; }
mon()  { echo "(live; Ctrl-C to return to prompt)"; while :; do clear; status; sleep 8; done; }
prs()  { GH_PAGER=cat gh -R "$_SLUG" pr list 2>/dev/null; }
q()    { ( cd "$DRAIN_BASE" && "$_TODO" list "${1:-}" 2>/dev/null ); }
say()  { local i="$1"; shift; tmux send-keys -t "drain-w$i" -l "$*"; tmux send-keys -t "drain-w$i" Enter; echo "→ w$i: $*"; }
at()   { tmux attach -t "drain-w$1"; }                       # Ctrl-b d to come back
killw(){ tmux kill-session -t "drain-w$1" 2>/dev/null && echo "killed w$1 (orchestrator respawns it if running)"; }
sdiff(){ git -C "$DRAIN_BASE" fetch -q origin 2>/dev/null; git -C "$DRAIN_BASE" diff origin/main...origin/staging; }
wall() { "$DRAIN_SCRIPTS/drain-wall.sh" "$DRAIN_N" "$DRAIN_BASE"; }

ostart(){                                    # start the orchestrator (spawns the fleet)
  if pgrep -f "drain-orchestrate.sh --repo $DRAIN_BASE" >/dev/null 2>&1; then echo "orchestrator already running"; return; fi
  nohup "$DRAIN_SCRIPTS/drain-orchestrate.sh" --repo "$DRAIN_BASE" --workers "${1:-$DRAIN_N}" "${@:2}" >/dev/null 2>&1 &
  echo "orchestrator started (workers=${1:-$DRAIN_N}); 'olog' to watch, 's' for status"
}
ostop(){ pkill -f "drain-orchestrate.sh --repo $DRAIN_BASE" 2>/dev/null && echo "orchestrator stopped (workers keep running; 'killw <i>' to stop them)" || echo "(not running)"; }
olog() { tail -n "${1:-40}" "$DRAIN_BASE/.drain/orchestrator.log" 2>/dev/null || echo "(no orchestrator.log)"; }

help() { cat <<'H'
drain control — commands:
  s / status     scoreboard: workers · queue · PRs · staging
  mon            live scoreboard (Ctrl-C to return)
  prs            open PRs                q [status]   task queue
  say <i> <msg>  steer worker i          at <i>       attach to worker i (Ctrl-b d back)
  killw <i>      kill worker i           wall         reopen the wall
  ostart [N]     start orchestrator      ostop        stop it (workers stay up)
  olog [n]       tail orchestrator log   sdiff        git diff main...staging
  help           this list
H
}
h() { help; }

PS1='drain[\W]$ '
echo "drain control — base=$DRAIN_BASE workers=$DRAIN_N · type 'help'"
status 2>/dev/null
