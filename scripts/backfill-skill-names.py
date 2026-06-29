#!/usr/bin/env python3
"""devbrain — backfill skill NAMES into already-captured logs.

The live capture once recorded an invoked skill as a nameless `Skill×N` in a turn's
`tools:` meta. The dashboard can't tell WHICH skill that was, so an autonomous call
(no leading slash in the prompt) gets dropped from the Skills charts. But the name is
not lost: the original Claude Code transcript on disk still has the `Skill` tool_use
with its `input.skill`. This pass re-reads the transcripts and rewrites each bare
`Skill×N` into the named form `Skill:<name>×k` — exactly what capture-response.sh now
writes live, so old and new logs read identically on the dashboard.

Matching is per-session and order-based: both the log's meta lines and the transcript's
Skill tool-uses are chronological, so we consume names from the session's ordered list as
we walk its bare tokens top-to-bottom. Idempotent (already-named `Skill:` tokens are left
alone) and additive (only the tools field is touched — prompts, recaps, tokens untouched).

Usage:  backfill-skill-names.py [--data DIR] [--claude DIR] [--dry-run]
"""
import argparse, glob, json, os, re, sys

# A bare token is `Skill×N` with NO `:name` — those are the ones we can still name.
_BARE = re.compile(r"(?<![:\w])Skill×(\d+)")


def transcript_skill_names(path):
    """Ordered list of skill names from every Skill tool_use in a transcript."""
    out = []
    try:
        fh = open(path, encoding="utf-8", errors="replace")
    except OSError:
        return out
    for ln in fh:
        try:
            e = json.loads(ln)
        except Exception:
            continue
        if e.get("type") != "assistant":
            continue
        for b in (e.get("message", {}) or {}).get("content", []):
            if isinstance(b, dict) and b.get("type") == "tool_use" and b.get("name") == "Skill":
                inp = b.get("input") or {}
                out.append(inp.get("skill") or inp.get("name") or "?")
    return out


def named_tokens(names):
    """['codex','distill'] -> 'Skill:codex×1, Skill:distill×1' (aggregated, order-stable)."""
    counts, order = {}, []
    for n in names:
        if n not in counts:
            order.append(n)
        counts[n] = counts.get(n, 0) + 1
    return ", ".join(f"Skill:{n}×{counts[n]}" for n in order)


def _is_meta_line(line):
    """A turn's tools meta is the indented `touched: … · tools: …` line written by
    capture-response.sh. Response-sample lines (`   > …`) can quote "Skill×1" verbatim,
    so we must NEVER rewrite those — only the real meta line."""
    s = line.lstrip()
    return (s.startswith("touched:") or s.startswith("tools:")) and "tools:" in s


def rewrite_log(text, queue):
    """Replace bare Skill×N tokens ON META LINES ONLY, consuming `queue` (mutated) in
    order. Returns (new_text, replaced_token_count, drawn_names). A token whose names
    can't be drawn (queue exhausted) is left bare — best-effort, never invents a name."""
    n, drawn_all, lines = 0, [], text.split("\n")
    for i, line in enumerate(lines):
        if "Skill×" not in line or not _is_meta_line(line):
            continue
        out, idx = [], 0
        for m in _BARE.finditer(line):
            k = int(m.group(1))
            out.append(line[idx:m.start()])
            if len(queue) >= k:
                drawn = queue[:k]; del queue[:k]
                out.append(named_tokens(drawn)); drawn_all.extend(drawn); n += 1
            else:
                out.append(m.group(0))
            idx = m.end()
        out.append(line[idx:])
        lines[i] = "".join(out)
    return "\n".join(lines), n, drawn_all


def main():
    ap = argparse.ArgumentParser(description="Backfill skill names into captured logs from transcripts.")
    ap.add_argument("--data", default=os.environ.get("DEVBRAIN_DATA", os.path.expanduser("~/devbrain-data")))
    ap.add_argument("--claude", default=os.path.expanduser("~/.claude"))
    ap.add_argument("--dry-run", action="store_true", help="report what would change; write nothing")
    args = ap.parse_args()

    tx = {os.path.basename(f)[:-6]: f for f in
          glob.glob(os.path.join(args.claude, "projects", "*", "*.jsonl"))}

    logs = glob.glob(os.path.join(args.data, "projects", "*", "log", "*", "*.md"))
    files_changed = tokens_named = no_transcript = 0
    by_skill = {}
    for f in logs:
        text = open(f, encoding="utf-8", errors="replace").read()
        if "Skill×" not in text:            # fast skip: no bare token anywhere
            continue
        if not any(_BARE.search(l) for l in text.split("\n") if _is_meta_line(l)):
            continue                        # "Skill×" only in quoted prose — nothing to name
        sid = os.path.basename(f)[:-3].split(".", 1)[-1]
        path = tx.get(sid)
        if not path:
            no_transcript += 1
            continue
        queue = transcript_skill_names(path)
        new, n, drawn = rewrite_log(text, queue)
        if n and new != text:
            files_changed += 1
            tokens_named += n
            for nm in drawn:
                by_skill[nm] = by_skill.get(nm, 0) + 1
            if not args.dry_run:
                open(f, "w", encoding="utf-8").write(new)

    verb = "would name" if args.dry_run else "named"
    print(f"{verb} {tokens_named} bare Skill×N token(s) across {files_changed} log file(s).")
    if no_transcript:
        print(f"  {no_transcript} log(s) had a bare Skill×N but no transcript on disk — left as-is.")
    if by_skill:
        print("  recovered skills: " +
              ", ".join(f"{k}×{v}" for k, v in sorted(by_skill.items(), key=lambda x: -x[1])))
    return 0


if __name__ == "__main__":
    sys.exit(main())
