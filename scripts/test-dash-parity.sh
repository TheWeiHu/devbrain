#!/usr/bin/env bash
# devbrain — dashboard parity test. Launches the GO `devbrain queue` on the
# dashboard fixture and diffs every /api/* endpoint's normalized JSON (sorted
# keys, <PID>, <DATA>) against testdata/golden/api/*.json — the immutable spec
# captured from the retired implementation. GET / must byte-equal
# assets/dashboard.html.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ROOT="$(dirname "$HERE")"
BIN="${DEVBRAIN_BIN:-$ROOT/devbrain}"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not installed"; exit 0; }
[ -x "$BIN" ] || { echo "skip: devbrain binary not built (go build -o devbrain ./cmd/devbrain)"; exit 0; }

TMP="$(mktemp -d)"
GO_PID=""
cleanup() { [ -n "$GO_PID" ] && kill "$GO_PID" 2>/dev/null; wait 2>/dev/null; rm -rf "$TMP"; }
trap cleanup EXIT

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1"; fi; }

# fixture + the generated nightshift run wiring
DGO="$TMP/data-go"
cp -R "$ROOT/testdata/dashboard-fixture" "$DGO"
printf '{"port": 0, "repo": "%s"}\n' "$DGO/nightshift-repo" > "$DGO/projects/fix__demo/nightshift-run.json"
mkdir -p "$DGO/nightshift-repo/.nightshift"
cp "$ROOT/testdata/dashboard-fixture/nightshift-status.json" "$DGO/nightshift-repo/.nightshift/status.json"

# the same normalizer the goldens were captured with: sorted keys, <PID>, <DATA>
NORM="$TMP/norm.py"
cat > "$NORM" <<'PY'
import json, os, sys
data_dir = sys.argv[1]
def norm(o):
    if isinstance(o, dict):
        return {k: ("<PID>" if k == "pid" else norm(v)) for k, v in o.items()}
    if isinstance(o, list):
        return [norm(v) for v in o]
    if isinstance(o, str):
        return o.replace(os.path.realpath(data_dir), "<DATA>").replace(data_dir, "<DATA>")
    return o
json.dump(norm(json.load(sys.stdin)), sys.stdout, indent=1, sort_keys=True, ensure_ascii=False)
print()
PY

free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'; }
P2="$(free_port)"
"$BIN" queue --port "$P2" --no-open --data "$DGO" >/dev/null 2>&1 & GO_PID=$!

wait_up() { # $1 port  $2 data dir — up AND serving OUR data (not a stray queue)
  for _ in $(seq 1 50); do
    if curl -sf "http://127.0.0.1:$1/api/whoami" 2>/dev/null \
        | grep -qF "$(python3 -c 'import os,sys;print(os.path.realpath(sys.argv[1]))' "$2")"; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}
wait_up "$P2" "$DGO" || { echo "FAIL: devbrain queue did not come up on $P2"; exit 1; }

snap() { # $1 port  $2 data dir  $3 out file  $4 path
  curl -sf "http://127.0.0.1:$1$4" | python3 "$NORM" "$2" > "$3"
}
ENDPOINTS="
todos|/api/todos
prompts-all|/api/prompts?days=0&kind=all
prompts-typed|/api/prompts?days=0&kind=typed
prompts-bot|/api/prompts?days=0&kind=bot
gbrain|/api/gbrain?days=0
tokens|/api/tokens?days=0
pricing|/api/pricing
nightshift|/api/nightshift
preferences|/api/preferences
whoami|/api/whoami
"
for ep in $ENDPOINTS; do
  name="${ep%%|*}"; path="${ep#*|}"
  snap "$P2" "$DGO" "$TMP/$name.go.json" "$path"
  check "go matches golden: $name" 'diff -u "$ROOT/testdata/golden/api/$name.json" "$TMP/$name.go.json" >&2'
done

# GET / is the embedded dashboard, byte-identical.
curl -sf "http://127.0.0.1:$P2/" > "$TMP/dash.html"
check "GET / byte-equals assets/dashboard.html" 'cmp -s "$ROOT/assets/dashboard.html" "$TMP/dash.html"'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
