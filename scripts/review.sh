#!/usr/bin/env bash
# devbrain — run a Claude Code headless review over a PR or local diff.
#
# Default is safe/read-only: write a markdown review artifact under .context/ and
# print it. Pass --post to publish that artifact as a GitHub PR review comment.
set -euo pipefail

usage() {
  cat <<'USAGE'
devbrain review [PR|URL|branch] [options]

Run `claude -p` as a code reviewer over a GitHub PR or the local diff.

Options:
  --post                  post the review body to the PR with `gh pr review --comment`
  --out FILE              write review markdown to FILE (default: .context/claude-review-*.md)
  --base REF              local-diff base when no PR is found (default: origin/main)
  --model MODEL           Claude model alias/name (default: sonnet)
  --effort LEVEL          Claude effort (default: medium)
  --include-untracked     include untracked files in local-diff reviews (off by default)
  --max-budget-usd USD    budget guard for the Claude call (default: 3)
  --max-diff-bytes N      cap patch bytes included in prompt (default: 240000)
  -h, --help              show this help

Environment:
  DEVBRAIN_REVIEW_CLAUDE  claude binary to run (default: claude; tests can fake it)
USAGE
}

die() { echo "devbrain review: $*" >&2; exit 1; }
json_get() {
  python3 -c '
import json, sys
path = sys.argv[1].split(".")
obj = json.load(sys.stdin)
for p in path:
    obj = obj.get(p, {}) if isinstance(obj, dict) else {}
print("" if obj is None or obj == {} else obj)
' "$1"
}
slugify() { printf '%s' "$1" | tr -cs 'A-Za-z0-9._-' '-' | sed 's/^-//; s/-$//'; }
model_label() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    sonnet) echo "Sonnet" ;;
    opus) echo "Opus" ;;
    fable) echo "Fable" ;;
    *) echo "$1" ;;
  esac
}

TARGET=""
POST=0
OUT=""
BASE="${DEVBRAIN_REVIEW_BASE:-origin/main}"
MODEL="${DEVBRAIN_REVIEW_MODEL:-sonnet}"
EFFORT="${DEVBRAIN_REVIEW_EFFORT:-medium}"
BUDGET="${DEVBRAIN_REVIEW_MAX_BUDGET_USD:-3}"
MAX_DIFF_BYTES="${DEVBRAIN_REVIEW_MAX_DIFF_BYTES:-240000}"
INCLUDE_UNTRACKED=0

while [ $# -gt 0 ]; do
  case "$1" in
    --post) POST=1; shift ;;
    --out) OUT="${2:-}"; [ -n "$OUT" ] || die "--out needs a file"; shift 2 ;;
    --base) BASE="${2:-}"; [ -n "$BASE" ] || die "--base needs a ref"; shift 2 ;;
    --model) MODEL="${2:-}"; [ -n "$MODEL" ] || die "--model needs a value"; shift 2 ;;
    --effort) EFFORT="${2:-}"; [ -n "$EFFORT" ] || die "--effort needs a value"; shift 2 ;;
    --include-untracked) INCLUDE_UNTRACKED=1; shift ;;
    --max-budget-usd) BUDGET="${2:-}"; [ -n "$BUDGET" ] || die "--max-budget-usd needs a value"; shift 2 ;;
    --max-diff-bytes) MAX_DIFF_BYTES="${2:-}"; [ -n "$MAX_DIFF_BYTES" ] || die "--max-diff-bytes needs a value"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    --*) die "unknown option: $1" ;;
    *) [ -z "$TARGET" ] || die "only one PR/branch target is supported"; TARGET="$1"; shift ;;
  esac
done

command -v git >/dev/null 2>&1 || die "git required"
command -v python3 >/dev/null 2>&1 || die "python3 required"
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || die "run inside a git repo"
cd "$ROOT"

CLAUDE_BIN="${DEVBRAIN_REVIEW_CLAUDE:-claude}"
command -v "$CLAUDE_BIN" >/dev/null 2>&1 || die "$CLAUDE_BIN not found"

MODE="local"
PR_REF=""
PR_JSON=""
PR_NUMBER=""
PR_TITLE=""
PR_URL=""
PR_BASE=""
PR_HEAD=""

GH_TRIED=0
if command -v gh >/dev/null 2>&1; then
  GH_TRIED=1
  if [ -n "$TARGET" ]; then
    PR_JSON="$(gh pr view "$TARGET" --json number,title,url,headRefName,baseRefName,body 2>/dev/null || true)"
  else
    PR_JSON="$(gh pr view --json number,title,url,headRefName,baseRefName,body 2>/dev/null || true)"
  fi
fi

if [ -n "$PR_JSON" ]; then
  MODE="pr"
  PR_REF="${TARGET:-}"
  [ -n "$PR_REF" ] || PR_REF="$(printf '%s' "$PR_JSON" | json_get number)"
  PR_NUMBER="$(printf '%s' "$PR_JSON" | json_get number)"
  PR_TITLE="$(printf '%s' "$PR_JSON" | json_get title)"
  PR_URL="$(printf '%s' "$PR_JSON" | json_get url)"
  PR_BASE="$(printf '%s' "$PR_JSON" | json_get baseRefName)"
  PR_HEAD="$(printf '%s' "$PR_JSON" | json_get headRefName)"
elif [ -n "$TARGET" ]; then
  die "could not resolve PR target: $TARGET"
elif [ "$POST" = 1 ]; then
  die "--post needs a GitHub PR (pass a PR number/URL or run on a PR branch)"
elif [ "$GH_TRIED" = 1 ]; then
  echo "devbrain review: no current GitHub PR resolved; reviewing local diff instead." >&2
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/devbrain-review.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
PATCH="$TMP/patch.diff"
STAT="$TMP/stat.txt"
BUNDLE="$TMP/review-prompt.md"

