// Package projectkey resolves the offline project identity: which
// projects/<key>/ folder a working directory belongs to. The key is
// <owner>__<repo> parsed from the git origin remote — collision-resistant by
// construction, no lookup table, never fails, no network.
//
// Two parsers live here on purpose. ProjectKey ports hooks/project-key.sh
// (bash parameter-expansion semantics, used for live identity); RemoteToKey
// ports devbrain_lib.remote_to_key (Python semantics, used by import
// routing). They differ at the edges (e.g. bash strips ONE trailing slash,
// Python strips all) and each is pinned by its own fixtures.
package projectkey

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Sanitize ports devbrain_sanitize: lowercase, spaces to dashes, then keep
// only alphanumerics plus . _ - (everything else deleted).
func Sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r == ' ':
			b.WriteByte('-')
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// gitOutput runs git in dir and returns trimmed stdout ("" on any failure).
func gitOutput(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ProjectKey maps cwd to its projects/<key> folder name (bash port).
// $DEVBRAIN_PROJECT overrides; a repo with no remote (or a local-path remote,
// which has no owner/repo shape) lands in the shared "miscellaneous" bucket.
func ProjectKey(cwd string) string {
	if p := os.Getenv("DEVBRAIN_PROJECT"); p != "" {
		return Sanitize(p)
	}
	remote := gitOutput(cwd, "remote", "get-url", "origin")
	// Ignore a local-path origin: its folders aren't an owner/repo.
	for _, p := range []string{"/", "./", "../", "~", "file://"} {
		if strings.HasPrefix(remote, p) {
			remote = ""
			break
		}
	}
	url := strings.TrimSuffix(remote, ".git")
	url = strings.TrimSuffix(url, "/") // bash ${url%/}: one trailing slash only
	repo := url
	if i := strings.LastIndex(url, "/"); i >= 0 {
		repo = url[i+1:]
	}
	owner := ""
	if i := strings.LastIndex(url, "/"); i >= 0 { // bash ${url%/*} != $url
		rest := url[:i]
		owner = rest
		if j := strings.LastIndexAny(rest, ":/"); j >= 0 {
			owner = rest[j+1:]
		}
	}
	if owner != "" && repo != "" {
		return Sanitize(owner + "__" + repo)
	}
	return "miscellaneous"
}

// WorktreeSlug names the session log file's worktree part: the sanitized
// basename of the git toplevel (or of cwd outside a repo), "unknown" if empty.
func WorktreeSlug(cwd string) string {
	top := gitOutput(cwd, "rev-parse", "--show-toplevel")
	if top == "" {
		top = cwd
	}
	slug := Sanitize(filepath.Base(top))
	if slug == "" {
		return "unknown"
	}
	return slug
}

var nonKeyChars = regexp.MustCompile(`[^a-z0-9._-]`)

// RemoteToKey ports devbrain_lib.remote_to_key: git remote URL ->
// <owner>__<repo> (lowercased, filesystem-safe), or "" for no stable identity.
func RemoteToKey(remote string) string {
	if remote == "" {
		return ""
	}
	url := strings.TrimSuffix(remote, ".git")
	url = strings.TrimRight(url, "/") // Python rstrip("/"): all trailing slashes
	repo := url
	if i := strings.LastIndex(url, "/"); i >= 0 {
		repo = url[i+1:]
	}
	owner := ""
	if i := strings.LastIndex(url, "/"); i >= 0 {
		rest := url[:i]
		owner = rest
		if j := strings.LastIndexAny(rest, ":/"); j >= 0 {
			owner = rest[j+1:]
		}
	}
	if owner == "" || repo == "" {
		return ""
	}
	key := strings.ToLower(owner + "__" + repo)
	key = strings.ReplaceAll(key, " ", "-")
	return nonKeyChars.ReplaceAllString(key, "")
}
