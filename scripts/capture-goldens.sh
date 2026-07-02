#!/usr/bin/env bash
# Regenerate testdata/golden/ from the LEGACY (bash/python) implementation.
# The goldens are the frozen behavioral contract for the Go rewrite: Go output
# must match byte-for-byte (timestamps/pids/hosts/abs-paths normalized here).
# Run from the repo root. Only re-run while the legacy implementation exists.
set -euo pipefail
cd "$(dirname "$0")/.."
LIB="hooks/devbrain_lib.py"
CORPUS="testdata/corpus"
GOLD="testdata/golden"
rm -rf "$GOLD"
mkdir -p "$GOLD"

echo "== redact =="
python3 "$LIB" redact < "$CORPUS/redact.txt" > "$GOLD/redact.golden"

echo "== prompt-filter =="
python3 - "$LIB" <<'PY' > "$GOLD/prompt-filter.jsonl"
import json, subprocess, sys
lib = sys.argv[1]
for line in open("testdata/corpus/prompt-filter-cases.jsonl"):
    c = json.loads(line)
    r = subprocess.run(["python3", lib, "prompt-filter"], input=c["text"],
                       capture_output=True, text=True)
    print(json.dumps({"name": c["name"], "out": r.stdout}, ensure_ascii=False))
PY

echo "== recap + sample =="
python3 - <<'PY' > "$GOLD/recap.json"
import json, sys
sys.path.insert(0, "hooks")
import devbrain_lib as lib
out = []
for c in json.load(open("testdata/corpus/recap-cases.json")):
    out.append({"name": c["name"], "recap": lib.recap(c["texts"]),
                "sample": lib.sample(c["texts"])})
json.dump(out, sys.stdout, indent=1, ensure_ascii=False); print()
PY

echo "== read-event =="
python3 - <<'PY' > "$GOLD/read-event.jsonl"
import json, sys
sys.path.insert(0, "hooks")
import devbrain_lib as lib
for line in open("testdata/corpus/read-event-cases.jsonl"):
    c = json.loads(line)
    v = lib.read_event(c["payload"], c["field"], c["harness"])
    print(json.dumps({"name": c["name"], "out": v}, ensure_ascii=False))
PY

echo "== gbrain-record =="
python3 - <<'PY' > "$GOLD/gbrain-record.jsonl"
import json, sys
sys.path.insert(0, "hooks")
import devbrain_lib as lib
for line in open("testdata/corpus/gbrain-record-cases.jsonl"):
    c = json.loads(line)
    v = lib.gbrain_record(c["cmd"], c["out"], c["project"], c["ts"])
    print(json.dumps({"name": c["name"], "out": v}, ensure_ascii=False))
PY

echo "== remote-keys =="
python3 - <<'PY' > "$GOLD/remote-keys.txt"
import sys
sys.path.insert(0, "hooks")
import devbrain_lib as lib
for url in open("testdata/corpus/remote-keys.txt").read().splitlines():
    print(f"{url}\t{lib.remote_to_key(url)}")
PY

echo "== settings register/unregister =="
SDIR="$GOLD/settings"; mkdir -p "$SDIR"
BIN="/opt/devbrain/bin/devbrain"   # fixed fake install path baked into scenarios
TMPS="$(mktemp -d)"
python3 - "$LIB" "$TMPS" "$SDIR" "$BIN" <<'PY'
import json, os, shutil, subprocess, sys
lib, tmp, out, BIN = sys.argv[1:5]
def prep(src, name):
    f = os.path.join(tmp, name + ".json")
    if src:
        text = open(os.path.join("testdata/corpus/settings", src)).read()
        open(f, "w").write(text.replace("REG_CMD_PLACEHOLDER", BIN + " hook"))
    elif os.path.exists(f):
        os.unlink(f)
    return f
def reg(f, event, matcher, cmd):
    subprocess.run(["python3", lib, "register-hook", f, event, matcher, cmd], check=True)
def unreg(f, *cmds):
    subprocess.run(["python3", lib, "unregister-hook", f] + list(cmds), check=True)
def save(f, name):
    shutil.copy(f, os.path.join(out, name + ".after.json"))

f = prep("01-empty-object.json", "s01")
reg(f, "UserPromptSubmit", "", BIN + " hook capture")
reg(f, "SessionStart", "startup|resume", BIN + " hook session-start")
save(f, "01-register-into-empty")

f = prep(None, "s02-absent")
reg(f, "UserPromptSubmit", "", BIN + " hook capture")
save(f, "02-register-absent-file")

f = prep("02-foreign-hooks.json", "s03")
reg(f, "UserPromptSubmit", "", BIN + " hook capture")
reg(f, "PostToolUse", "Bash", BIN + " hook gbrain")
reg(f, "Stop", "", BIN + " hook response")
save(f, "03-register-among-foreign")
unreg(f, BIN + " hook capture", BIN + " hook gbrain", BIN + " hook response")
save(f, "04-unregister-leaves-foreign")

f = prep("03-already-registered.json", "s05")
reg(f, "UserPromptSubmit", "", BIN + " hook capture")
save(f, "05-idempotent-reregister")

f = prep("04-grouped-sibling.json", "s06")
unreg(f, BIN + " hook response")
save(f, "06-unregister-keeps-sibling")
PY

