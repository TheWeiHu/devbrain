# Releasing devbrain

Releases are cut locally with [goreleaser](https://goreleaser.com). A tag builds the
binaries, publishes a GitHub Release, and pushes the bumped `Formula/devbrain.rb` to the
Homebrew tap ([TheWeiHu/homebrew-devbrain](https://github.com/TheWeiHu/homebrew-devbrain)) —
so `brew upgrade devbrain` just works, no hand-edited formula.

**One-time:** `brew install goreleaser`, and a `GITHUB_TOKEN` (personal PAT) with write on
both `devbrain` and `homebrew-devbrain`.

```sh
echo 1.3.0 > VERSION && git commit -am "Release v1.3.0" && git push   # land on main
git tag v1.3.0 && git push origin v1.3.0
GITHUB_TOKEN=<pat> goreleaser release --clean                         # release + tap push
scripts/brew-canary.sh 1.3.0                                          # verify brew serves it
```

Prereleases (`v1.3.0-rc1`) skip the tap automatically. Release notes come from the git log
between tags (no CHANGELOG). goreleaser deprecates `brews` for `homebrew_casks`, but casks
are macOS-only and devbrain ships a Linux binary, so we stay on the formula generator.
