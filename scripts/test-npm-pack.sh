#!/usr/bin/env bash
# devbrain — npm package test. The npm package is a thin forwarder: it ships
# bin/devbrain.js only, which prints install instructions when the Go binary is
# absent and forwards argv to a `devbrain` on PATH when present. Build the real
# tarball with `npm pack`, extract it, and prove both paths. Skips without node.
set -u

REPO="$(cd "$(dirname "$0")/.." && pwd)"
command -v npm  >/dev/null 2>&1 || { echo "skip: npm not available";  exit 0; }
command -v node >/dev/null 2>&1 || { echo "skip: node not available"; exit 0; }

pass=0; fail=0
check() { if eval "$2"; then echo "  ok   — $1"; pass=$((pass+1)); else echo "  FAIL — $1"; fail=$((fail+1)); fi; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# 1. Build the real tarball (local, no publish, no lifecycle scripts).
if ! npm pack "$REPO" --pack-destination "$TMP" >"$TMP/pack.log" 2>&1; then
  echo "  FAIL — npm pack"; sed 's/^/        | /' "$TMP/pack.log"
  echo "== 0 passed, 1 failed =="; exit 1
fi
TGZ="$(ls "$TMP"/*.tgz 2>/dev/null | head -1)"
check "npm pack produced a tarball" '[ -n "$TGZ" ] && [ -f "$TGZ" ]'

# 2. It ships the forwarder (plus npm's automatic README/LICENSE) and nothing else.
LIST="$(tar tzf "$TGZ" 2>/dev/null | sed 's#^package/##')"
check "ships bin/devbrain.js"      'printf "%s\n" "$LIST" | grep -qxF bin/devbrain.js'
check "ships no scripts or hooks"  '! printf "%s\n" "$LIST" | grep -qE "^(scripts|hooks)/"'

mkdir -p "$TMP/extracted"
tar xzf "$TGZ" -C "$TMP/extracted"          # -> $TMP/extracted/package/
JS="$TMP/extracted/package/bin/devbrain.js"
check "bin/devbrain.js executable" '[ -x "$JS" ]'

# 3. No binary on PATH: `help` prints install instructions and exits 0.
out="$(env PATH="$(dirname "$(command -v node)"):/usr/bin:/bin" node "$JS" help 2>&1)"; rc=$?
check "help exits 0"                '[ "$rc" -eq 0 ]'
check "help prints install channel" 'printf "%s" "$out" | grep -q "brew install TheWeiHu/devbrain/devbrain"'

# 4. With a stub `devbrain` on PATH, argv is forwarded verbatim.
STUB="$TMP/stubbin"; mkdir -p "$STUB"
printf '#!/bin/sh\nprintf "%%s\\n" "$@" > "%s/argv.txt"\n' "$TMP" > "$STUB/devbrain"
chmod +x "$STUB/devbrain"
env PATH="$STUB:$(dirname "$(command -v node)"):/usr/bin:/bin" node "$JS" todo next >/dev/null 2>&1
check "forwards to devbrain on PATH" '[ "$(cat "$TMP/argv.txt" 2>/dev/null)" = "$(printf "todo\nnext")" ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
