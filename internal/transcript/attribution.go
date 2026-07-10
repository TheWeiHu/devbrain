package transcript

import (
	"path/filepath"
	"regexp"
	"strings"
)

var observedWorkingDirRe = regexp.MustCompile(`(?s)<working_directory>\s*([^<\r\n]+?)\s*</working_directory>`)

const ClaudeMemObserverProject = "automation__claude-mem-observer"

func IsClaudeMemObserverCWD(cwd string) bool {
	return strings.Contains(filepath.ToSlash(cwd), "/.claude-mem/observer-sessions")
}

// SourceCWD recovers the primary session's working directory from a
// claude-mem observer prompt. Observer transcripts execute from a shared
// non-Git directory, but their wrapper includes the cwd whose activity they
// are processing; using that source keeps project and cost attribution intact.
func SourceCWD(cwd, prompt string) string {
	if !IsClaudeMemObserverCWD(cwd) {
		return cwd
	}
	m := observedWorkingDirRe.FindStringSubmatch(prompt)
	if len(m) != 2 {
		return cwd
	}
	source := strings.TrimSpace(m[1])
	if !filepath.IsAbs(source) {
		return cwd
	}
	return filepath.Clean(source)
}
