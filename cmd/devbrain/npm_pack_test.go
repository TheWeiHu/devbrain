package main

// Go-native port of scripts/test-npm-pack.sh: the npm forwarder package packs
// correctly. Skips when npm or node are absent.

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNpmPack(t *testing.T) {
	npmSkipIfUnavailable(t)

	root := npmRepoRoot(t)
	tmp := t.TempDir()

	// 1. Build the real tarball (local, no publish, no lifecycle scripts).
	packLog := filepath.Join(tmp, "pack.log")
	packCmd := exec.Command("npm", "pack", root, "--pack-destination", tmp)
	packOut, err := packCmd.CombinedOutput()
	if err != nil {
		if wErr := os.WriteFile(packLog, packOut, 0o644); wErr != nil {
			t.Logf("writing pack.log: %v", wErr)
		}
		t.Fatalf("npm pack failed:\n%s", packOut)
	}

	// Find the produced .tgz.
	tgz := npmFindTgz(t, tmp)
	if tgz == "" {
		t.Fatal("npm pack produced a tarball: no .tgz found in", tmp)
	}

	t.Run("npm pack produced a tarball", func(t *testing.T) {
		if _, err := os.Stat(tgz); err != nil {
			t.Errorf("tarball not found: %v", err)
		}
	})

	// 2. It ships the forwarder and nothing else.
	fileList := npmTarList(t, tgz)

	t.Run("ships bin/devbrain.js", func(t *testing.T) {
		if !npmListContains(fileList, "bin/devbrain.js") {
			t.Errorf("bin/devbrain.js not found in tarball; contents:\n%s", strings.Join(fileList, "\n"))
		}
	})

	t.Run("ships no scripts or hooks", func(t *testing.T) {
		for _, f := range fileList {
			if strings.HasPrefix(f, "scripts/") || strings.HasPrefix(f, "hooks/") {
				t.Errorf("unexpected path in tarball: %s", f)
			}
		}
	})

	// Extract the tarball to inspect files.
	extracted := filepath.Join(tmp, "extracted")
	if err := os.MkdirAll(extracted, 0o755); err != nil {
		t.Fatal(err)
	}
	npmExtractTgz(t, tgz, extracted)
	jsPath := filepath.Join(extracted, "package", "bin", "devbrain.js")

	t.Run("bin/devbrain.js executable", func(t *testing.T) {
		info, err := os.Stat(jsPath)
		if err != nil {
			t.Fatalf("bin/devbrain.js not found after extract: %v", err)
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("bin/devbrain.js is not executable (mode %s)", info.Mode())
		}
	})

	// 3. No binary on PATH: `help` prints install instructions and exits 0.
	nodePath := npmFindNode(t)
	minPath := filepath.Dir(nodePath) + ":/usr/bin:/bin"

	t.Run("help exits 0", func(t *testing.T) {
		cmd := exec.Command("node", jsPath, "help")
		cmd.Env = append(os.Environ(), "PATH="+minPath)
		out, _ := cmd.CombinedOutput()
		if cmd.ProcessState == nil {
			t.Fatal("node did not start")
		}
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("help exit code = %d, want 0; output:\n%s", code, out)
		}
	})

	t.Run("help prints install channel", func(t *testing.T) {
		cmd := exec.Command("node", jsPath, "help")
		cmd.Env = append(os.Environ(), "PATH="+minPath)
		out, _ := cmd.CombinedOutput()
		if !strings.Contains(string(out), "brew install TheWeiHu/devbrain/devbrain") {
			t.Errorf("help output does not contain brew install line:\n%s", out)
		}
	})

	// 4. With a stub `devbrain` on PATH, argv is forwarded verbatim.
	stubDir := filepath.Join(tmp, "stubbin")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argvFile := filepath.Join(tmp, "argv.txt")
	// The stub writes each argument on its own line, matching the bash script.
	stubScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argvFile + "\"\n"
	stubPath := filepath.Join(stubDir, "devbrain")
	if err := os.WriteFile(stubPath, []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("forwards to devbrain on PATH", func(t *testing.T) {
		forwardPath := stubDir + ":" + filepath.Dir(nodePath) + ":/usr/bin:/bin"
		cmd := exec.Command("node", jsPath, "todo", "next")
		cmd.Env = append(os.Environ(), "PATH="+forwardPath)
		cmd.Run() //nolint:errcheck // exit code may be non-zero from stub
		got, err := os.ReadFile(argvFile)
		if err != nil {
			t.Fatalf("argv.txt not written: %v", err)
		}
		want := "todo\nnext"
		if strings.TrimRight(string(got), "\n") != want {
			t.Errorf("forwarded argv = %q, want %q", strings.TrimRight(string(got), "\n"), want)
		}
	})
}

// npmSkipIfUnavailable skips if npm or node are not on PATH.
func npmSkipIfUnavailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("skip: npm not available")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("skip: node not available")
	}
}

// npmRepoRoot walks up from cwd to find go.mod.
func npmRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from cwd")
		}
		dir = parent
	}
}

// npmFindTgz returns the first .tgz file in dir.
func npmFindTgz(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tgz") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// npmTarList returns the entry paths inside a .tgz, stripping the leading "package/" prefix.
func npmTarList(t *testing.T, tgzPath string) []string {
	t.Helper()
	f, err := os.Open(tgzPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(hdr.Name, "package/")
		names = append(names, name)
	}
	return names
}

// npmListContains reports whether the file list contains the exact path.
func npmListContains(list []string, path string) bool {
	for _, f := range list {
		if f == path {
			return true
		}
	}
	return false
}

// npmExtractTgz extracts a .tgz archive into dest.
func npmExtractTgz(t *testing.T, tgzPath, dest string) {
	t.Helper()
	f, err := os.Open(tgzPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dest, filepath.FromSlash(hdr.Name))
		// Guard against path traversal.
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
			t.Fatalf("tar: suspicious path %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			mode := hdr.FileInfo().Mode()
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				t.Fatal(err)
			}
			out.Close()
		}
	}
}

// npmFindNode returns the path to the node binary.
func npmFindNode(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("node")
	if err != nil {
		t.Skip("skip: node not available")
	}
	return p
}
