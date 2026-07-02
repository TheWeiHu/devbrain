#!/usr/bin/env bash
# Clean-environment brew e2e, run ON the Linux box: reset to a pristine state,
# brew-install devbrain from the shipped artifact, wire a machine with stubbed
# agents, drive the capture/todo/queue surface end-to-end, uninstall, and
# assert the machine is clean. Exits non-zero on any failed check.
#
# Expects (scp'd by `make e2e-brew` into ~/e2e/): devbrain.rb (formula whose
# url points at the local tarball), devbrain.tar.gz, this script, and
# EXPECTED_VERSION in the environment.
set -uo pipefail
eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_INSTALL_CLEANUP=1

pass=0; fail=0
check() { if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

cycle() {  # one full clean-slate → install → exercise → uninstall pass
  local round="$1"
  echo "== round $round: clean slate =="
  brew uninstall --force devbrain >/dev/null 2>&1
  brew untap local/e2e >/dev/null 2>&1
  rm -rf ~/.claude ~/.codex ~/.agents ~/devbrain-data ~/.config/devbrain ~/.local/bin/devbrain*
  crontab -r 2>/dev/null
  systemctl --user disable --now devbrain-flush.timer >/dev/null 2>&1
  sed -i '/added by devbrain installer/,+1d' ~/.bashrc ~/.profile 2>/dev/null

  echo "== round $round: brew install =="
  # modern brew refuses path formulas — install through a throwaway local tap
  TAPDIR="$(brew --repository)/Library/Taps/local/homebrew-e2e"
  rm -rf "$TAPDIR" && mkdir -p "$TAPDIR/Formula"
  cp ~/e2e/devbrain.rb "$TAPDIR/Formula/devbrain.rb"
  git -C "$TAPDIR" init -q 2>/dev/null; git -C "$TAPDIR" add -A 2>/dev/null
  git -C "$TAPDIR" -c user.name=e2e -c user.email=e2e@localhost commit -qm formula 2>/dev/null
  brew install local/e2e/devbrain >/dev/null 2>&1 || brew install local/e2e/devbrain
  hash -r   # drop bash's stale PATH hash from the previous round's binary
  check "binary on PATH"        'command -v devbrain'
  check "version matches"       '[ "$(devbrain version)" = "$EXPECTED_VERSION" ]'
  # file -L: the brew bin path is a symlink into the Cellar; Linux file(1)
  # doesn't follow it by default. grep drains the stream (no -q): a -q
  # early-exit SIGPIPEs the producer under pipefail and fails a real match.
  check "static linux binary"   'file -L "$(command -v devbrain)" | grep "statically linked\|static-pie" >/dev/null'

  echo "== round $round: devbrain install =="
  mkdir -p ~/stubbin
  printf '#!/bin/sh\nexit 0\n' > ~/stubbin/claude && chmod +x ~/stubbin/claude
  PATH="$HOME/stubbin:$PATH" DEVBRAIN_NO_IMPORT=1 DEVBRAIN_DATA="$HOME/devbrain-data" devbrain install --yes >/dev/null 2>&1
  check "settings.json registers capture" 'grep -q "hook capture" ~/.claude/settings.json'
  check "settings.json registers response" 'grep -q "hook response" ~/.claude/settings.json'
  check "config records data dir" 'grep -q "devbrain-data" ~/.config/devbrain/config.json'
  check "data repo initialized"   'git -C ~/devbrain-data rev-parse HEAD >/dev/null 2>&1'
  check "skills extracted"        '[ -f ~/.claude/skills/continue/SKILL.md ]'
  check "linux flusher (no launchd)" '{ systemctl --user is-enabled devbrain-flush.timer >/dev/null 2>&1 || crontab -l 2>/dev/null | grep -q "devbrain flush"; } && [ ! -e ~/Library ]'
  check "CLAUDE.md block written" 'grep -q "devbrain:start" ~/.claude/CLAUDE.md'

  echo "== round $round: capture pipeline =="
  hookcmd="$(python3 -c 'import json;print([h["command"] for e in json.load(open("'"$HOME"'/.claude/settings.json"))["hooks"]["UserPromptSubmit"] for h in e["hooks"] if "devbrain" in h["command"]][0])' 2>/dev/null)"
  check "capture hook command found" '[ -n "$hookcmd" ]'
  printf '%s' '{"prompt":"e2e prompt with key sk-abcdefghijklmnopqrstuvwx end","cwd":"'"$HOME"'","session_id":"e2e1"}' | eval "$hookcmd"
  log="$(find ~/devbrain-data/projects -name '*.e2e1.md' 2>/dev/null | head -1)"
  check "prompt captured"  '[ -n "$log" ] && grep -q "e2e prompt" "$log"'
  check "secret redacted"  'grep -q "REDACTED" "$log" && ! grep -q "sk-abcdefghijklmnopqrstuvwx" "$log"'
  # a Stop event with a fixture transcript -> recap line + token sidecar
  mkdir -p /tmp/e2etr
  printf '%s\n%s\n' \
    '{"type":"user","timestamp":"2026-07-02T10:00:00Z","cwd":"'"$HOME"'","message":{"content":"e2e prompt with key end"}}' \
    '{"type":"assistant","timestamp":"2026-07-02T10:00:05Z","message":{"id":"m1","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":5},"content":[{"type":"text","text":"All checks passed on the box."}]}}' \
    > /tmp/e2etr/t.jsonl
  stopcmd="$(python3 -c 'import json;print([h["command"] for e in json.load(open("'"$HOME"'/.claude/settings.json"))["hooks"]["Stop"] for h in e["hooks"] if "devbrain" in h["command"]][0])' 2>/dev/null)"
  printf '%s' '{"transcript_path":"/tmp/e2etr/t.jsonl","cwd":"'"$HOME"'","session_id":"e2e1"}' | eval "$stopcmd"
  check "recap appended"   'grep -q "↳ .* — All checks passed on the box." "$log"'
  check "token sidecar"    'grep -q "claude-opus-4-8" ~/devbrain-data/projects/*/tokens.jsonl'

  echo "== round $round: todo + queue =="
  cd "$HOME"
  id="$(DEVBRAIN_PROJECT=e2e__proj devbrain todo add "box task" -p 9)"
  check "todo add"   '[ -n "$id" ]'
  check "todo next"  '[ "$(DEVBRAIN_PROJECT=e2e__proj devbrain todo next)" = "$id" ]'
  check "todo claim" 'DEVBRAIN_PROJECT=e2e__proj devbrain todo claim "$id" >/dev/null'
  check "todo done"  'DEVBRAIN_PROJECT=e2e__proj devbrain todo done "$id" >/dev/null && DEVBRAIN_PROJECT=e2e__proj devbrain todo show "$id" | grep -q "^done_at: ....-..-..T..:..:..Z"'
  devbrain queue --no-open --port 8787 >/dev/null 2>&1 &
  QP=$!
  for i in $(seq 1 30); do curl -sf http://127.0.0.1:8787/api/whoami >/dev/null 2>&1 && break; sleep 0.2; done
  check "queue whoami"     'curl -sf http://127.0.0.1:8787/api/whoami | grep -q devbrain-queue'
  check "queue lists task" 'curl -sf http://127.0.0.1:8787/api/todos | grep -q "box task"'
  # grep WITHOUT -q (drain the stream): under pipefail a -q early-exit
  # SIGPIPEs curl and turns a real 200+match into pipeline failure.
  check "dashboard served" 'curl -sf http://127.0.0.1:8787/ | grep -i "<html" >/dev/null'
  kill "$QP" 2>/dev/null

  echo "== round $round: uninstall =="
  devbrain uninstall >/dev/null 2>&1
  check "hooks deregistered"  '! grep -q "devbrain" ~/.claude/settings.json 2>/dev/null'
  check "skills removed"      '[ ! -e ~/.claude/skills/continue ]'
  check "data repo intact"    '[ -n "$log" ] && [ -f "$log" ]'
  brew uninstall --force devbrain >/dev/null 2>&1
  hash -r   # bash caches the resolved path; without this command -v reports the deleted binary
  check "binary gone"         '! command -v devbrain'
}

cycle 1
cycle 2   # the protocol itself must be repeatable from its own aftermath

# leave the shared box clean: no wiring, no data repo, no staging artifacts
rm -rf ~/.claude ~/.codex ~/.agents ~/devbrain-data ~/.config/devbrain ~/stubbin /tmp/e2etr
brew untap local/e2e >/dev/null 2>&1

echo
echo "== e2e-brew: $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
