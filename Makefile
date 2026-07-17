# devbrain — `make help` lists targets. The whole suite is Go now:
# `make test` == `go test ./...` (unit, golden, and CLI black-box tests that
# build the binary and drive it as a subprocess via internal/clitest).
.PHONY: help build test release

.DEFAULT_GOAL := test  # bare `make` keeps running the suite, as before

help:  ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "} {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

build:  ## Build the devbrain binary at the repo root (version from VERSION)
	@go build -ldflags "-X github.com/TheWeiHu/devbrain/internal/version.Version=$$(cat VERSION)" -o devbrain ./cmd/devbrain

test:  ## Go vet + the full test suite (unit, golden, and CLI black-box against the built binary)
	@go vet ./... && go test ./...

release:  ## Publish the tagged release: goreleaser, brew formula push, brew canary
	GITHUB_TOKEN=$${GITHUB_TOKEN:-$$(gh auth token)} sh -c '\
		goreleaser release --clean && \
		scripts/brew-formula-push.sh "$$(cat VERSION)" && \
		scripts/brew-canary.sh "$$(cat VERSION)"'
