// Package taskstore provides the cross-process serialization and atomic file
// replacement shared by the TODO CLI and dashboard writers.
package taskstore

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var localLocks sync.Map

// Revision returns the stable content token used for optimistic concurrency.
func Revision(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

// Lock serializes every task mutation for one data-repo project. The in-process
// mutex covers goroutines because flock semantics differ across Unix variants;
// flock covers the dashboard, workers, and orchestrator in separate processes.
type Lock struct {
	file  *os.File
	local *sync.Mutex
}

func Acquire(data, project string) (*Lock, error) {
	path, err := lockPath(data, project)
	if err != nil {
		return nil, err
	}
	muValue, _ := localLocks.LoadOrStore(path, &sync.Mutex{})
	mu := muValue.(*sync.Mutex)
	mu.Lock()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		mu.Unlock()
		return nil, err
	}
	return &Lock{file: f, local: mu}, nil
}

func (l *Lock) Close() error {
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	l.local.Unlock()
	return err
}

func lockPath(data, project string) (string, error) {
	root := filepath.Join(data, ".git", "devbrain-locks")
	if fi, err := os.Stat(filepath.Join(data, ".git")); err != nil || !fi.IsDir() {
		cache, cacheErr := os.UserCacheDir()
		if cacheErr != nil {
			cache = os.TempDir()
		}
		root = filepath.Join(cache, "devbrain", "locks")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(filepath.Clean(data) + "\x00" + project))
	return filepath.Join(root, fmt.Sprintf("todo-%x.lock", key[:16])), nil
}

// AtomicWrite replaces path only after the complete new file has been flushed.
func AtomicWrite(path string, content []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".devbrain-write-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// AtomicCreate publishes a complete file without replacing an existing path.
// The hard link is the create-if-absent compare-and-swap shared with other
// allocators that may not use the project lock.
func AtomicCreate(path string, content []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".devbrain-create-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmpName, path)
}
