package brain

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// putNeedsStdin reports whether gbrain will read the page from stdin for this
// put: it does unless --content carries the page inline. gbrain documents a
// trailing --content with no value as "read stdin", so only --content=<text>
// and --content <text> count as inline.
func putNeedsStdin(args []string) bool {
	for i, arg := range args {
		if strings.HasPrefix(arg, "--content=") {
			return false
		}
		if arg == "--content" && i+1 < len(args) {
			return false
		}
	}
	return true
}

// putPage runs `gbrain put` and, when the engine refuses because the page
// already exists (revision_required, gbrain >= 0.51), writes it again with
// --force.
//
// gbrain's write contract wants the revision a caller read before it replaces
// a page. devbrain has no such reader: the Markdown page on disk is the source
// of truth and this wrapper is the index's only writer, so the disk copy always
// wins and a revision conflict is never a real one here. Older engines never
// answer revision_required, so they never see --force. A caller who already
// passed --force or --expected-revision gets gbrain's own answer, because those
// outcomes carry other codes. The superseded receipt from the first attempt is
// dropped when the retry succeeds; any other failure is relayed unchanged.
//
// stdin is buffered only when gbrain would read it (see putNeedsStdin), so an
// interactive `put --content ...` cannot block on a terminal, and the retry
// replays the same bytes.
func putPage(gb string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var content []byte
	if stdin != nil && putNeedsStdin(args) {
		var err error
		if content, err = io.ReadAll(stdin); err != nil {
			fmt.Fprintf(stderr, "brain: reading the page from stdin: %v\n", err)
			return 1
		}
	}
	var out, errOut bytes.Buffer
	code := passthrough(gb, args, &out, &errOut, bytes.NewReader(content))
	if code != 0 && strings.Contains(out.String()+errOut.String(), "revision_required") {
		forced := append(append(make([]string, 0, len(args)+1), args...), "--force")
		return passthrough(gb, forced, stdout, stderr, bytes.NewReader(content))
	}
	stdout.Write(out.Bytes())
	stderr.Write(errOut.Bytes())
	return code
}
