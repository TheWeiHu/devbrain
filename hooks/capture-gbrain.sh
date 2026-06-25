#!/usr/bin/env bash
# devbrain — gbrain call trace (PostToolUse hook on the Bash tool).
#
# Fires AFTER every Bash tool call. If the command ran a `gbrain` subcommand,
# append ONE JSON line to projects/<project>/gbrain-queries.log:
#   {ts, project, cmd, modes, hits, slugs}
# Deterministic — it fires on every Bash call the agent makes, so it can't be
# bypassed and needs no wrapper to be "remembered." Logging only; never blocks,
# always exit 0. Identity is resolved from cwd, exactly like capture.sh.
#
# It logs DIRECTIONAL signal, not exact per-call terms — and that's deliberate:
#  - `cmd`   — the redacted, whitespace-collapsed, truncated command. Its literal
#              text carries the topic even when the query itself is a `$var`
#              (e.g. the words "recent decisions, open items, conventions").
#  - `modes` — the gbrain subcommands the command used (whitelisted, so "gbrain"
#              inside a string/filename doesn't masquerade as a call).
#  - hits/slugs — what actually surfaced in the output; the strongest "aboutness"
#              signal and reliable regardless of how the query was built.
# Why not exact terms: the hook only has the command TEXT (a `$var` is unexpanded)
# plus the combined output, and parsing arbitrary shell for exact per-call queries
# is unreliable (loops run once in text, quoting, vars). Recovering expanded terms
# would need non-portable shell-trace parsing — deliberately not done. `cmd` +
# `slugs` are reliable and directionally describe what a query was about.

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

# Hook payload is JSON on stdin. Field extraction (the per-harness event shim, keyed by
# $DEVBRAIN_HARNESS) lives in devbrain_lib.py — fail OPEN (exit 0) if python3 is missing.
payload="$(cat 2>/dev/null)" || exit 0
# Raw fast-bail: this fires on EVERY Bash tool call, so skip the whole shim (no
# subprocess) for the ~all payloads that mention no gbrain at all. False positives
# (gbrain only in output) are caught by the whitelist gate below — no spurious log.
case "$payload" in *gbrain*) ;; *) exit 0 ;; esac
command -v python3 >/dev/null 2>&1 || exit 0

_lib="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)/devbrain_lib.py"
[ -f "$_lib" ] || _lib="$HOME/.claude/hooks/devbrain_lib.py"
ev() { printf '%s' "$payload" | python3 "$_lib" read-event "$1" 2>/dev/null; }

tool="$(ev tool)"
[ "$tool" = "Bash" ] || exit 0
cmd="$(ev command)"
[ -n "$cmd" ] || exit 0
case "$cmd" in *gbrain*) ;; *) exit 0 ;; esac     # gbrain in command (not just output)

cwd="$(ev cwd)"
[ -n "$cwd" ] || cwd="$PWD"

# tool_response shape varies by version (object with .stdout, or a bare string) — the
# shim coerces whatever it is into printed text so we can parse result lines from it.
out="$(ev tool-response)"

# Identity — shared OFFLINE resolver, so we write to the SAME projects/<owner>__<repo>
# folder capture.sh uses. Installed alongside as devbrain-project-key.sh.
_pk="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
for _c in "$_pk/devbrain-project-key.sh" "$_pk/project-key.sh" "$HOME/.claude/hooks/devbrain-project-key.sh"; do
  [ -f "$_c" ] && { . "$_c"; break; }
done
# --- Route the trace to the repo this call ACTUALLY queried -------------------------
# Identity defaults to the payload cwd (the agent's shell dir). But agents routinely
# run gbrain against another repo's brain — most often by cd-ing into a worktree inline
# from a non-repo parent (`cd <repo> && gbrain …`, or `v="<repo>" (cd "$v" && gbrain …)`),
# whose cwd then has no remote and dumps the trace into the shared "miscellaneous" bucket.
# Two signals recover the real target; either beats cwd, and $DEVBRAIN_PROJECT (explicit
# user override, already honored above) trumps both:
#   1. slug prefix — when the call returned hits, result lines read "[score] owner__repo/page".
#      The prefix names the brain that answered: authoritative (gbrain's OWN output, no
#      command-parsing guesswork), so it wins outright.
#   2. inline `cd` target — writes and zero-hit reads surface no slug; for those, recover
#      the `cd <repo>` the command ran in and use it when it's a hosted <owner>__<repo>.
project="$(devbrain_project_key "$cwd" "$DATA")"; [ -n "$project" ] || project="unknown"

