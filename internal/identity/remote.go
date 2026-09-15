// Package identity parses repository names without consulting data configuration.
package identity

import (
	"regexp"
	"strings"
)

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
