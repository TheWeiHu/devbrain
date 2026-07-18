package taskstore

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAcquireSerializesGoroutines(t *testing.T) {
	data := t.TempDir()
	var wg sync.WaitGroup
	entered := make(chan int, 2)
	release := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lock, err := Acquire(data, "proj__a")
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			defer lock.Close()
			entered <- i
			<-release
		}(i)
	}
	<-entered
	select {
	case <-entered:
		t.Fatal("second goroutine entered while the project lock was held")
	default:
	}
	release <- struct{}{}
	<-entered
	release <- struct{}{}
	wg.Wait()
}

func TestAtomicWriteReplacesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new" {
		t.Fatalf("content = %q, want new", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", fi.Mode().Perm())
	}
}

func TestAtomicCreateDoesNotReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.md")
	if err := AtomicCreate(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicCreate(path, []byte("second"), 0o644); !os.IsExist(err) {
		t.Fatalf("second AtomicCreate error = %v, want exists", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "first" {
		t.Fatalf("content = %q, want first", b)
	}
}
