# Releasing devbrain

Releases are cut **locally** with [goreleaser](https://goreleaser.com); there is no
release CI. A tagged release builds the binaries, publishes a GitHub Release, and opens
a **PR** against the Homebrew tap ([TheWeiHu/homebrew-devbrain](https://github.com/TheWeiHu/homebrew-devbrain))
that bumps `Formula/devbrain.rb`. You merge that PR to make `brew upgrade` see the new version.

## One-time setup

- `brew install goreleaser`
- `GITHUB_TOKEN` = a personal token with write access to **both** `devbrain` and
  `homebrew-devbrain` (goreleaser opens the tap PR with it). A repo-scoped token is not enough.

## Cut a release

```sh
# 1. Bump the version (source of truth for ldflags + the brew test)
echo 1.3.0 > VERSION
git commit -am "Release v1.3.0" && git push        # land on main first

# 2. Tag main and release
git tag v1.3.0 && git push origin v1.3.0
GITHUB_TOKEN=<pat> goreleaser release --clean       # builds, drafts the release, opens the tap PR

# 3. Confirm goreleaser actually opened the tap PR BEFORE publishing anything.
#    goreleaser can log a PR-open failure (bad token/scope) without failing the release,
#    which would leave you with a published release and no tap update. Verify it exists:
gh pr list -R TheWeiHu/homebrew-devbrain --head "devbrain-1.3.0"   # must show one PR

# 4. Publish the DRAFT release so the download URLs go live
gh release edit v1.3.0 --draft=false

# 5. Merge the tap PR from step 3 (formula → tap main)
gh pr merge -R TheWeiHu/homebrew-devbrain --squash "<pr#>"

# 6. Verify brew actually serves it
scripts/brew-canary.sh 1.3.0
```

**Order matters (publish step 4 before merging step 5).** The release is drafted first, so
its asset URLs 404 until step 4. The tap formula points at those URLs, so merge the tap PR
(step 5) only *after* publishing the release (step 4) — otherwise `brew install` is briefly
broken. Opening the formula update as a PR (not a direct push) is what makes this ordering
safe: nothing hits the tap's `main` until you merge.

## Notes

- **Formula, not cask.** goreleaser deprecates `brews` for `homebrew_casks`, but casks are
  macOS-only and devbrain ships a Linux binary. We stay on the formula generator. `goreleaser
  check` warns about the deprecation; it is not gated.
- **Prereleases** (`v1.3.0-rc1`) skip the tap automatically (`skip_upload: auto`).
- **No CHANGELOG** — release notes are generated from the git log between tags.
- `.github/workflows/release-check.yml` runs a goreleaser snapshot build on any PR that
  touches `.goreleaser.yaml`, so config breakage is caught before a real release.
