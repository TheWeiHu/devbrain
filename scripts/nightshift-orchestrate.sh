#!/usr/bin/env bash
# devbrain/nightshift — multi-worker ORCHESTRATOR.
#
# Runs N `claude` workers in parallel, each in its OWN git worktree (devbrain's
# "one worktree ↔ one branch ↔ one issue" rule — required so parallel workers
# don't collide; the queue's `claim` keeps them off the same task). The
# orchestrator assigns /continue to idle workers, gates + merges each completed
# turn into `staging`, replans when the queue empties, and runs FOREVER (bound
# with --max-turns / --max-wall, or stop via ostop / Ctrl-C).
#
# ── TWO EXECUTION BACKENDS ───────────────────────────────────────────────────
# headless  (DEFAULT) — one `claude -p` per turn per worker. The process IS the
#           turn: its exit is the turn boundary, its exit code/stdout the result.
#           No tmux, no Stop-hook marker, no terminal-scraping — far simpler and
#           more robust. This is what you want.
#   --tmux  (FALLBACK) — drive a persistent interactive `claude` in a tmux pane
#           via send-keys, detecting turn-completion with a Stop-hook marker file
#           and scraping the pane for state. More moving parts; kept only as a hedge
#           against a future `claude -p` pricing change (the `nightshift` CLI prints
#           the full why at `start --tmux`, and it's in the no-arg help).
#
# Watch (either mode):  nightshift watch   (browser dashboard)
# Watch a tmux worker:  tmux attach -t ns-w0      (--tmux mode only)
#
# Usage:  nightshift-orchestrate.sh --repo BASE_CLONE [options]
#   --workers N      parallel workers           (default 3)
#   --tmux           use the interactive tmux backend instead of headless claude -p
#   --turn-timeout S max seconds for one headless turn (default 1800; SIGTERM after)
#   --hang SECS      frozen-pane hang threshold  (default 600; --tmux only)
#   --low N          replenish when open<N       (default 2)
#   --max-turns N    total turns across workers  (default 30)
#   --max-wall SECS  hard wall-clock stop        (default 28800 = 8h)
#   --poll SECS      poll interval               (default 15)
#   --base-branch B  branch staging is cut from  (default main)
#   --keep-staging   accumulate onto existing staging instead of resetting it
#   --test-cmd CMD   green-gate command (default: auto pytest in a venv)
#   --no-gate        merge without running tests (staging is disposable anyway)
#   --strict-gate    treat an inconclusive gate (no tests/tooling) as FAIL
#   --retries N      merge re-attempts before parking a task for the human (default 2)
#
# COMPOUNDING: workers branch off origin/staging (not main); on turn-complete the
# orchestrator merges the worker branch into staging IF the green-gate passes
# (serialized — the single orchestrator loop is the merge lock), marks the task
# `done`, and pushes. Conflicts / red tests requeue the task. You review and merge
# `git diff main...staging` → main yourself.

set -uo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
TODO="$HOME/.claude/hooks/devbrain-todo.sh"; [ -x "$TODO" ] || TODO="$SELF_DIR/todo.sh"

BASE=""; N=3; HANG=600; LOW=2; MAXTURNS=0; MAXWALL=0; POLL=15; REPLAN=300; FOREVER=1
BASE_BRANCH=main; KEEP_STAGING=0; TEST_CMD=""; NO_GATE=0; STRICT=0; RETRIES=2
MODE=headless; TURN_MAX=1800   # default backend = claude -p; per-turn wall cap (s)
STALL_K=8; RECON_EVERY=8   # stall after K turns with no new merge; reconcile every N polls
NOTIFY=0                   # macOS notifications OFF by default; --notify to enable
LIMIT_BACKOFF=300          # on a usage limit, poll/ping only every 5 min (not aggressively)
RESEND_GRACE=60            # don't re-send /continue within this many s of the last send (kills startup spam)
# Defaults run FOREVER: 0 caps = unlimited. Workers are respawned if they die or go
# idle with no work; when the queue empties, a planning turn refills it (--replan).
# Stop with `ostop` / Ctrl-C, or set --max-turns / --max-wall to bound a run.
while [ $# -gt 0 ]; do case "$1" in
  --repo)        BASE="$2"; shift 2;;
  --workers)     N="$2"; shift 2;;
  --tmux)        MODE=tmux; shift;;
  --headless)    MODE=headless; shift;;
  --turn-timeout) TURN_MAX="$2"; shift 2;;
  --hang)        HANG="$2"; shift 2;;
  --low)         LOW="$2"; shift 2;;
  --max-turns)   MAXTURNS="$2"; FOREVER=0; shift 2;;
  --max-wall)    MAXWALL="$2"; FOREVER=0; shift 2;;
  --replan)      REPLAN="$2"; shift 2;;
  --poll)        POLL="$2"; shift 2;;
  --base-branch) BASE_BRANCH="$2"; shift 2;;
  --keep-staging) KEEP_STAGING=1; shift;;
  --test-cmd)    TEST_CMD="$2"; shift 2;;
  --no-gate)     NO_GATE=1; shift;;
  --strict-gate) STRICT=1; shift;;
  --retries)     RETRIES="$2"; shift 2;;
  --notify)      NOTIFY=1; shift;;
  *) echo "orch: unknown arg $1" >&2; exit 1;;
