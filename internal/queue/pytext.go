// Python string-semantics helpers, aliased to the shared internal/pytext so the
// queue port keeps byte-identical text handling without a private copy.
package queue

import "github.com/TheWeiHu/devbrain/internal/pytext"

func pyStrip(s string) string        { return pytext.Strip(s) }
func pyLStrip(s string) string       { return pytext.LStrip(s) }
func pyIntStr(s string) (int, error) { return pytext.Int(s) }
func splitPyLines(s string) []string { return pytext.SplitLines(s) }
