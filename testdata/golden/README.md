# Goldens — immutable behavioral spec

These fixtures were captured from the retired bash/python implementation
(the generator lived at `scripts/capture-goldens.sh`; see commit history).
They are now the immutable behavioral spec for the Go binary: never
regenerate them from the Go code — that would let a regression rewrite
its own gate.