esac; done

STAGE_WT="$BASE-stage"; VENV="$BASE/.nightshift/venv"; RETRYDIR="$BASE/.nightshift/retries"
RULES_FILE="$BASE/.nightshift/drain-rules.txt"   # rules go in a file (read at launch) — NOT inline in the shell command, so quotes/newlines in the text can't break the launch

[ "$MODE" = tmux ] && { command -v tmux >/dev/null 2>&1 || { echo "orch: tmux not found (required for --tmux mode)" >&2; exit 1; }; }
command -v claude >/dev/null 2>&1 || { echo "orch: claude not found" >&2; exit 1; }
[ -n "$BASE" ] || { echo "orch: --repo is required" >&2; exit 1; }
BASE="$(cd "$BASE" && pwd)" || { echo "orch: --repo not a dir" >&2; exit 1; }

NIGHTSHIFT_RULES="NIGHTSHIFT (unattended) MODE: you are running unattended in an automated loop; there is no human to answer questions this turn. Never ask the user anything and never use AskUserQuestion. BASE-HEALTH FIRST: before building anything, glance at \`devbrain-todo list held\` — if several 'red gate' / 'tests failed' holds are clustered there, the shared base (origin/staging) is probably broken; confirm by running the test suite against origin/staging, and if staging's OWN tests FAIL, that is priority zero: do NOT build a queued feature on a red base — instead diagnose and fix the failing test and open a PR that makes staging green (skip if another worker already holds the fix per \`devbrain-todo list taken\`). Only when the base is green, proceed with the normal task pickup. Base every task on origin/staging, NOT main: when the /continue protocol says to branch off origin/main, branch off origin/staging instead and open your PR against the staging branch. If the task you claimed shows a last_failure field (a previous attempt failed), that is your FIRST priority: read it, reproduce the named failing test or merge conflict, and fix THAT specifically before adding anything else. When you would ask follow-up questions, instead append them to .nightshift/followups.md and queue them as TODOs via the devbrain-todo CLI. Be conservative about adding TODOs — only queue a follow-up that is essential to the objective and not already in the queue; the goal is to DRAIN the queue toward the objective, not grow it. If the task you picked CANNOT be done unattended (it needs a missing binary, a large dataset download, network/API credentials, or GPU/torch/model weights), do NOT spin on it: run \`devbrain-todo hold <id> \"<one-line why>\"\` to park it for a human, then end your turn. EXCEPTION: if the task shows \`approved: true\` in its frontmatter, a human has greenlit unattended execution — you ARE authorized to download datasets, pip install (including torch), and use the network/credentials it needs; complete it instead of holding. Build a minimal MVP for the current task only, then end your turn."

PLAN_RULES="PLANNING TURN: the task queue is low. Do NOT write code or open a PR this turn. Read .nightshift/followups.md (if present) and the project objective (run: gbrain search for this project, and read its objective.md under the devbrain-data brain). Then add 3 to 6 concrete, minimal next TODOs that advance the objective via the devbrain-todo CLI (devbrain-todo add \"title\" -p PRIORITY -b \"why/acceptance\"), deduped against existing open tasks. Then end your turn."

# ---- shared helpers ----------------------------------------------------------
pane()  { tmux capture-pane -t "$1" -p 2>/dev/null; }
is_idle() {  # $1 session — footer present AND not mid-turn
  local p; p="$(pane "$1")" || return 1
  printf '%s' "$p" | grep -q "bypass permissions\|to cycle\|? for shortcuts" || return 1
  printf '%s' "$p" | grep -q "esc to interrupt" && return 1
  return 0
}
open_count() { ( cd "$BASE" && "$TODO" list 2>/dev/null ) | grep -cE '^[[:space:]]*\['; }
hashpane() { pane "$1" | cksum | awk '{print $1}'; }

handle_prompts() {  # $1 session — auto-clear trust + menus so nothing blocks
  local s="$1" p; p="$(pane "$s")"
  if printf '%s' "$p" | grep -qiE "trust this folder|trust the (files|authors)|Is this a project you"; then
    tmux send-keys -t "$s" "1"; tmux send-keys -t "$s" Enter; return 0
  fi
  if printf '%s' "$p" | grep -qE "Enter to select|Tab/Arrow keys to navigate"; then
    { echo "## menu @ $(date -u +%FT%TZ) [$s]"; printf '%s\n\n' "$p"; } >> "$BASE/.nightshift/followups.md" 2>/dev/null
    tmux send-keys -t "$s" Enter   # take the agent's recommended (highlighted) option
    return 0
  fi
  return 1
}
is_stuck_error() { printf '%s' "$(pane "$1")" | grep -qiE "API Error|Overloaded|\b529\b|usage limit|resets at"; }
# Any worker showing a real USAGE LIMIT (not a transient 529) → back off to a 5-min cadence.
usage_limited() {
  local i
  for i in $(seq 0 $((N - 1))); do
    printf '%s' "$(pane "${SESS[$i]}")" | grep -qiE "usage limit|limit reached|resets? (at|in)|approaching .*limit|out of .*credit|quota" && return 0
  done
  return 1
}

