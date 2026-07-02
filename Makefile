# devbrain — `make help` lists targets; `make test` runs the full suite (scripts/test-all.sh).
.PHONY: help test
.DEFAULT_GOAL := test  # bare `make` keeps running the suite, as before

help:  ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "} {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

test:  ## Run the full test suite (scripts/test-all.sh)
	@bash scripts/test-all.sh
