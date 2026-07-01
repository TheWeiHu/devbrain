#!/usr/bin/env bash
# devbrain — `devbrain review` / review.sh tests. Uses fake claude/gh binaries;
# no network and no real Claude calls.
set -u

REPO="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$REPO/scripts/review.sh"
command -v git >/dev/null 2>&1 || { echo "skip: git not available"; exit 0; }
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not available"; exit 0; }

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1"; fi; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
FAKE="$TMP/fakebin"; mkdir -p "$FAKE"

cat > "$FAKE/claude" <<'SH'
#!/usr/bin/env bash
cat > "$CAPTURE_PROMPT"
printf '## Findings\n- No blocking findings.\n\n## Open Questions\n- None.\n\n## Notes\n- fake review.\n'
SH
chmod +x "$FAKE/claude"

# 1. Local diff review streams the generated bundle into claude and writes --out.
WORK="$TMP/repo"; mkdir -p "$WORK"; git -C "$WORK" init -q
git -C "$WORK" config user.email test@example.test
git -C "$WORK" config user.name Test
printf 'one\n' > "$WORK/app.txt"
git -C "$WORK" add app.txt && git -C "$WORK" commit -qm base
printf 'two\n' > "$WORK/app.txt"
git -C "$WORK" add app.txt && git -C "$WORK" commit -qm change
OUT="$TMP/review.md"; (cd "$WORK" && CAPTURE_PROMPT="$TMP/prompt-local.md" \
  PATH="$FAKE:/usr/bin:/bin" bash "$SCRIPT" --base HEAD~1 --out "$OUT" --max-budget-usd 1 >/dev/null 2>&1)
check "local review writes output file" '[ -s "$OUT" ] && grep -q "fake review" "$OUT"'
check "local review prompt includes strict review stance" 'grep -q "strict code-review stance" "$TMP/prompt-local.md"'
check "local review prompt includes patch" 'grep -q "^-one" "$TMP/prompt-local.md" && grep -q "^+two" "$TMP/prompt-local.md"'

printf 'new\n' > "$WORK/new.txt"
OUT_UNTRACKED="$TMP/review-untracked.md"; (cd "$WORK" && CAPTURE_PROMPT="$TMP/prompt-untracked.md" \
  PATH="$FAKE:/usr/bin:/bin" bash "$SCRIPT" --base HEAD --out "$OUT_UNTRACKED" --max-budget-usd 1 >/dev/null 2>&1)
check "local review omits untracked files by default" 'grep -q "Untracked files omitted" "$TMP/prompt-untracked.md" && ! grep -q "^+new" "$TMP/prompt-untracked.md"'
OUT_UNTRACKED2="$TMP/review-untracked-include.md"; (cd "$WORK" && CAPTURE_PROMPT="$TMP/prompt-untracked-include.md" \
  PATH="$FAKE:/usr/bin:/bin" bash "$SCRIPT" --base HEAD --include-untracked --out "$OUT_UNTRACKED2" --max-budget-usd 1 >/dev/null 2>&1)
check "local review includes untracked files only when opted in" 'grep -q "Untracked file: new.txt" "$TMP/prompt-untracked-include.md" && grep -q "^+new" "$TMP/prompt-untracked-include.md"'

# 2. PR mode can post one GitHub review comment when --post is explicit.
cat > "$FAKE/gh" <<'SH'
#!/usr/bin/env bash
set -eu
case "$1 $2" in
  "pr view")
    [ "${3:-}" = "missing" ] && exit 1
    printf '{"number":123,"title":"Review me","url":"https://example.test/pr/123","headRefName":"feature","baseRefName":"main","body":"body"}\n'
    ;;
  "pr diff")
    if [ "${4:-}" = "--name-only" ]; then
      printf 'src/app.py\n'
    else
      printf 'diff --git a/src/app.py b/src/app.py\n@@ -1 +1 @@\n-old\n+new\n'
    fi
    ;;
  "pr review")
    out="$GH_REVIEW_CALL"
    printf '%s\n' "$*" > "$out"
    while [ $# -gt 0 ]; do
      if [ "$1" = "-F" ]; then shift; cat "$1" > "$GH_REVIEW_BODY"; exit 0; fi
      shift
    done
    exit 2
    ;;
  *) echo "unexpected gh call: $*" >&2; exit 2 ;;
esac
SH
chmod +x "$FAKE/gh"

OUT2="$TMP/pr-review.md"; CAPTURE_PROMPT="$TMP/prompt-pr.md" GH_REVIEW_CALL="$TMP/gh-call" GH_REVIEW_BODY="$TMP/gh-body" \
  PATH="$FAKE:/usr/bin:/bin" bash "$SCRIPT" 123 --post --out "$OUT2" --max-budget-usd 1 >/dev/null 2>&1
check "post mode calls gh pr review --comment" 'grep -q "pr review 123 --comment -F" "$TMP/gh-call"'
check "post mode sends attributed review body file" 'grep -q "CLAUDE (Sonnet):" "$TMP/gh-body" && grep -q "fake review" "$TMP/gh-body"'
check "PR prompt includes PR metadata" 'grep -q "GitHub PR: #123" "$TMP/prompt-pr.md"'
check "PR prompt includes real diff stat" 'grep -q "src/app.py" "$TMP/prompt-pr.md"'

BAD_ERR="$TMP/bad-target.err"; PATH="$FAKE:/usr/bin:/bin" bash "$SCRIPT" missing --out "$TMP/bad.md" >"$TMP/bad-target.out" 2>"$BAD_ERR"; bad_rc=$?
check "explicit bad PR target fails instead of local fallback" '[ "$bad_rc" -ne 0 ] && grep -q "could not resolve PR target: missing" "$BAD_ERR"'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