send_prompt() {  # robust submit: clear stale menu/input, type, then Enter
  tmux send-keys -t "$1" Escape 2>/dev/null     # dismiss any open slash-command autocomplete
  tmux send-keys -t "$1" C-u    2>/dev/null     # clear the input line (no leftover "continue" to concat)
  tmux send-keys -t "$1" -l "$2"
  sleep 0.5                                      # let the slash menu populate so Enter runs the command
  tmux send-keys -t "$1" Enter
}

spawn_worker() {  # $1 index
  local i="$1" wt sess marker
  wt="$BASE-w$i"; sess="ns-w$i"; marker="$wt/.nightshift/w$i.turns"
  git -C "$BASE" worktree prune 2>/dev/null
  git -C "$BASE" fetch -q origin 2>/dev/null
  [ -d "$wt" ] || git -C "$BASE" worktree add -f --detach "$wt" origin/staging >/dev/null 2>&1
  mkdir -p "$wt/.nightshift"
  tmux kill-session -t "$sess" 2>/dev/null; sleep 1   # let the killed pane's processes go
  tmux new-session -d -s "$sess" -c "$wt" -x 200 -y 50
  local launch="claude --dangerously-skip-permissions --disallowedTools AskUserQuestion mcp__conductor__AskUserQuestion --append-system-prompt \"\$(cat '$RULES_FILE')\""
  # Wait for the (zsh) shell to finish starting before typing — sending keystrokes
  # before the prompt is ready mangles the launch (the respawn-into-garbage bug).
  sleep 2
  tmux send-keys -t "$sess" -l "export NIGHTSHIFT_MARKER='$marker'; $launch"; tmux send-keys -t "$sess" Enter
  # Confirm claude actually came up; if the shell was slow, Ctrl-C + relaunch once.
  local r ok=0
  for r in $(seq 1 15); do
    tmux capture-pane -t "$sess" -p 2>/dev/null | grep -q "bypass permissions" && { ok=1; break; }
    sleep 1
  done
  if [ "$ok" = 0 ]; then
    tmux send-keys -t "$sess" C-c 2>/dev/null; sleep 1
    tmux send-keys -t "$sess" -l "export NIGHTSHIFT_MARKER='$marker'; $launch"; tmux send-keys -t "$sess" Enter
    echo "orch: worker $i launch retried (shell was slow to ready)"
  fi
  WT[$i]="$wt"; SESS[$i]="$sess"; MARKER[$i]="$marker"
  BASE_CNT[$i]=0; LASTHASH[$i]=""; LASTCHG[$i]=$(date +%s); STATE[$i]="booting"; PROMPT_SENT[$i]=""
  echo "orch: spawned worker $i ($sess) in $wt"
}
mcount() { [ -f "${MARKER[$1]}" ] && wc -l < "${MARKER[$1]}" | tr -d ' ' || echo 0; }

