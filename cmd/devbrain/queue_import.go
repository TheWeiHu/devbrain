// Registers the dashboard server and the transcript importer verbs.
package main

import (
	"os"

	"github.com/TheWeiHu/devbrain/internal/importer"
	"github.com/TheWeiHu/devbrain/internal/queue"
)

func init() {
	dashboard := func(args []string) int {
		return queue.Run(args, os.Stdout, os.Stderr)
	}
	commands["dashboard"] = dashboard
	commands["queue"] = dashboard // hidden back-compat alias (former name)
	commands["import"] = func(args []string) int {
		return importer.Run(args, os.Stdout, os.Stderr)
	}
}
