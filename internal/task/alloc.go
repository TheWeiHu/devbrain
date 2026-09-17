package task

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"syscall"
)

// LockName is the advisory-lock file AllocID takes inside a todo dir. It is a
// dotfile so the `*.md` listings and the bash-glob-style scans never see it,
// and the data repo's .gitignore (written by install) keeps it out of git.
const LockName = ".alloc.lock"

// AllocID mints the next `NNNN-<slug>` task id under dir and reserves it by
// creating an empty `<id>.md`. It is the ONE allocator for the queue (the todo
// CLI and the dashboard both mint through it), and the NNNN prefix — not the
// full file name — is the unit of exclusivity: the scan of the current max
// and the create run under an exclusive flock on dir/.alloc.lock, so two
// parallel `todo add` calls with different slugs can no longer both read the
// same max and both win their O_EXCL create (which is how one queue ended up
// with two files each for 0240, 0252, 0281 … in production). The O_EXCL create
// stays as belt-and-braces against a writer that bypasses the lock.
//
// Ids are counted across dir AND dir/archive so a moved-away top id can't be
// reissued; only canonical `NNNN-*.md` names take part in the scan, so a stray
// `12-short.md` or a non-task file never shifts the sequence.
func AllocID(dir, slug string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	lock, err := os.OpenFile(filepath.Join(dir, LockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return "", fmt.Errorf("lock %s: %w", lock.Name(), err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck // Close releases it anyway

	seq := 0
	for _, d := range []string{dir, filepath.Join(dir, "archive")} {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			name := e.Name()
			if ok, _ := path.Match("[0-9][0-9][0-9][0-9]-*.md", name); !ok {
				continue
			}
			if n, err := strconv.Atoi(name[:4]); err == nil && n > seq {
				seq = n
			}
		}
	}
	for {
		seq++
		id := fmt.Sprintf("%04d-%s", seq, slug)
		f, err := os.OpenFile(filepath.Join(dir, id+".md"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return id, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
}
