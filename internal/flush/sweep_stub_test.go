package flush

import "io"

// Flush tests exercise git behavior only; the real sweep would read the
// developer's actual ~/.claude and ~/.codex stores (and the refresh their
// real AGENTS.md). Stub both for the package.
func init() {
	Sweep = func(stdout, stderr io.Writer) {}
	RefreshAgents = func() {}
}