if [ -z "${DEVBRAIN_PROJECT:-}" ]; then
  # 1. Slug prefix from the output. Require the owner__repo shape so a slug-less line
  #    (or the miscellaneous bucket's session files) can't hijack routing.
  slug_proj="$(printf '%s\n' "$out" | sed -n 's/^\[[0-9.][0-9.]*\][[:space:]][[:space:]]*\([A-Za-z0-9._-]*__[A-Za-z0-9._-]*\)\/.*/\1/p' | head -1)"
  if [ -n "$slug_proj" ]; then
    project="$slug_proj"
  else
    # 2. Inline `cd` target. Extracted via python kept at statement level, NOT inside a
    #    $(...) — a quoted heredoc with unbalanced quotes/parens confuses that parser —
    #    so it writes the resolved target to a temp file we read back.
    cd_target=""
    _cdf="$(mktemp 2>/dev/null)" || _cdf=""
    if [ -n "$_cdf" ]; then
      CMD="$cmd" python3 - >"$_cdf" 2>/dev/null <<'PY'
import os, re
cmd = os.environ.get("CMD", "")
# Leading var assignments at a command position (start / after ; && || | ( or ws).
vars = {}
for m in re.finditer(r'(?:^|[\s;&|(])([A-Za-z_]\w*)=("(?:[^"\\]|\\.)*"|\'[^\']*\'|[^\s;&|()]*)', cmd):
    v = m.group(2)
    if v[:1] in ('"', "'"): v = v[1:-1]
    vars[m.group(1)] = v
