#!/usr/bin/env bash
# devbrain — brain query router (makes gbrain optional).
#
# Pages live on disk as projects/<project>/brain/*.md; gbrain is just a per-machine
# search index built FROM them. This router lets the skills/nudge read the brain
# whether or not gbrain is installed:
#   • gbrain present  -> transparent passthrough (`exec gbrain "$@"`), so semantic
#     `query`, `--fuzzy` get, put/embed/tag/list — everything — behaves exactly as before.
#   • gbrain absent   -> built-in offline fallback for the READ verbs the skills use
#     (`search`, `query`/`ask`, `get`) implemented with grep over the markdown. Index
#     verbs (put/embed/…) become no-ops because the on-disk pages ARE the source.
#
# So a fresh install with no engine still has a searchable brain — gbrain is now an
# optional accelerator (better ranking, semantic), never a hard dependency.
# Used by: scripts/devbrain (`devbrain brain …`), the /continue + /distill skills.
set -uo pipefail

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

# gbrain on PATH -> hand the whole call straight through; nothing below runs.
command -v gbrain >/dev/null 2>&1 && exec gbrain "$@"

# ── offline fallback (no gbrain) ────────────────────────────────────────────
brain_files() { find "$DATA"/projects -type f -path '*/brain/*.md' 2>/dev/null; }
slug_of() {  # projects/<project>/brain/<page>.md -> <project>/<page> (one per line)
  local f="$1"; printf '%s/%s\n' "$(basename "$(dirname "$(dirname "$f")")")" "$(basename "$f" .md)"
}

# Keyword search: tokenize the whole query, score each page by how many DISTINCT
# terms it hits (then by total hits), require >=1, print gbrain-shaped lines. OR-
# ranked, not strict AND — gbrain's `search` is AND, but a keyless fallback must
# stay useful for the natural-language queries Step 7 sends when there's no key.
fallback_search() {
  local query="$*"
  # split on non-word chars; drop <=2-char tokens and a tiny stopword set.
  local terms=() t lc
  for t in $(printf '%s' "$query" | tr -cs '[:alnum:]_' ' '); do
    lc="$(printf '%s' "$t" | tr '[:upper:]' '[:lower:]')"
    [ "${#lc}" -gt 2 ] || continue
    case " and the for with that this from your you are not how does into " in
      *" $lc "*) continue ;;
    esac
    terms+=("$lc")
  done
  [ "${#terms[@]}" -gt 0 ] || { echo "No results."; return 0; }

  local f matched score t c first out
  # Capture into a var FIRST, then decide — never end on `head | … || echo`: with
  # >20 hits `head` closes the pipe early, `sort` dies with SIGPIPE, and `set -o
  # pipefail` would make the whole pipeline "fail" and append a bogus "No results."
  # AFTER real hits. The command substitution isolates that SIGPIPE from the test below.
  out="$(
    brain_files | while IFS= read -r f; do
      [ -n "$f" ] || continue
      matched=0; score=0
      for t in "${terms[@]}"; do
        c=$(grep -Fic -- "$t" "$f" 2>/dev/null) || c=0
        [ "$c" -gt 0 ] && { matched=$((matched+1)); score=$((score+c)); }
      done
      [ "$matched" -gt 0 ] || continue
      # excerpt: first line containing any term, trimmed.
      first=""
      for t in "${terms[@]}"; do
        first=$(grep -Fim1 -- "$t" "$f" 2>/dev/null | sed 's/^[[:space:]#>*-]*//')
        [ -n "$first" ] && break
      done
      printf '%d\t%d\t%s\t%s\n' "$matched" "$score" "$(slug_of "$f")" "$first"
    done | sort -t"$(printf '\t')" -k1,1rn -k2,2rn | head -20 | \
      while IFS="$(printf '\t')" read -r matched score slug first; do
        printf '[%d.%04d] %s -- %s\n' "$matched" "$score" "$slug" "$first"
      done
  )"
  [ -n "$out" ] && printf '%s\n' "$out" || echo "No results."
}

# Direct page read by <project>/<page> slug. --fuzzy resolves a bare/near slug the
# way gbrain does: unique basename match wins; multiple -> "Did you mean" hints.
fallback_get() {
  local fuzzy=0 slug="" a
  for a in "$@"; do
    case "$a" in --fuzzy) fuzzy=1 ;; --*) ;; *) [ -z "$slug" ] && slug="$a" ;; esac
  done
  [ -n "$slug" ] || { echo "usage: brain get <project>/<page> [--fuzzy]" >&2; return 1; }

  local f="$DATA/projects/${slug%%/*}/brain/${slug#*/}.md"
  [ -f "$f" ] && { cat "$f"; return 0; }

  if [ "$fuzzy" = 1 ]; then
    local page="${slug##*/}" hits mf
    hits=$(brain_files | while IFS= read -r mf; do
      [ "$(basename "$mf" .md)" = "$page" ] && slug_of "$mf"; done)
    if [ "$(printf '%s' "$hits" | grep -c .)" = 1 ]; then
      cat "$DATA/projects/${hits%%/*}/brain/${hits#*/}.md"; return 0
    elif [ -n "$hits" ]; then
      echo "page_not_found: $slug"; echo "Did you mean:"; printf '  %s\n' $hits; return 0
    fi
  fi
  echo "page_not_found: $slug (gbrain not installed; offline read found no such page)"; return 0
}

sub="${1:-}"; shift 2>/dev/null || true
case "$sub" in
  search|query|ask) fallback_search "$@" ;;
  get)              fallback_get "$@" ;;
  put|tag|embed|link|import|sync|delete)
    # index ops are gbrain-only; on-disk pages are the source, so skipping is safe.
    : ;;
  list)
    brain_files | while IFS= read -r f; do slug_of "$f"; done ;;
  ""|help|--help|-h)
    echo "brain — offline brain reader (gbrain not installed)"
    echo "  brain search <terms>     keyword search over on-disk pages"
    echo "  brain get <slug> [--fuzzy]  read a page"
    echo "  brain list               list page slugs" ;;
  *) echo "brain: '$sub' needs gbrain; only search/get/list work offline" >&2 ;;
esac
