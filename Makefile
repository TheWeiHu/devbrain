# devbrain — run `make` or `make help` to list targets.
.DEFAULT_GOAL := help
.PHONY: help test

help: ## List available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*## "} {printf "  %-12s %s\n", $$1, $$2}'

test: ## Run the full test suite (scripts/test-all.sh)
	@bash scripts/test-all.sh
