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

	"github.com/TheWeiHu/devbrain/internal/brain"
	"github.com/TheWeiHu/devbrain/internal/flush"
	"github.com/TheWeiHu/devbrain/internal/gbrainlog"
	"github.com/TheWeiHu/devbrain/internal/hookev"
	"github.com/TheWeiHu/devbrain/internal/hooks"
	"github.com/TheWeiHu/devbrain/internal/jsonedit"
	"github.com/TheWeiHu/devbrain/internal/projectkey"
	"github.com/TheWeiHu/devbrain/internal/redact"
	"github.com/TheWeiHu/devbrain/internal/retro"
	"github.com/TheWeiHu/devbrain/internal/todo"
	"github.com/TheWeiHu/devbrain/internal/transcript"
	"github.com/TheWeiHu/devbrain/internal/version"
)

const usage = `devbrain — prompts in, brain out

  devbrain todo <verb> …          TODO queue (add/list/next/claim/… )
  devbrain dashboard [--port N]   browser control plane (Board / Profile / Nightshift)
  devbrain import [--apply] …     backfill from agent transcripts
  devbrain brain <args>           brain query (gbrain, or offline fallback)
  devbrain rebuild                rebuild the brain index
  devbrain retro [--days N]       monthly retro page from the journal cache
  devbrain flush [reason]         commit+push the data repo
  devbrain nightshift <verb> …    autonomous overnight fleet
  devbrain hook <event>           harness hook entrypoints (stdin JSON)
  devbrain project-key [cwd]      print the project identity slug
  devbrain link-preferences       wire the preferences @import
  devbrain install                wire this machine (hooks, skills, dashboard)
  devbrain uninstall              remove the wiring (data repo untouched)
  devbrain doctor [--fix]         audit capture hooks; --fix re-points them
  devbrain version | help
`

// commands maps verb -> handler. Later phases register more entries.
var commands = map[string]func(args []string) int{
	"version":     cmdVersion,
	"--version":   cmdVersion,
	"-v":          cmdVersion,
	"help":        cmdHelp,
	"-h":          cmdHelp,
	"--help":      cmdHelp,
	"project-key": cmdProjectKey,
	"internal":    cmdInternal,
	"hook":        cmdHook,
	"todo": func(args []string) int {
		return todo.Run(args, os.Stdout, os.Stderr, os.Stdin)
	},
	"brain": func(args []string) int {
		return brain.Run(args, os.Stdout, os.Stderr, os.Stdin)
	},
	"rebuild": func(args []string) int {
		return brain.Rebuild(os.Stdout, os.Stderr)
	},
	"flush": func(args []string) int {
		return flush.Run(args, os.Stdout, os.Stderr)
	},
	"retro": func(args []string) int {
		return retro.Run(args, os.Stdout, os.Stderr)
	},
}

// cmdHook runs one harness hook handler under the fail-open contract:
// whatever happens, exit 0 and never block the agent's turn.
func cmdHook(args []string) int {
	if len(args) == 0 {
		return 0 // even a misregistered hook must not break a turn
	}
	h, ok := hooks.Handlers[args[0]]
	if !ok {
		return 0
	}
	return hooks.Run(h)
}

func main() {
	args := os.Args[1:]
	// Legacy alias support: a `devbrain-todo` symlink behaves as `devbrain todo`.
	// Restricted to known verbs so an unrelated binary name (devbrain-snapshot,
	// devbrain-backup, …) can never be reinterpreted as a command.
	if base := filepath.Base(os.Args[0]); strings.HasPrefix(base, "devbrain-") {
		if verb := strings.TrimPrefix(base, "devbrain-"); commands[verb] != nil {
			args = append([]string{verb}, args...)
		}
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
		os.Exit(1)
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
	case "read-event":
		// one normalized field per call (multiline-safe, like a single jq pull)
		data, _ := io.ReadAll(os.Stdin)
		field := ""
		if len(rest) > 0 {
			field = rest[0]
		}
		fmt.Print(hookev.ReadEvent(string(data), field, ""))
		return 0
	case "session-start-context":
		data, _ := io.ReadAll(os.Stdin)
		fmt.Print(hookev.SessionStartContext(string(data)))
		return 0
	case "response-capture":
		// TRANSCRIPT [SIDECAR SESSION TS AUTO FALLBACK_TEXT] — legacy CLI shape
		if len(rest) < 1 {
			return 0
		}
		get := func(i int) string {
			if i < len(rest) {
				return rest[i]
			}
			return ""
		}
		fmt.Print(transcript.ResponseCapture(get(0), get(1), get(2), get(3), get(4) == "1", get(5)))
		return 0
	case "gbrain-record":
		if len(rest) < 3 {
			return 0
		}
		fmt.Print(gbrainlog.Record(rest[0], rest[1], rest[2], os.Getenv("TS"), os.Getenv("AUTO") == "1"))
		return 0
	case "recap":
		data, _ := io.ReadAll(os.Stdin)
		fmt.Print(transcript.Recap([]string{string(data)}))
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
