// Registers the dashboard queue server and the transcript importer verbs.
package main

import (
	"os"

	"github.com/TheWeiHu/devbrain/internal/importer"
	"github.com/TheWeiHu/devbrain/internal/queue"
)

func init() {
	commands["queue"] = func(args []string) int {
		return queue.Run(args, os.Stdout, os.Stderr)
	}
	commands["import"] = func(args []string) int {
		return importer.Run(args, os.Stdout, os.Stderr)
	}
}
