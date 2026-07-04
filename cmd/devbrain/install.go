// Machine-wiring verbs: install / uninstall / link-preferences. Registered
// from init() so main.go stays untouched (parallel porting convention).
package main

import (
	"os"

	"github.com/TheWeiHu/devbrain/internal/install"
)

func init() {
	commands["install"] = func(args []string) int {
		return install.Run(args, os.Stdout, os.Stderr, os.Stdin)
	}
	commands["uninstall"] = func(args []string) int {
		return install.Uninstall(args, os.Stdout, os.Stderr)
	}
	commands["doctor"] = func(args []string) int {
		return install.Doctor(args, os.Stdout, os.Stderr)
	}
	commands["link-preferences"] = func(args []string) int {
		return install.LinkPreferences(args, os.Stdout, os.Stderr)
	}
}