# ---- headless backend (claude -p) — the DEFAULT ------------------------------
# One `claude -p` per turn per worker. No tmux, no Stop-hook marker, no pane
# scraping: the process is the turn (exit = turn boundary, exit code/log = result).
# Workers still each get their own worktree off origin/staging.
spawn_worker_headless() {  # $1 index — ensure the worktree exists; turns run on demand
  local i="$1" wt; wt="$BASE-w$i"
  git -C "$BASE" worktree prune 2>/dev/null
  git -C "$BASE" fetch -q origin 2>/dev/null
  [ -d "$wt" ] || git -C "$BASE" worktree add -f --detach "$wt" origin/staging >/dev/null 2>&1
  mkdir -p "$wt/.nightshift"
  WT[$i]="$wt"; WTLOG[$i]="$wt/.nightshift/turn.log"; WTPID[$i]=""
  STATE[$i]="idle"; LASTCHG[$i]=$(date +%s); PROMPT_SENT[$i]=""
  echo "orch: worker $i worktree ready ($wt) [headless]"
}
run_headless_turn() {  # $1 index ; $2 prompt — launch one claude -p turn in the background
  local i="$1" prompt="$2" wt="${WT[$i]}" log="${WTLOG[$i]}"
  : > "$log"
  # The rules go in --append-system-prompt as a real argument (not typed into a
  # TUI), so quotes/newlines in them can't break anything — the whole reason the
  # headless backend is less hacky than --tmux. `timeout` bounds a runaway turn.
  ( cd "$wt" && exec timeout "$TURN_MAX" claude -p "$prompt" \
       --dangerously-skip-permissions \
       --disallowedTools AskUserQuestion mcp__conductor__AskUserQuestion \
       --append-system-prompt "$(cat "$RULES_FILE")" ) >>"$log" 2>&1 &
  WTPID[$i]=$!; PROMPT_SENT[$i]="$prompt"
}
hl_step() {  # $1 index — one poll step for a headless worker
  local i="$1" rc br
  if [ -n "${WTPID[$i]}" ]; then
    if kill -0 "${WTPID[$i]}" 2>/dev/null; then STATE[$i]="working"; return; fi   # turn in progress
    wait "${WTPID[$i]}" 2>/dev/null; rc=$?; WTPID[$i]=""; STATE[$i]="idle"
    TURNS_DONE=$((TURNS_DONE + 1))
    echo "orch: worker $i finished a turn rc=$rc (total turns: $TURNS_DONE)"
    [ "$rc" = 124 ] && echo "orch: worker $i turn TIMED OUT after ${TURN_MAX}s"
    # exit code/stdout replace the pane-scrape: a usage limit shows in the log.
    grep -qiE "usage limit|limit reached|out of .*credit|quota|resets? (at|in)" "${WTLOG[$i]}" 2>/dev/null && LIMIT_HIT=1
    br="$(git -C "${WT[$i]}" branch --show-current 2>/dev/null)"
    case "$br" in
      todo/*) if merge_to_staging "$br" "${br#todo/}"; then NOMERGE=0; else NOMERGE=$((NOMERGE + 1)); fi;;
      *)      NOMERGE=$((NOMERGE + 1));;   # planning / no-branch turn → no merge
    esac
    return   # harvested this poll; assign the next turn on the following poll
  fi
  # idle → decide the next turn (SAME policy as the tmux backend)
  if [ "$STALLED" = 1 ] || [ "$NOMERGE" -ge "$STALL_K" ]; then STATE[$i]="parked"; return; fi
  [ "$BASE_RED" = 1 ] && [ "$BR_ASSIGNED" -ge 1 ] && { STATE[$i]="parked"; return; }   # red base → feed one fixer only
  if [ "$oc" -gt 0 ]; then
    run_headless_turn "$i" "/continue"; STATE[$i]="working"; BR_ASSIGNED=$((BR_ASSIGNED + 1))
    echo "orch: worker $i started /continue (open=$oc)"
  elif [ $((now - PLANNED_LAST)) -gt "$REPLAN" ]; then
    echo "orch: queue empty — worker $i planning (replenish)"
    run_headless_turn "$i" "$PLAN_RULES"; STATE[$i]="working"; PLANNED_LAST=$now
  else
    STATE[$i]="parked"
  fi
}

release_branch_task() {  # $1 index — free the task this worker's worktree had claimed
  local b; b="$(git -C "${WT[$1]}" branch --show-current 2>/dev/null)"
  case "$b" in todo/*) ( cd "$BASE" && "$TODO" release "${b#todo/}" 2>/dev/null ) && echo "orch: released ${b#todo/}";; esac
}

# Ensure the turn-marker Stop hook is installed globally (guarded by NIGHTSHIFT_MARKER,
# so it only fires for workers). Global — NOT per-worktree — because /continue's
# `git stash -u` would stash a worktree-local .claude/settings.json mid-turn.
ensure_marker_hook() {
  local hook="$HOME/.claude/hooks/devbrain-turn-marker.sh" src=""
  for c in "$SELF_DIR/../hooks/turn-marker.sh" "$SELF_DIR/turn-marker.sh"; do [ -f "$c" ] && { src="$c"; break; }; done
  mkdir -p "$HOME/.claude/hooks"
  [ -n "$src" ] && { cp "$src" "$hook"; chmod +x "$hook"; }
  [ -f "$hook" ] || { echo "orch: WARN turn-marker.sh not found — markers will not fire"; return; }
  command -v jq >/dev/null 2>&1 || { echo "orch: WARN jq missing — register Stop hook manually: $hook"; return; }
  local set="$HOME/.claude/settings.json" tmp; [ -f "$set" ] || echo '{}' > "$set"
  if ! grep -q "devbrain-turn-marker" "$set" 2>/dev/null; then
    tmp="$(mktemp)"
    jq --arg c "$hook" '.hooks.Stop = ((.hooks.Stop // []) + [{"hooks":[{"type":"command","command":$c}]}])' "$set" > "$tmp" && mv "$tmp" "$set" \
      && echo "orch: registered turn-marker Stop hook globally"
  fi
}

# ---- staging + green-gate + serialized automerge -----------------------------
setup_staging() {
  git -C "$BASE" fetch -q origin
  if [ "$KEEP_STAGING" = 1 ] && git -C "$BASE" ls-remote --exit-code --heads origin staging >/dev/null 2>&1; then
    echo "orch: keeping existing origin/staging"
  else
    git -C "$BASE" branch -f staging "origin/$BASE_BRANCH"
    git -C "$BASE" push -f -q origin staging
    echo "orch: staging reset to origin/$BASE_BRANCH"
  fi
  git -C "$BASE" worktree prune 2>/dev/null
  [ -d "$STAGE_WT" ] || git -C "$BASE" worktree add -f "$STAGE_WT" staging >/dev/null 2>&1
  git -C "$STAGE_WT" checkout -q staging 2>/dev/null; git -C "$STAGE_WT" reset -q --hard origin/staging
  mkdir -p "$RETRYDIR"
  # Exclude the state dir in ALL worktrees (shared info/exclude) so /continue's
  # `git add -A` never commits markers/logs into a task's PR.
  local excl="$BASE/.git/info/exclude"
  [ -f "$excl" ] && ! grep -qxF '.nightshift/' "$excl" 2>/dev/null && echo '.nightshift/' >> "$excl"
  if [ "$NO_GATE" != 1 ] && [ -z "$TEST_CMD" ]; then
    # Upgrade pip/setuptools/wheel FIRST — the venv default pip can be too old to do
    # PEP 660 editable installs from a pyproject-only project, which silently breaks
    # `pip install -e .` and leaves the package + its deps uninstalled (rc=2 gate).
    python3 -m venv "$VENV" >/dev/null 2>&1 \
      && "$VENV/bin/pip" install -q --upgrade pip setuptools wheel >/dev/null 2>&1 \
      && "$VENV/bin/pip" install -q pytest >/dev/null 2>&1 \
      && echo "orch: green-gate venv ready (pytest)" || echo "orch: WARN gate venv unavailable — gate may be inconclusive"
  fi
}

run_gate() {  # $1 dir → 0 pass · 1 fail · 2 inconclusive ; sets global GATE_DETAIL on fail
  local dir="$1" out rc; GATE_DETAIL=""
  if [ -n "$TEST_CMD" ]; then
    out="$( cd "$dir" && timeout 600 bash -c "$TEST_CMD" 2>&1 )"; rc=$?
    [ "$rc" -eq 0 ] && { echo "  gate PASS: $TEST_CMD"; return 0; }
    GATE_DETAIL="$(printf '%s' "$out" | tail -3 | tr '\n' ' ' | cut -c1-240)"
    echo "  gate FAIL ($TEST_CMD): $GATE_DETAIL"; return 1
  fi
  [ -x "$VENV/bin/python" ] || { echo "  gate inconclusive (no venv)"; return 2; }
  # Install the package + its declared deps (dev extras if present) so pytest can
  # actually import it. If this fails the suite won't collect → rc=2 → FAIL below,
  # which is correct: a task that can't be installed/imported must not merge.
  ( cd "$dir" && { "$VENV/bin/pip" install -q -e ".[dev]" >/dev/null 2>&1 || "$VENV/bin/pip" install -q -e . >/dev/null 2>&1; } ) || true
  out="$( cd "$dir" && timeout 600 "$VENV/bin/python" -m pytest -q 2>&1 )"; rc=$?
  GATE_DETAIL="$(printf '%s' "$out" | grep -E '^(FAILED|ERROR)' | head -4 | tr '\n' ' ')"
  [ -n "$GATE_DETAIL" ] || GATE_DETAIL="$(printf '%s' "$out" | tail -3 | tr '\n' ' ' | cut -c1-240)"
  case "$rc" in
    0) echo "  gate PASS (pytest)"; return 0;;
    5) echo "  gate inconclusive (no tests collected)"; return 2;;
    1) echo "  gate FAIL (pytest): $GATE_DETAIL"; return 1;;
    2) echo "  gate FAIL (collection/import error): $GATE_DETAIL"; return 1;;
    *) echo "  gate inconclusive (pytest rc=$rc)"; return 2;;
  esac
}

notify() {  # $1 title-suffix · $2 message — native macOS toast (best-effort)
  [ "$NOTIFY" = 1 ] || return 0   # off by default (enable with --notify)
  command -v osascript >/dev/null 2>&1 && \
    osascript -e "display notification \"$2\" with title \"nightshift\" subtitle \"$1\"" 2>/dev/null || true
}
requeue() {  # $1 id ; $2 why — release back to open, or PARK for the human after $RETRIES
  local id="$1" why="${2:-could not merge}" f="$RETRYDIR/$id" n; n=$(cat "$f" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > "$f"
  ( cd "$BASE" && "$TODO" note "$id" "attempt $n — $why" 2>/dev/null )   # feedback the next worker reads via `todo show`
  if [ "$n" -le "$RETRIES" ]; then ( cd "$BASE" && "$TODO" release "$id" 2>/dev/null ); echo "  requeued $id (attempt $n/$RETRIES): $why"
  else
    ( cd "$BASE" && "$TODO" hold "$id" "$why (after $RETRIES attempts)" 2>/dev/null )
    echo "  ⚠ $id held after ${n} attempts — $why (needs you)"
    notify "needs your review" "$id: $why"
  fi
}

task_status() { ( cd "$BASE" && "$TODO" show "$1" 2>/dev/null ) | sed -n 's/^status:[[:space:]]*//p' | head -1; }

# Serialized by construction: only the single orchestrator loop calls this.
# Returns: 0 NEW merge · 2 already-in-staging (no-op) · 1 conflict/fail/not-pushed.
merge_to_staging() {  # $1 branch (todo/<id>) ; $2 task id
  local br="$1" id="$2" verdict
  git -C "$BASE" ls-remote --exit-code --heads origin "$br" >/dev/null 2>&1 || { echo "orch:   $br not pushed — requeue"; requeue "$id" "worker turn produced no pushed branch"; return 1; }
  git -C "$BASE" fetch -q origin
  # Already in staging (e.g. a stale branch from a no-op turn) → ensure done, never
  # re-merge. This kills the re-merge churn (was 60×) AND makes reconcile cheap.
  if git -C "$BASE" merge-base --is-ancestor "origin/$br" origin/staging 2>/dev/null; then
    ( cd "$BASE" && "$TODO" done "$id" 2>/dev/null ); return 2
  fi
  git -C "$STAGE_WT" checkout -q staging 2>/dev/null; git -C "$STAGE_WT" reset -q --hard origin/staging
  if ! git -C "$STAGE_WT" merge --no-ff -q -m "nightshift: merge $br into staging" "origin/$br" >/dev/null 2>&1; then
    local cf; cf="$(git -C "$STAGE_WT" diff --name-only --diff-filter=U 2>/dev/null | tr '\n' ' ')"
    git -C "$STAGE_WT" merge --abort 2>/dev/null
    echo "orch: ✗ $br CONFLICTS with staging ($cf)"; requeue "$id" "merge conflict with staging in: ${cf:-?} — rebuild on current origin/staging and resolve"; return 1
  fi
  if [ "$NO_GATE" = 1 ]; then verdict=0; else run_gate "$STAGE_WT"; verdict=$?; fi
  if [ "$verdict" -eq 0 ] || { [ "$verdict" -eq 2 ] && [ "$STRICT" != 1 ]; }; then
    if git -C "$STAGE_WT" push -q origin staging; then
      ( cd "$BASE" && "$TODO" done "$id" 2>/dev/null ); echo "orch: ✓ merged $br → staging; task $id done"; return 0
    else
      git -C "$STAGE_WT" reset -q --hard origin/staging
      echo "orch: ✗ push of staging failed for $br — requeue"; requeue "$id" "git push to staging failed"; return 1
    fi
  else
    git -C "$STAGE_WT" reset -q --hard origin/staging
    echo "orch: ✗ $br failed gate — not merged"; requeue "$id" "gate failed: ${GATE_DETAIL:-tests failed} — reproduce by merging your branch onto origin/staging and running the test suite"; return 1
  fi
}

# Self-heal: merge any pushed todo/* branch stranded out of staging — e.g. a turn
# whose merge was never triggered (the PR #11 case). Idempotent and cheap: branches
# already in staging are skipped by the ancestor check before any heavy work.
reconcile() {
  git -C "$BASE" fetch -q origin 2>/dev/null
  local line br id st
  while IFS= read -r line; do
    br="${line##*refs/heads/}"; [ -n "$br" ] || continue
    git -C "$BASE" merge-base --is-ancestor "origin/$br" origin/staging 2>/dev/null && continue
    id="${br#todo/}"; st="$(task_status "$id")"
    { [ "$st" = "held" ] || [ "$st" = "done" ]; } && continue
    # already gave up on this branch (hit the retry cap) — don't reconcile-loop a
    # stale branch that keeps conflicting (this was spinning 200-300× overnight).
    [ "$(cat "$RETRYDIR/$id" 2>/dev/null || echo 0)" -ge "$RETRIES" ] && continue
    echo "orch: ♻ reconcile — $br is pushed but not in staging; merging"
    merge_to_staging "$br" "$id"
  done < <(git -C "$BASE" ls-remote --heads origin 'todo/*' 2>/dev/null)
}

# Base-health gate: is the base (origin/staging) green ON ITS OWN? If red, every task
# merge is doomed, so we auto-file a top-priority fix task instead of churning /continue.
base_gate() {  # 0 = staging green/inconclusive · 1 = staging RED (GATE_DETAIL set)
  [ "$NO_GATE" = 1 ] && return 0
  git -C "$BASE" fetch -q origin 2>/dev/null
  git -C "$STAGE_WT" checkout -q staging 2>/dev/null; git -C "$STAGE_WT" reset -q --hard origin/staging
  run_gate "$STAGE_WT"; case $? in 0|2) return 0;; *) return 1;; esac
}
ensure_base_fix_task() {  # $1 = failing detail — file ONE high-priority fix task (deduped)
  ( cd "$BASE" && "$TODO" list all 2>/dev/null ) | grep -i "staging is red" | grep -qv "done" && return 0
  ( cd "$BASE" && "$TODO" add "STAGING IS RED — fix the failing test(s) to unblock all merges" -p 99 \
      -b "origin/staging fails its OWN test suite, so EVERY task merge fails the gate — the whole fleet is blocked until this is green. Fix the failing test(s) and push staging green. Failing: ${1:-?}. Reproduce: checkout staging, pip install -e '.[dev]', python -m pytest -q." >/dev/null 2>&1 )
  echo "orch: 🩺 staging RED → filed priority-99 fix task — ${1:-?}"
}

