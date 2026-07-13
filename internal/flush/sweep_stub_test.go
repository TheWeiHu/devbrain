package flush

import "io"

// Flush tests exercise git behavior only; the real sweep would read the
// developer's actual ~/.claude and ~/.codex stores. Stub it for the package.
func init() {
	Sweep = func(stdout, stderr io.Writer) {}
}
