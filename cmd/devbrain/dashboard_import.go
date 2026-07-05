// Registers the dashboard server and the transcript importer verbs.
package main

import (
	"os"

	"github.com/TheWeiHu/devbrain/internal/dashboard"
	"github.com/TheWeiHu/devbrain/internal/importer"
)

func init() {
	dash := func(args []string) int {
		return dashboard.Run(args, os.Stdout, os.Stderr)
	}
	commands["dashboard"] = dash
	commands["queue"] = dash // hidden back-compat alias (former name)
	commands["import"] = func(args []string) int {
		return importer.Run(args, os.Stdout, os.Stderr)
	}
}
