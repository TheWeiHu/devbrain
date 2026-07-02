// nightshift verb registration: the full verb CLI (start/watch/status/…),
// the run/emit plumbing, and the hidden `internal` test entrypoints.
package main

import (
	"os"

	"github.com/TheWeiHu/devbrain/internal/nightshift"
)

func init() {
	commands["nightshift"] = func(args []string) int {
		return nightshift.RunCLI(args, os.Stdout, os.Stderr)
	}
}
