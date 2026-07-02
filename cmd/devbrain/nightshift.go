// nightshift verb registration. Only the `internal` plumbing surface is
// ported so far — the daemon loop / backends / status emitter stay in
// scripts/nightshift* until the daemon phase builds on these foundations.
package main

import (
	"fmt"
	"os"

	"github.com/TheWeiHu/devbrain/internal/nightshift"
)

func init() {
	commands["nightshift"] = cmdNightshift
}

func cmdNightshift(args []string) int {
	if len(args) > 0 && args[0] == "internal" {
		return nightshift.RunInternal(args[1:], os.Stdout, os.Stderr)
	}
	fmt.Fprintln(os.Stderr, "devbrain nightshift: only the `internal` plumbing verbs are ported; use scripts/nightshift for start/stop/watch until the daemon phase lands")
	return 2
}
