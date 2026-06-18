# Homebrew distribution

devbrain ships as a Homebrew **tap** — a public repo you own, no homebrew-core
gatekeeping. End users get:

```bash
brew install theweihu/devbrain/devbrain
devbrain setup        # wire this machine (hooks, flusher, skills)
```

`brew install` lays down the CLI artifact only; `devbrain setup` does the per-user
machine wiring (the same proven `./setup` → `scripts/install.sh` path). They are
split on purpose: a Homebrew formula must be relocatable and free of `$HOME` side
effects, so it cannot run the wiring at install time.

`devbrain.rb` here is the canonical source. The tap repo just mirrors it.

## One-time: create the tap

1. Create a public GitHub repo named **`TheWeiHu/homebrew-devbrain`** (the
   `homebrew-` prefix is mandatory — `brew` strips it).
2. Add the formula at `Formula/devbrain.rb` (copy this dir's `devbrain.rb`).
3. Done. `brew install theweihu/devbrain/devbrain` resolves
   `<user>/<tap>/<formula>` to that file.

## Each release

After `scripts/release.sh <ver> --push` cuts the `vX.Y.Z` tag + GitHub Release:

```bash
ver=X.Y.Z
url="https://github.com/TheWeiHu/devbrain/archive/refs/tags/v${ver}.tar.gz"
sha=$(curl -sL "$url" | shasum -a 256 | awk '{print $1}')
# In the tap's Formula/devbrain.rb, set:
#   url "$url"
#   sha256 "$sha"
```

Commit the formula bump to the tap repo. (`brew bump-formula-pr` can automate the
url+sha edit if you prefer.)

## Verify locally before publishing

```bash
brew install --build-from-source ./devbrain.rb   # from this dir
brew test devbrain
brew audit --strict --new devbrain
devbrain setup                                   # smoke-test the wiring handoff
```

## Notes

- The hooks `devbrain setup` installs are **stable copies** under `~/.claude` that
  don't reference the Cellar path, so `brew upgrade devbrain` won't break existing
  wiring — just re-run `devbrain setup` afterward to refresh the copies.
- `nightshift` stays opt-in: `devbrain setup --with nightshift`.
- No bottle is published; users build from the source tarball (it's all scripts —
  there's nothing to compile, so it's instant).
