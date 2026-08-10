# devbrain — `make help` lists targets. The main suite is Go:
# `make test` == `go test ./...` (unit, golden, and CLI black-box tests that
# build the binary and drive it as a subprocess via internal/clitest), wrapped by
# scripts/test-guard.sh so a test that escapes its tempdir can't corrupt this repo.
.PHONY: help build test release

.DEFAULT_GOAL := test  # bare `make` keeps running the suite, as before

help:  ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "} {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

build:  ## Build the devbrain binary at the repo root (version from VERSION)
	@go build -ldflags "-X github.com/TheWeiHu/devbrain/internal/version.Version=$$(cat VERSION)" -o devbrain ./cmd/devbrain

test:  ## Go vet + full test suite, plus dashboard regression when Node is available
	@scripts/test-guard.sh $(GOTESTFLAGS)
	@if command -v node >/dev/null 2>&1; then node scripts/test-dashboard-concurrency.mjs; else echo "dashboard regression skipped (node unavailable)"; fi

release:  ## Manual fallback — CI releases on tag push (.github/workflows/release.yml)
	GITHUB_TOKEN=$${GITHUB_TOKEN:-$$(gh auth token)} sh -c '\
		goreleaser release --clean && \
		scripts/brew-formula-push.sh "$$(cat VERSION)" && \
		scripts/brew-canary.sh "$$(cat VERSION)"'
