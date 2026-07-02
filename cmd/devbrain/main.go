// devbrain — one binary for capture hooks, the TODO queue, the dashboard
// server, import, brain access, install wiring, and nightshift. Subcommands
// mirror the legacy CLI surface verb-for-verb; `devbrain internal …` exposes
// the shared library primitives for tests and skills.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/jsonedit"
	"github.com/TheWeiHu/devbrain/internal/projectkey"
	"github.com/TheWeiHu/devbrain/internal/redact"
	"github.com/TheWeiHu/devbrain/internal/version"
)

const usage = `devbrain — prompts in, brain out

  devbrain todo <verb> …          TODO queue (add/list/next/claim/… )
  devbrain queue [--port N]       dashboard server (Board / Profile / Nightshift)
  devbrain import [--apply] …     backfill from agent transcripts
  devbrain brain <args>           brain query (gbrain, or offline fallback)
  devbrain rebuild                rebuild the brain index
  devbrain flush [reason]         commit+push the data repo
  devbrain nightshift <verb> …    autonomous overnight fleet
  devbrain hook <event>           harness hook entrypoints (stdin JSON)
  devbrain project-key [cwd]      print the project identity slug
  devbrain link-preferences       wire the preferences @import
  devbrain install / uninstall    machine wiring
  devbrain version | help
`

// commands maps verb -> handler. Later phases register more entries.
var commands = map[string]func(args []string) int{
	"version":     cmdVersion,
	"help":        cmdHelp,
	"-h":          cmdHelp,
	"--help":      cmdHelp,
	"project-key": cmdProjectKey,
	"internal":    cmdInternal,
}

func main() {
	args := os.Args[1:]
	// Legacy alias support: a `devbrain-todo` symlink behaves as `devbrain todo`.
	if base := filepath.Base(os.Args[0]); strings.HasPrefix(base, "devbrain-") {
		args = append([]string{strings.TrimPrefix(base, "devbrain-")}, args...)
	}
	verb := "help"
	if len(args) > 0 {
		verb = args[0]
		args = args[1:]
	}
	handler, ok := commands[verb]
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		fmt.Fprintf(os.Stderr, "devbrain: unknown command: %s\n", verb)
		os.Exit(2)
	}
	os.Exit(handler(args))
}

func cmdVersion(args []string) int {
	fmt.Println(version.String())
	return 0
}

func cmdHelp(args []string) int {
	fmt.Print(usage)
	return 0
}

func cmdProjectKey(args []string) int {
	cwd := ""
	if len(args) > 0 {
		cwd = args[0]
	} else if wd, err := os.Getwd(); err == nil {
		cwd = wd
	}
	fmt.Print(projectkey.ProjectKey(cwd))
	return 0
}

// cmdInternal mirrors the legacy devbrain_lib.py CLI modes: stable, hidden
// entrypoints for the parity tests and skills (stdin -> stdout, no
// trailing-newline surprises).
func cmdInternal(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: devbrain internal {redact|prompt-filter|register-hook FILE EVENT MATCHER CMD|unregister-hook FILE CMD...}")
		return 2
	}
	mode, rest := args[0], args[1:]
	switch mode {
	case "redact":
		data, _ := io.ReadAll(os.Stdin)
		fmt.Print(redact.Redact(string(data)))
		return 0
	case "prompt-filter":
		data, _ := io.ReadAll(os.Stdin)
		fmt.Print(redact.PromptFilter(string(data)))
		return 0
	case "register-hook":
		if len(rest) < 4 {
			fmt.Fprintln(os.Stderr, "register-hook: FILE EVENT MATCHER CMD")
			return 2
		}
		if err := jsonedit.RegisterHook(rest[0], rest[1], rest[2], rest[3]); err != nil {
			fmt.Fprintf(os.Stderr, "register-hook: %v\n", err)
			return 1
		}
		return 0
	case "unregister-hook":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "unregister-hook: FILE CMD...")
			return 2
		}
		if err := jsonedit.UnregisterHook(rest[0], rest[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "unregister-hook: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "devbrain internal: unknown mode: %s\n", mode)
	return 2
}
