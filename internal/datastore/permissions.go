// Package datastore owns filesystem invariants for the private data root.
package datastore

import (
	"fmt"
	"os"
)

// EnsurePrivateRoot creates the data root when needed and removes group/other
// access from the root directory. Descendants may keep ordinary file modes;
// the non-traversable root protects the entire tree.
func EnsurePrivateRoot(path string) error {
	if path == "" {
		return fmt.Errorf("data directory is empty")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("data path %s is not a directory", path)
	}
	return os.Chmod(path, 0o700)
}