echo "== todo verb sequence -> tree + cli output =="
TDATA="$(mktemp -d)"
TOUT="$GOLD/todo-cli-output.txt"; : > "$TOUT"
PRSTUB="$TMPS/prstub"; printf '#!/bin/sh\ncase "$1" in *81*) echo MERGED;; *) echo OPEN;; esac\n' > "$PRSTUB"; chmod +x "$PRSTUB"
tstep() { # label, then command; records normalized stdout+exit code
  local label="$1"; shift
  local rc=0 out
  out="$( DEVBRAIN_DATA="$TDATA" DEVBRAIN_PROJECT=fix__demo DEVBRAIN_PR_STATE_CMD="$PRSTUB" \
          bash scripts/legacy/todo.sh "$@" 2>&1 )" || rc=$?
  printf -- '--- %s (rc=%s)\n%s\n' "$label" "$rc" "$out" \
    | sed -E 's/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}Z/<TS>/g' >> "$TOUT"
}
tstep add1        add "Add retry queue" -p 80 -b "Failed jobs should retry with backoff."
tstep add2        add "Fix Mobile FOOTER overlap!!" -p 5
tstep add3        add "Third task" -b "body only"
tstep list-open   list
tstep next        next
tstep claim2      claim 0002-fix-mobile-footer-overlap
tstep claim2-again claim 0002-fix-mobile-footer-overlap
tstep review1     review 0001-add-retry-queue https://github.com/fix/demo/pull/81
tstep hold3       hold 0003-third-task waiting on design
tstep note3       note 0003-third-task gate failed twice
tstep list-all    list all
tstep prio3       prio 0003-third-task 99
tstep edit3       edit 0003-third-task -t "Third task (renamed)" -b "new body line"
tstep approve3    approve 0003-third-task
tstep done2       done 0002-fix-mobile-footer-overlap
tstep release2-done release 0002-fix-mobile-footer-overlap
tstep selfheal    self-heal open taken review
tstep reopen2     reopen 0002-fix-mobile-footer-overlap verified absent
tstep list-final  list all
tstep show1       show 0001-add-retry-queue
printf '%s\n' "--- context3 (rc=0)" >> "$TOUT"
printf 'Synthesized context from the brain.\nSecond line.\n' \
  | DEVBRAIN_DATA="$TDATA" DEVBRAIN_PROJECT=fix__demo bash scripts/legacy/todo.sh context 0003-third-task >> "$TOUT"
mkdir -p "$GOLD/todo-tree"
for f in "$TDATA/projects/fix__demo/todo/"*.md; do
  sed -E "s/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}Z/<TS>/g; s/^claimed_by: .+/claimed_by: <WHO>/" \
    "$f" > "$GOLD/todo-tree/$(basename "$f")"
done

echo "== queue.py API endpoints on the dashboard fixture =="
QDATA="$(mktemp -d)/data"
cp -R testdata/dashboard-fixture "$QDATA"
# nightshift-run.json + the repo's .nightshift/status.json are generated (absolute
# repo path / gitignored dir), never committed — status content lives in the fixture
printf '{"port": 8907, "repo": "%s"}\n' "$QDATA/nightshift-repo" \
  > "$QDATA/projects/fix__demo/nightshift-run.json"
mkdir -p "$QDATA/nightshift-repo/.nightshift"
cp testdata/dashboard-fixture/nightshift-status.json "$QDATA/nightshift-repo/.nightshift/status.json"
PORT=8907
python3 scripts/legacy/queue.py --port "$PORT" --no-open --data "$QDATA" &
QPID=$!
trap 'kill "$QPID" 2>/dev/null || true' EXIT
for i in $(seq 1 50); do
  curl -sf "http://127.0.0.1:$PORT/api/whoami" >/dev/null 2>&1 && break; sleep 0.1
done
# the server on $PORT must be OURS (a stray queue would golden the wrong data)
curl -sf "http://127.0.0.1:$PORT/api/whoami" | grep -qF "$(python3 -c 'import os,sys;print(os.path.realpath(sys.argv[1]))' "$QDATA")" \
  || { echo "capture-goldens: port $PORT is serving foreign data — free it and re-run" >&2; exit 1; }
mkdir -p "$GOLD/api"
NORM="$TMPS/norm.py"
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
snap() { # $1 name  $2 path — normalized, key-sorted JSON
  curl -sf "http://127.0.0.1:$PORT$2" | python3 "$NORM" "$QDATA" > "$GOLD/api/$1.json"
}
snap whoami        "/api/whoami"
snap todos         "/api/todos"
snap prompts-all   "/api/prompts?days=0&kind=all"
snap prompts-typed "/api/prompts?days=0&kind=typed"
snap prompts-bot   "/api/prompts?days=0&kind=bot"
snap gbrain        "/api/gbrain?days=0"
snap tokens        "/api/tokens?days=0"
snap pricing       "/api/pricing"
snap nightshift    "/api/nightshift"
snap preferences   "/api/preferences"
kill "$QPID" 2>/dev/null || true; wait "$QPID" 2>/dev/null || true
trap - EXIT

shasum -a 256 assets/dashboard.html | awk '{print $1}' > "$GOLD/dashboard.sha256"

rm -rf "$TMPS" "$TDATA" "$(dirname "$QDATA")"
echo "goldens regenerated under $GOLD"