# ---- boot --------------------------------------------------------------------
mkdir -p "$BASE/.nightshift"
printf '%s' "$NIGHTSHIFT_RULES" > "$RULES_FILE"   # workers read the rules from here at launch
exec > >(tee -a "$BASE/.nightshift/orchestrator.log") 2>&1   # stable log for the wall pane
echo "orch: starting $N workers on $BASE | mode=$MODE gate=$([ "$NO_GATE" = 1 ] && echo off || echo on)$([ "$MODE" = headless ] && echo " turn-timeout=${TURN_MAX}s" || echo " hang=${HANG}s")"
[ "$MODE" = tmux ] && ensure_marker_hook   # the Stop-hook marker is only needed for the tmux backend
setup_staging        # staging must exist before workers branch off it
declare -a WT SESS MARKER BASE_CNT LASTHASH LASTCHG STATE PROMPT_SENT WTLOG WTPID
for i in $(seq 0 $((N-1))); do
  if [ "$MODE" = headless ]; then spawn_worker_headless "$i"; else spawn_worker "$i"; fi
done
[ "$MODE" = tmux ] && echo "orch: workers booting; watch any with: tmux attach -t ns-w0"

START=$(date +%s); TURNS_DONE=0; PLANNED_LAST=0; NOMERGE=0; STALLED=0; LOOPS=0; BASE_RED=0; BR_ASSIGNED=0; LIMIT_HIT=0
reconcile   # self-heal any branch stranded out of staging from a prior run (e.g. PR #11)
if ! base_gate; then BASE_RED=1; ensure_base_fix_task "$GATE_DETAIL"; fi   # don't build on a red base
[ "$FOREVER" = 1 ] && echo "orch: running FOREVER — respawns dead/idle workers, replans every ${REPLAN}s; stop with ostop/Ctrl-C"

