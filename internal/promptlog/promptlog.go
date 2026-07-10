// Package promptlog owns the append-only raw prompt entry framing.
package promptlog

import "strings"

const (
	FileMarker  = "> format: devbrain-log-v2"
	EntryMarker = "<!-- devbrain:prompt-v2 -->"
)

// FormatEntry frames one prompt so user-controlled Markdown can never look
// like an entry header or response recap. Each line receives one reversible
// prefix; the per-entry marker supports mixed v1/v2 files during upgrades.
func FormatEntry(timestamp, prompt string) string {
	lines := strings.Split(prompt, "\n")
	for i := range lines {
		lines[i] = "| " + lines[i]
	}
	return "## " + timestamp + "\n\n" + EntryMarker + "\n" + strings.Join(lines, "\n") + "\n\n"
}

// DecodeEntryBody returns the prompt body from the lines after an entry
// heading. It reports false for a legacy v1 entry.
func DecodeEntryBody(lines []string) (string, bool) {
	i := 0
	for i < len(lines) && lines[i] == "" {
		i++
	}
	if i >= len(lines) || lines[i] != EntryMarker {
		return "", false
	}
	i++
	var body []string
	for i < len(lines) && strings.HasPrefix(lines[i], "| ") {
		body = append(body, strings.TrimPrefix(lines[i], "| "))
		i++
	}
	return strings.Join(body, "\n"), true
}