if [ "$MODE" = pr ]; then
  gh pr diff "$PR_REF" --patch > "$PATCH"
  gh pr diff "$PR_REF" --name-only > "$TMP/files.txt"
  {
    echo "PR #$PR_NUMBER: $PR_TITLE"
    echo "$PR_URL"
    echo "$PR_BASE -> $PR_HEAD"
    echo
    git apply --stat < "$PATCH" 2>/dev/null || sed 's/$/ | changed/' "$TMP/files.txt"
  } > "$STAT"
  SUBJECT="pr-${PR_NUMBER:-$(slugify "$PR_REF")}"
else
  git rev-parse --verify "$BASE" >/dev/null 2>&1 || die "base ref not found: $BASE"
  {
    git diff --stat "$BASE"...HEAD
    git diff --stat HEAD
    if [ "$INCLUDE_UNTRACKED" = 1 ]; then
      git ls-files --others --exclude-standard | sed 's/$/ | untracked/'
    elif [ -n "$(git ls-files --others --exclude-standard)" ]; then
      echo "untracked files omitted (pass --include-untracked to review them)"
    fi
  } > "$STAT"
  {
    git diff --name-only "$BASE"...HEAD
    git diff --name-only HEAD
    if [ "$INCLUDE_UNTRACKED" = 1 ]; then
      git ls-files --others --exclude-standard
    fi
  } | awk 'NF && !seen[$0]++' > "$TMP/files.txt"
  {
    git diff --patch "$BASE"...HEAD
    if ! git diff --quiet HEAD --; then
      echo
      echo "# Uncommitted tracked changes"
      git diff --patch HEAD
    fi
    if [ "$INCLUDE_UNTRACKED" = 1 ]; then
      git ls-files --others --exclude-standard | while IFS= read -r f; do
        [ -f "$f" ] || continue
        echo
        echo "# Untracked file: $f"
        git diff --no-index -- /dev/null "$f" 2>/dev/null || true
      done
    elif [ -n "$(git ls-files --others --exclude-standard)" ]; then
      echo
      echo "# Untracked files omitted"
      echo "Untracked files were not sent to Claude because they may contain secrets."
      echo "Re-run with --include-untracked if you intentionally want to review them."
    fi
  } > "$PATCH"
  SUBJECT="local-$(slugify "$BASE")"
fi

PATCH_BYTES="$(wc -c < "$PATCH" | tr -d ' ')"
TRUNCATED=0
PATCH_FOR_PROMPT="$PATCH"
case "$MAX_DIFF_BYTES" in *[!0-9]*|"") die "--max-diff-bytes must be an integer" ;; esac
if [ "$PATCH_BYTES" -gt "$MAX_DIFF_BYTES" ]; then
  TRUNCATED=1
  PATCH_FOR_PROMPT="$TMP/patch.truncated.diff"
  head -c "$MAX_DIFF_BYTES" "$PATCH" > "$PATCH_FOR_PROMPT"
fi

{
  cat <<'PROMPT'
You are reviewing a pull request. Take a strict code-review stance.
Use only the review bundle below. You cannot run tools in this mode; do not say you
will inspect files, do not emit tool-call transcripts, and do not ask to run commands.
If the bundle is insufficient, say what is missing in Open Questions.

Prioritize concrete bugs, behavioral regressions, security/privacy issues, data-loss
risks, broken installation/runtime behavior, and missing tests for changed behavior.
Do not praise. Do not summarize first. Do not suggest broad refactors unrelated to
the diff. If there are no blocking findings, say that clearly and list residual risk.

Output markdown with exactly these sections:

## Findings
- Severity: file:line — concise issue and why it matters.

## Open Questions
- Only questions that block review confidence.

## Notes
- Brief test gaps or residual risk.

Use repository-relative paths. Cite the closest line visible in the patch when exact
line numbers are available. If the diff is truncated, say so in Notes.
PROMPT
  echo
  echo "## Review Target"
  if [ "$MODE" = pr ]; then
    printf 'GitHub PR: #%s\nTitle: %s\nURL: %s\nBase: %s\nHead: %s\n' "$PR_NUMBER" "$PR_TITLE" "$PR_URL" "$PR_BASE" "$PR_HEAD"
  else
    printf 'Local diff: %s...HEAD\n' "$BASE"
  fi
  echo
  echo "## Changed Files"
  sed 's/^/- /' "$TMP/files.txt"
  echo
  echo "## Diff Stat"
  cat "$STAT"
  echo
  echo "## Patch"
  if [ "$TRUNCATED" = 1 ]; then
    echo "NOTE: patch truncated from $PATCH_BYTES bytes to $MAX_DIFF_BYTES bytes."
  fi
  cat "$PATCH_FOR_PROMPT"
} > "$BUNDLE"

if [ -z "$OUT" ]; then
  mkdir -p .context
  OUT=".context/claude-review-${SUBJECT}-$(date +%Y%m%d-%H%M%S).md"
fi

"$CLAUDE_BIN" -p \
  --model "$MODEL" \
  --effort "$EFFORT" \
  --max-budget-usd "$BUDGET" \
  --output-format text \
  --safe-mode \
  --tools "" \
  --no-session-persistence \
  < "$BUNDLE" > "$OUT"

cat "$OUT"
echo
echo "review saved: $OUT" >&2

if [ "$POST" = 1 ]; then
  POST_BODY="$TMP/post-body.md"
  {
    printf 'CLAUDE (%s):\n\n' "$(model_label "$MODEL")"
    cat "$OUT"
  } > "$POST_BODY"
  gh pr review "$PR_REF" --comment -F "$POST_BODY"
  echo "posted GitHub PR review comment: ${PR_URL:-$PR_REF}" >&2
fi