# First `cd <target>` — literal path or a $VAR / ${VAR} / "$VAR" reference.
m = re.search(r'(?:^|[\s;&|(])cd\s+("(?:[^"\\]|\\.)*"|\'[^\']*\'|[^\s;&|()]+)', cmd)
if not m: raise SystemExit
t = m.group(1)
if t[:1] in ('"', "'"): t = t[1:-1]
mv = re.fullmatch(r'\$\{?(\w+)\}?', t)
if mv: t = vars.get(mv.group(1), "")
if t.startswith("~"): t = os.path.expanduser(t)
print(t)
PY
      cd_target="$(cat "$_cdf" 2>/dev/null)"
      rm -f "$_cdf" 2>/dev/null
    fi
    case "$cd_target" in /*|"") ;; *) cd_target="$cwd/$cd_target" ;; esac   # relative -> resolve vs cwd
    if [ -n "$cd_target" ] && [ -d "$cd_target" ]; then
      cd_project="$(devbrain_project_key "$cd_target" "$DATA")"
      case "$cd_project" in
        ""|miscellaneous|unknown) ;;          # cd target isn't a hosted repo -> keep cwd identity
        *) project="$cd_project" ;;           # the call's real target -> attribute there
      esac
    fi
  fi
fi

# Build one directional record from the command + output, redacted via the shared
# rule lib (devbrain_lib.redact) so secrets never reach the log. Captured to a var,
# not redirected to the log, so a command that mentions "gbrain" but runs no real
# subcommand can't create an empty projects/<project>/ folder (see the gate below).
_libdir="$_pk"; [ -f "$_libdir/devbrain_lib.py" ] || _libdir="$HOME/.claude/hooks"
record="$(TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)" python3 - "$cmd" "$out" "$project" "$_libdir" 2>/dev/null <<'PY'
import sys, re, json, os, shlex
cmd, out, project, libdir = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
sys.path.insert(0, libdir)
try:
    import devbrain_lib; redact = devbrain_lib.redact
except Exception:
    redact = lambda s: s
ts = os.environ.get("TS", "")

# Which gbrain subcommands the command actually used. Whitelisted to real ones, so
# the word "gbrain" inside a string ("log gbrain queries") or a filename
# (capture-gbrain.sh) doesn't masquerade as a call. A space after the subcommand is
# required, which also rules out path-y refs like gbrain-queries.log.
WHITELIST = {"query", "search", "ask", "get", "put", "delete",
             "list", "tag", "link", "embed", "sync", "import", "export"}
modes = []
for m in re.finditer(r'gbrain\s+([a-z][a-z-]*)', cmd):
    s = m.group(1)
    if s in WHITELIST and s not in modes:
        modes.append(s)
if not modes:
    sys.exit(0)   # no real gbrain subcommand -> not a call, don't log

# The command itself, redacted + whitespace-collapsed + truncated. Carries the
# topic words even when the query arg is a variable.
snippet = redact(re.sub(r'\s+', ' ', cmd).strip())
if len(snippet) > 300:
    snippet = snippet[:300] + "…"

# What surfaced — result lines look like "[0.83] owner__repo/slug -- snippet".
slugs, hits = [], 0
for ln in out.splitlines():
    if re.match(r'\[[0-9.]+\]', ln):
        hits += 1
        mm = re.match(r'\[[0-9.]+\]\s+(\S+)\s+--', ln)
        if mm and mm.group(1) not in slugs:
            slugs.append(mm.group(1))

# A `gbrain get <slug>` is a DIRECT page read, not a ranked search — its output is
# the page body, which carries no "[score] slug --" lines, so the loop above leaves
# hits=0. But a get that returns the page IS a hit (the brain handed you the exact
# page you asked for); only a get that returns nothing or page_not_found is a real
# miss. Credit the success and record the requested slug so the page also shows up
# in the "pages surfaced" view, exactly as a searched-to hit would.
# NOTE: this block runs inside a $(...) command substitution, so the shell scans it
# for a matching ")" and miscounts on UNBALANCED quote chars (see the cd-parser note
# above). Keep every quote/apostrophe here in balanced pairs.
if "get" in modes and hits == 0:
    low = out.lower()
    missed = (not out.strip()) or ("page_not_found" in low) \
        or ("did you mean" in low) or ("not found" in low)
    if not missed:
        # Find the page target by TOKENIZING the command (shlex), not regex-scanning
        # its raw text. This anchors to a REAL `gbrain get` invocation: a quoted query
        # such as `gbrain search "gbrain get foo"` collapses to ONE token, so the inner
        # words can no longer masquerade as a get command and fabricate a slug.
        # punctuation_chars peels shell wrappers off the binary token so a command
        # substitution or subshell (`x=$(gbrain get p)`, `(gbrain get p)`) still
        # tokenizes `gbrain` cleanly and the trailing `)` drops off the slug.
        # Tokenize PER LINE: a chained heredoc body with a stray apostrophe is not
        # valid shlex input, and parsing it as part of the whole command would raise
        # and drop a real get hit. Per line, only that fragment is skipped.
        # shlex is not a full bash parser, so some valid lines (e.g. an ANSI-C
        # $'..\'..' on the same line as the get) still will not tokenize. For those
        # we fall back to a plain string scan of the line so a real read is still
        # credited. The scan stays quote-free (no regex parens) to keep this safe
        # inside the enclosing $(...). The fullmatch guard below still vets the slug.
        def _page_arg(seq):
            # First real page argument after `get`: skip flags (--fuzzy) and bare
            # redirection fds (the 2 that punctuation_chars splits out of `2>&1`),
            # and stop at any shell control/redirection token (the get args ended).
            # An option-only call (gbrain get --help) yields no page -> "".
            for t in seq:
                if not t or t.startswith("-") or t.isdigit():
                    continue
                if any(c in t for c in "<>&|;(){}"):
                    return ""
                return t
            return ""
        target = ""
        for line in cmd.splitlines():
            try:
                lex = shlex.shlex(line, posix=True, punctuation_chars=True)
                lex.whitespace_split = True
                lex.commenters = ""
                toks = list(lex)
            except ValueError:
                toks = None
            if toks is not None:
                for i in range(len(toks) - 1):
                    # accept a bare or path-prefixed binary: gbrain / /usr/bin/gbrain
                    if toks[i].rsplit("/", 1)[-1] == "gbrain" and toks[i + 1] == "get":
                        target = _page_arg(toks[i + 2:])
                        if target:
                            break   # keep scanning past an option-only get to a real one
            elif "gbrain get " in line:
                rest = line.split("gbrain get ", 1)[1].split()
                target = _page_arg([t.strip(chr(34) + chr(39) + "();") for t in rest])
            if target:
                break
        if target:
            hits = 1   # a real page argument was read and the output was not a miss
            # Record the slug ONLY when it is a concrete page reference. A target
            # built from a shell var (gbrain get "$page") reaches the hook
            # unexpanded, so its real slug is unknowable — credit the read but do
            # not log a bogus "$page" slug into the surfaced-pages view.
            if re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._/-]*", target) \
                    and target not in slugs:
                slugs.append(target)

print(json.dumps({"ts": ts, "project": project, "cmd": snippet,
                  "modes": modes, "hits": hits, "slugs": slugs},
                 ensure_ascii=False))
PY
)"
[ -n "$record" ] || exit 0     # no real gbrain subcommand -> nothing to log, touch nothing

log="$DATA/projects/$project/gbrain-queries.log"
mkdir -p "$DATA/projects/$project" 2>/dev/null || exit 0
printf '%s\n' "$record" >> "$log" 2>/dev/null

exit 0
