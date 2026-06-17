#!/usr/bin/env bash
# devbrain/nightshift — control library for the wall's control pane.
# Sourced into an interactive shell (bash --rcfile nightshift-ctl.sh -i): gives you a
# scoreboard + commands to monitor, steer, and manage the agent fleet from one pane.
# Config via env (set by nightshift-wall.sh): NIGHTSHIFT_BASE, NIGHTSHIFT_N, NIGHTSHIFT_SCRIPTS.

NIGHTSHIFT_BASE="${NIGHTSHIFT_BASE:-$HOME/drain/chess-equity}"
NIGHTSHIFT_N="${NIGHTSHIFT_N:-3}"
NIGHTSHIFT_SCRIPTS="${NIGHTSHIFT_SCRIPTS:-$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)}"
_TODO="$HOME/.claude/hooks/devbrain-todo.sh"; [ -x "$_TODO" ] || _TODO="$NIGHTSHIFT_SCRIPTS/todo.sh"
_SLUG="$(git -C "$NIGHTSHIFT_BASE" remote get-url origin 2>/dev/null | sed -E 's#(\.git)?$##; s#.*[:/]([^/]+/[^/]+)$#\1#')"

status() {                                   # one-shot scoreboard
  local i sess br st
  echo "── workers ($_SLUG) ──"
  for i in $(seq 0 $((NIGHTSHIFT_N - 1))); do
    sess="ns-w$i"
    if ! tmux has-session -t "$sess" 2>/dev/null; then printf "  w%s  %-8s —\n" "$i" "(down)"; continue; fi
    br="$(git -C "$NIGHTSHIFT_BASE-w$i" branch --show-current 2>/dev/null)"
    if tmux capture-pane -t "$sess" -p 2>/dev/null | grep -q "esc to interrupt"; then st="working"; else st="idle"; fi
    printf "  w%s  %-8s %s\n" "$i" "$st" "${br:-—}"
  done
  printf "── queue: %s open ──\n" "$( cd "$NIGHTSHIFT_BASE" && "$_TODO" list 2>/dev/null | grep -cE '^[[:space:]]*\[' )"
  echo "── open PRs ──"; GH_PAGER=cat gh -R "$_SLUG" pr list 2>/dev/null | head -6 || echo "  (gh n/a)"
  echo "── staging (commits ahead of main) ──"
  git -C "$NIGHTSHIFT_BASE" fetch -q origin 2>/dev/null
  git -C "$NIGHTSHIFT_BASE" log --oneline origin/main..origin/staging 2>/dev/null | head -6 | sed 's/^/  /' || echo "  (no staging yet)"
}
s()    { status; }
mon()  { echo "(live; Ctrl-C to return to prompt)"; while :; do clear; status; sleep 8; done; }
prs()  { GH_PAGER=cat gh -R "$_SLUG" pr list 2>/dev/null; }
q()    { ( cd "$NIGHTSHIFT_BASE" && "$_TODO" list "${1:-}" 2>/dev/null ); }
say()  { local i="$1"; shift; tmux send-keys -t "ns-w$i" -l "$*"; tmux send-keys -t "ns-w$i" Enter; echo "→ w$i: $*"; }
at()   { tmux attach -t "ns-w$1"; }                       # Ctrl-b d to come back
killw(){ tmux kill-session -t "ns-w$1" 2>/dev/null && echo "killed w$1 (orchestrator respawns it if running)"; }
sdiff(){ git -C "$NIGHTSHIFT_BASE" fetch -q origin 2>/dev/null; git -C "$NIGHTSHIFT_BASE" diff origin/main...origin/staging; }
wall() { "$NIGHTSHIFT_SCRIPTS/nightshift-wall.sh" "$NIGHTSHIFT_N" "$NIGHTSHIFT_BASE"; }

ostart(){                                    # start the orchestrator (spawns the fleet)
  if pgrep -f "nightshift-orchestrate.sh --repo $NIGHTSHIFT_BASE" >/dev/null 2>&1; then echo "orchestrator already running"; return; fi
  nohup "$NIGHTSHIFT_SCRIPTS/nightshift-orchestrate.sh" --repo "$NIGHTSHIFT_BASE" --workers "${1:-$NIGHTSHIFT_N}" "${@:2}" >/dev/null 2>&1 &
  echo "orchestrator started (workers=${1:-$NIGHTSHIFT_N}); 'olog' to watch, 's' for status"
}
ostop(){ pkill -f "nightshift-orchestrate.sh --repo $NIGHTSHIFT_BASE" 2>/dev/null && echo "orchestrator stopped (workers keep running; 'killw <i>' to stop them)" || echo "(not running)"; }
olog() { tail -n "${1:-40}" "$NIGHTSHIFT_BASE/.nightshift/orchestrator.log" 2>/dev/null || echo "(no orchestrator.log)"; }

help() { cat <<'H'
nightshift control — commands:
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

PS1='ns[\W]$ '
echo "nightshift control — base=$NIGHTSHIFT_BASE workers=$NIGHTSHIFT_N · type 'help'"
status 2>/dev/null
