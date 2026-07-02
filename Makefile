# devbrain — `make help` lists targets. Tiers: `make unit` (T0, seconds),
# `make parity` (T1, bash suite against the Go binary), `make test` (full suite).
.PHONY: help build test unit parity e2e-brew

e2e-brew:  ## T2: clean-environment brew install e2e on the Linux box
	@bash scripts/e2e/e2e-brew.sh
.DEFAULT_GOAL := test  # bare `make` keeps running the suite, as before

help:  ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "} {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

build:  ## Build the devbrain binary at the repo root
	@go build -o devbrain ./cmd/devbrain

unit:  ## T0: Go vet + unit/golden tests
	@go vet ./... && go test ./...

parity: build  ## T1: bash behavioral suite against the Go binary (fast tier)
	@DEVBRAIN_BIN="$(CURDIR)/devbrain" DEVBRAIN_TEST_SKIP='docker|dogfood|npm-pack|release' bash scripts/test-all.sh

test: build  ## Run the full test suite (scripts/test-all.sh)
	@DEVBRAIN_BIN="$(CURDIR)/devbrain" bash scripts/test-all.sh