# ---- the orchestration loop --------------------------------------------------
while :; do
  now=$(date +%s)
  [ "$MAXWALL"  -gt 0 ] && [ $((now - START)) -ge "$MAXWALL" ] && { echo "orch: wall-clock cap hit"; break; }
  [ "$MAXTURNS" -gt 0 ] && [ "$TURNS_DONE" -ge "$MAXTURNS" ]   && { echo "orch: max-turns cap hit"; break; }

  oc="$(open_count)"
  [ "$STALLED" = 1 ] && [ "$oc" -gt 0 ] && { echo "orch: ▶ resuming — $oc open task(s) available"; STALLED=0; NOMERGE=0; }
  LOOPS=$((LOOPS + 1))
  if [ $((LOOPS % RECON_EVERY)) -eq 0 ]; then
    reconcile
    if base_gate; then [ "$BASE_RED" = 1 ] && echo "orch: ✅ staging green again — resuming full fleet"; BASE_RED=0
    else BASE_RED=1; ensure_base_fix_task "$GATE_DETAIL"; fi
  fi
  BR_ASSIGNED=0   # while BASE_RED, only one worker is fed per cycle (funnel to the fix, no churn)
  for i in $(seq 0 $((N-1))); do
    if [ "$MODE" = headless ]; then
      [ -d "${WT[$i]:-}" ] || spawn_worker_headless "$i"   # re-create a deleted worktree
      hl_step "$i"
      continue
    fi
    s="${SESS[$i]}"
    # respawn a worker whose session died (crash / closed)
    if ! tmux has-session -t "$s" 2>/dev/null; then
      echo "orch: worker $i session gone — respawning"; spawn_worker "$i"; s="${SESS[$i]}"; continue
    fi
    handle_prompts "$s" >/dev/null && { LASTCHG[$i]=$now; continue; }   # cleared a blocker

    cur="$(mcount "$i")"
    if [ "$cur" -gt "${BASE_CNT[$i]}" ]; then           # turn finished
      TURNS_DONE=$((TURNS_DONE + 1)); BASE_CNT[$i]="$cur"; STATE[$i]="idle"
      echo "orch: worker $i finished a turn (total turns: $TURNS_DONE)"
      # gate + merge the work this turn produced; track stall (no NEW merge).
      br="$(git -C "${WT[$i]}" branch --show-current 2>/dev/null)"
      case "$br" in
        todo/*) if merge_to_staging "$br" "${br#todo/}"; then NOMERGE=0; else NOMERGE=$((NOMERGE + 1)); fi;;
        *)      NOMERGE=$((NOMERGE + 1));;   # planning / no-branch turn → no merge
      esac
    fi

    if is_idle "$s"; then
      # stalled (or about to be) → don't hand out more work; the fleet goes quiet
      if [ "$STALLED" = 1 ] || [ "$NOMERGE" -ge "$STALL_K" ]; then STATE[$i]="parked"; continue; fi
      if [ "${STATE[$i]}" = "assigned" ]; then
        # idle but marker didn't advance — could be an API error OR the turn just hasn't
        # started yet. Wait out RESEND_GRACE so we don't double-send during startup (the
        # /continue spam). If a usage limit, the loop's 5-min backoff paces this.
        [ $((now - ${LASTCHG[$i]})) -lt "$RESEND_GRACE" ] && continue
        if is_stuck_error "$s"; then echo "orch: worker $i hit API/limit — resending"; fi
        send_prompt "$s" "${PROMPT_SENT[$i]}"; LASTCHG[$i]=$now; continue
      fi
      # while the base is red, feed only ONE worker per cycle (the priority-99 fix) — no churn
      [ "$BASE_RED" = 1 ] && [ "$BR_ASSIGNED" -ge 1 ] && { STATE[$i]="parked"; continue; }
      # needs an assignment
      if [ "$oc" -gt 0 ]; then
        send_prompt "$s" "/continue"; PROMPT_SENT[$i]="/continue"
        STATE[$i]="assigned"; BASE_CNT[$i]="$cur"; LASTCHG[$i]=$now; BR_ASSIGNED=$((BR_ASSIGNED + 1))
        echo "orch: worker $i assigned /continue (open=$oc)"
      elif [ $((now - PLANNED_LAST)) -gt "$REPLAN" ]; then
        # queue empty → generate more work so the fleet never starves (forever mode)
        echo "orch: queue empty — worker $i planning (replenish)"
        send_prompt "$s" "$PLAN_RULES"; PROMPT_SENT[$i]="$PLAN_RULES"
        STATE[$i]="assigned"; BASE_CNT[$i]="$cur"; LASTCHG[$i]=$now; PLANNED_LAST=$now
      else
        STATE[$i]="parked"   # no work + planned recently — re-plans after $REPLAN s
      fi
    else
      # busy: detect a hang via a frozen pane
      h="$(hashpane "$s")"
      if [ "$h" = "${LASTHASH[$i]}" ]; then
        if is_stuck_error "$s"; then LASTCHG[$i]=$now            # waiting out API/limit ≠ hang
        elif [ $((now - LASTCHG[$i])) -ge "$HANG" ]; then
          echo "orch: worker $i HUNG (${HANG}s frozen) — restarting"
          release_branch_task "$i"; tmux kill-session -t "$s" 2>/dev/null
          spawn_worker "$i"; continue
        fi
      else
        LASTHASH[$i]="$h"; LASTCHG[$i]=$now
      fi
    fi
  done

  # Convergence: K turns with no NEW merge while open work remains → those open
  # tasks are undoable unattended. HOLD them (so `next` stops handing them out),
  # ping the human, and go quiet. The loop keeps running; it resumes the moment a
  # human releases a held task (open>0 → cleared at the top). No --max-turns needed.
  if [ "$STALLED" = 0 ] && [ "$NOMERGE" -ge "$STALL_K" ] && [ "$oc" -gt 0 ]; then
    n=0
    for id in $( ( cd "$BASE" && "$TODO" list 2>/dev/null ) | grep -oE '[0-9]{4}-[a-z0-9-]+' ); do
      ( cd "$BASE" && "$TODO" hold "$id" "stalled: no unattended progress — provision deps or release" >/dev/null 2>&1 ) && n=$((n + 1))
    done
    STALLED=1
    echo "orch: ⚠ STALLED — held $n undoable task(s); going quiet (release one to resume)"
    notify "stalled — needs you" "$n task(s) can't progress unattended; held for review"
  fi
  # On a usage limit, don't hammer it — poll only every LIMIT_BACKOFF (5 min) until
  # it resets; otherwise the normal fast poll. headless reads the flag set when a
  # turn's log showed a limit; tmux scrapes the live panes.
  if [ "$MODE" = headless ]; then
    if [ "$LIMIT_HIT" = 1 ]; then echo "orch: ⏳ usage limit hit — backing off ${LIMIT_BACKOFF}s before the next turn"; LIMIT_HIT=0; sleep "$LIMIT_BACKOFF"; else sleep "$POLL"; fi
  elif usage_limited; then echo "orch: ⏳ usage limit detected — backing off ${LIMIT_BACKOFF}s (ping ~every 5 min until reset)"; sleep "$LIMIT_BACKOFF"
  else sleep "$POLL"; fi
done

echo "orch: done. turns=$TURNS_DONE open=$(open_count) tasks left."
echo "orch: REVIEW WHAT LANDED →  git -C $STAGE_WT diff $BASE_BRANCH...staging   (then merge staging → $BASE_BRANCH)"
[ "$MODE" = tmux ] && echo "orch: worker sessions left alive: ns-w0 .. ns-w$((N-1))"
