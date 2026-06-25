#!/usr/bin/env bash
# devbrain — tests for the aggregate runner's classification (scripts/test-all.sh),
# focused on the DEVBRAIN_TEST_REQUIRE guard added for task 0032: a required test that
# would otherwise SKIP (its own bail OR an explicit DEVBRAIN_TEST_SKIP) must turn the
# run RED, and a REQUIRE that matches nothing must fail loudly. Runs the real runner
# against a throwaway dir of synthetic test-*.sh fixtures (test-all.sh only scans its
# own directory, so we copy it next to the fixtures).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; RUNNER="$HERE/test-all.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

cp "$RUNNER" "$TMP/test-all.sh"
mk(){ printf '#!/usr/bin/env bash\n%s\n' "$2" > "$TMP/$1"; chmod +x "$TMP/$1"; }
mk test-fake-pass.sh   'echo ok; exit 0'
mk test-fake-skip.sh   'echo "skip: missing dep"; exit 0'      # output-based skip
mk test-fake-docker.sh 'echo "docker daemon not running"; exit 0'  # the docker bail line

# run the runner with ONLY the given env vars set — scrub the two we control so this
# stays hermetic when the outer aggregate run already has them set (it does, in CI / the
# nightshift gate). Capture output + exit code.
run(){ ( cd "$TMP" && env -u DEVBRAIN_TEST_SKIP -u DEVBRAIN_TEST_REQUIRE "$@" bash ./test-all.sh ) >"$TMP/out" 2>&1; echo $?; }

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; printf '%s\n' "$(cat "$TMP/out")" | sed 's/^/        | /'; fi; }

# ── baseline: a docker-bail test is a benign SKIP, run is green ───────────────
rc="$(run)"
check "no REQUIRE: docker bail → SKIP, exit 0" '[ "$rc" = 0 ] && grep -q "SKIP  test-fake-docker.sh" "$TMP/out"'

# ── REQUIRE upgrades an output-skip to FAIL ──────────────────────────────────
rc="$(run DEVBRAIN_TEST_REQUIRE=fake-docker)"
check "REQUIRE=fake-docker: skipped-but-required → FAIL, exit !=0" \
  '[ "$rc" != 0 ] && grep -q "FAIL  test-fake-docker.sh (DEVBRAIN_TEST_REQUIRE: must run" "$TMP/out"'

# ── a required test that genuinely passes stays green ────────────────────────
rc="$(run DEVBRAIN_TEST_REQUIRE=fake-pass)"
check "REQUIRE=fake-pass: really passes → exit 0" '[ "$rc" = 0 ] && grep -q "PASS  test-fake-pass.sh" "$TMP/out"'

# ── REQUIRE beats an explicit DEVBRAIN_TEST_SKIP for the same test ───────────
rc="$(run DEVBRAIN_TEST_SKIP=fake-pass DEVBRAIN_TEST_REQUIRE=fake-pass)"
check "REQUIRE wins over SKIP-filter → FAIL, exit !=0" \
  '[ "$rc" != 0 ] && grep -q "FAIL  test-fake-pass.sh (DEVBRAIN_TEST_REQUIRE: skip-filtered" "$TMP/out"'

# ── REQUIRE matching no test at all fails loudly (renamed/deleted guard) ─────
rc="$(run DEVBRAIN_TEST_REQUIRE=does-not-exist-xyz)"
check "REQUIRE matches nothing → FAIL, exit !=0" \
  '[ "$rc" != 0 ] && grep -q "no test matches DEVBRAIN_TEST_REQUIRE" "$TMP/out"'

# ── the regex genuinely scopes to the named test, not every skip ─────────────
rc="$(run DEVBRAIN_TEST_REQUIRE=fake-docker)"
check "REQUIRE=fake-docker leaves unrelated fake-skip a benign SKIP" \
  'grep -q "SKIP  test-fake-skip.sh" "$TMP/out"'

echo
echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
