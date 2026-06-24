#!/usr/bin/env bash
# Smoke test for capture.sh's secret redaction. Feeds prompts with known secret
# shapes through the hook and asserts the secret never lands in the log file.
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOOK="$ROOT/hooks/capture.sh"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not installed"; exit 0; }
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

pass=0; fail=0
DATA="$TMP/data"

run() {  # run <prompt> — feed one prompt through the hook, echo the written log
  local prompt="$1"
  local payload
  payload="$(python3 -c 'import json,sys;print(json.dumps({"prompt":sys.argv[1],"cwd":sys.argv[2],"session_id":"testsess"}))' "$prompt" "$TMP")"
  printf '%s' "$payload" | DEVBRAIN_DATA="$DATA" bash "$HOOK"
  cat "$DATA"/projects/*/log/*/*.testsess.md 2>/dev/null
}

# secret-shape → must NOT appear ; redaction marker MUST appear
secrets=(
  "my key is sk-abcdefghijklmnopqrstuvwxyz0123456789"
  "token ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
  "github_pat_11ABCDEFG0123456789_abcdefghijklmnop"
  "aws AKIAIOSFODNN7EXAMPLE here"
  "slack xoxb-1234567890-abcdefghij"
  "Authorization: Bearer abcdef1234567890ghijkl"
)
for s in "${secrets[@]}"; do
  rm -rf "$DATA"
  out="$(run "$s")"
  # extract the raw secret token (last whitespace-free chunk that triggered it)
  if printf '%s' "$out" | grep -Eq 'sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_|AKIA[0-9A-Z]{16}|xox[baprs]-[0-9]|Bearer [A-Za-z0-9._-]{16,}'; then
    echo "FAIL: secret leaked into log for: $s"; fail=$((fail+1))
  elif printf '%s' "$out" | grep -q '\[REDACTED\]'; then
    echo "ok:   redacted — $s"; pass=$((pass+1))
  else
    echo "FAIL: no redaction marker for: $s"; fail=$((fail+1))
  fi
done

# ordinary prose must pass through untouched
rm -rf "$DATA"
out="$(run "please refactor the parser and add tests")"
if printf '%s' "$out" | grep -q 'refactor the parser and add tests' && ! printf '%s' "$out" | grep -q 'REDACTED'; then
  echo "ok:   prose untouched"; pass=$((pass+1))
else
  echo "FAIL: prose was altered"; fail=$((fail+1))
fi

echo "── $pass passed, $fail failed ──"
[ "$fail" -eq 0 ]
