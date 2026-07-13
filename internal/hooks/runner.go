package hooks

import (
	"io"
	"os"
	"time"
)

// maxPayload caps the hook event read (a runaway harness payload must not
// balloon a hook process).
const maxPayload = 10 << 20

// Run executes a hook handler under the fail-open contract the legacy shell
// scripts enforced with `|| exit 0` everywhere: bad JSON, missing data repo,
// unwritable dirs, panics, or a wedged filesystem must never break the
// user's turn. Always returns 0; a hard timer exits the process if a handler
// hangs past the deadline.
func Run(h func(*Event) error) (code int) {
	defer func() {
		recover() // a panic is a swallowed failure, not a broken turn
	}()
	time.AfterFunc(10*time.Second, func() { os.Exit(0) })
	payload, _ := io.ReadAll(io.LimitReader(os.Stdin, maxPayload))
	_ = h(&Event{Payload: payload})
	return 0
}

// Handlers maps hook event names to their handlers (the `devbrain hook X`
// dispatch table).
var Handlers = map[string]func(*Event) error{
	"gbrain":        Gbrain,
	"session-start": SessionStart,
	"turn-marker":   TurnMarker,
}
