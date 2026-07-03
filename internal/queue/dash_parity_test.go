package queue_test

// Go-native port of scripts/test-dash-parity.sh: launches `devbrain queue` on
// the dashboard fixture, fetches every /api/* endpoint, normalizes the JSON
// (sorted keys, <PID>/<DATA> placeholders) and diffs against
// testdata/golden/api/*.json. GET / must byte-equal assets/dashboard.html.

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// dashEndpoint pairs an endpoint name with its URL path.
type dashEndpoint struct {
	name string
	path string
}

var dashEndpoints = []dashEndpoint{
	{"todos", "/api/todos"},
	{"prompts-all", "/api/prompts?days=0&kind=all"},
	{"prompts-typed", "/api/prompts?days=0&kind=typed"},
	{"prompts-bot", "/api/prompts?days=0&kind=bot"},
	{"gbrain", "/api/gbrain?days=0"},
	{"tokens", "/api/tokens?days=0"},
	{"pricing", "/api/pricing"},
	{"nightshift", "/api/nightshift"},
	{"preferences", "/api/preferences"},
	{"whoami", "/api/whoami"},
}

// dashNorm recursively normalizes a decoded JSON value the same way the bash
// script's norm.py does: replace any string value containing the data dir
// with <DATA>, replace any "pid" key value with "<PID>".
func dashNorm(v any, dataDir, realDataDir string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if k == "pid" {
				out[k] = "<PID>"
			} else {
				out[k] = dashNorm(val, dataDir, realDataDir)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = dashNorm(val, dataDir, realDataDir)
		}
		return out
	case string:
		s := x
		// Mirror Python: replace os.path.realpath(data_dir) first, then data_dir.
		if realDataDir != "" && realDataDir != dataDir {
			s = strings.ReplaceAll(s, realDataDir, "<DATA>")
		}
		s = strings.ReplaceAll(s, dataDir, "<DATA>")
		return s
	default:
		// json.Number, bool, nil — pass through unchanged.
		return v
	}
}

// dashMarshalSorted marshals v to JSON with sorted keys and indent=1 (one space),
// matching Python's json.dump(..., indent=1, sort_keys=True, ensure_ascii=False).
func dashMarshalSorted(v any) ([]byte, error) {
	return dashMarshalDepth(v, 0)
}

func dashMarshalDepth(v any, depth int) ([]byte, error) {
	indent := strings.Repeat(" ", depth+1)
	closeIndent := strings.Repeat(" ", depth)
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			return []byte("{}"), nil
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		sb.WriteByte('{')
		for i, k := range keys {
			sb.WriteByte('\n')
			sb.WriteString(indent)
			kb, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			sb.Write(kb)
			sb.WriteString(": ")
			vb, err := dashMarshalDepth(x[k], depth+1)
			if err != nil {
				return nil, err
			}
			sb.Write(vb)
			if i < len(keys)-1 {
				sb.WriteByte(',')
			}
		}
		sb.WriteByte('\n')
		sb.WriteString(closeIndent)
		sb.WriteByte('}')
		return []byte(sb.String()), nil
	case []any:
		if len(x) == 0 {
			return []byte("[]"), nil
		}
		var sb strings.Builder
		sb.WriteByte('[')
		for i, elem := range x {
			sb.WriteByte('\n')
			sb.WriteString(indent)
			vb, err := dashMarshalDepth(elem, depth+1)
			if err != nil {
				return nil, err
			}
			sb.Write(vb)
			if i < len(x)-1 {
				sb.WriteByte(',')
			}
		}
		sb.WriteByte('\n')
		sb.WriteString(closeIndent)
		sb.WriteByte(']')
		return []byte(sb.String()), nil
	case json.Number:
		// Preserve the number as-is (avoid float64 precision loss).
		return []byte(x.String()), nil
	case bool:
		if x {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case nil:
		return []byte("null"), nil
	case string:
		// Python's json.dump with ensure_ascii=False does NOT HTML-escape < > &.
		// Go's json.Marshal does. Use a no-escape encoder.
		var buf strings.Builder
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(x); err != nil {
			return nil, err
		}
		// Encode appends a newline; strip it.
		return []byte(strings.TrimRight(buf.String(), "\n")), nil
	default:
		return json.Marshal(v)
	}
}

// dashFetchBytes fetches the URL and returns the raw response body.
func dashFetchBytes(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body %s: %v", url, err)
	}
	return b
}

// dashNormalizeJSON fetches JSON, normalizes it, and returns bytes with trailing newline.
func dashNormalizeJSON(t *testing.T, url, dataDir, realDataDir string) []byte {
	t.Helper()
	raw := dashFetchBytes(t, url)
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode JSON from %s: %v\nbody: %s", url, err, raw)
	}
	normed := dashNorm(v, dataDir, realDataDir)
	b, err := dashMarshalSorted(normed)
	if err != nil {
		t.Fatalf("marshal normalized JSON for %s: %v", url, err)
	}
	return append(b, '\n')
}

// dashWaitUp polls /api/whoami until it responds AND the data path matches,
// mirroring the bash wait_up function (50 retries × 100 ms = 5 s).
func dashWaitUp(t *testing.T, base, dataDir, realDataDir string) {
	t.Helper()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for i := 0; i < 50; i++ {
		resp, err := client.Get(base + "/api/whoami")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			s := string(body)
			if strings.Contains(s, realDataDir) || strings.Contains(s, dataDir) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("devbrain queue did not come up within 5 seconds")
}

// dashFreePort grabs a free TCP port and returns it.
func dashFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port, nil
}

// dashDiff returns a compact line-diff string for failure messages.
func dashDiff(want, got string) string {
	wLines := strings.Split(want, "\n")
	gLines := strings.Split(got, "\n")
	var sb strings.Builder
	max := len(wLines)
	if len(gLines) > max {
		max = len(gLines)
	}
	for i := 0; i < max; i++ {
		var w, g string
		if i < len(wLines) {
			w = wLines[i]
		}
		if i < len(gLines) {
			g = gLines[i]
		}
		if w != g {
			fmt.Fprintf(&sb, "line %d:\n  want: %q\n  got:  %q\n", i+1, w, g)
		}
	}
	return sb.String()
}

// dashCopyDir recursively copies src into dst.
func dashCopyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return dashCopyFile(path, target, info.Mode())
	})
	if err != nil {
		t.Fatalf("dashCopyDir %s -> %s: %v", src, dst, err)
	}
}

func dashCopyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func TestDashboardParity(t *testing.T) {
	root := clitest.Root(t)

	// Skip if fixture or golden dirs are absent (mirrors the bash script's skip).
	fixtureDir := filepath.Join(root, "testdata", "dashboard-fixture")
	goldenDir := filepath.Join(root, "testdata", "golden", "api")
	if _, err := os.Stat(fixtureDir); os.IsNotExist(err) {
		t.Skip("testdata/dashboard-fixture absent — skipping dash parity test")
	}
	if _, err := os.Stat(goldenDir); os.IsNotExist(err) {
		t.Skip("testdata/golden/api absent — skipping dash parity test")
	}

	bin := clitest.Bin(t)

	// Build the data dir from the fixture, mirroring the bash setup.
	dataDir := t.TempDir()
	dashCopyDir(t, fixtureDir, dataDir)

	// Write nightshift-run.json pointing at our nightshift-repo inside the data dir.
	nightshiftRepo := filepath.Join(dataDir, "nightshift-repo")
	if err := os.MkdirAll(filepath.Join(nightshiftRepo, ".nightshift"), 0o755); err != nil {
		t.Fatalf("mkdir nightshift: %v", err)
	}
	nightshiftRunJSON := fmt.Sprintf(`{"port": 0, "repo": %q}`, nightshiftRepo)
	if err := os.WriteFile(
		filepath.Join(dataDir, "projects", "fix__demo", "nightshift-run.json"),
		[]byte(nightshiftRunJSON+"\n"), 0o644,
	); err != nil {
		t.Fatalf("write nightshift-run.json: %v", err)
	}
	// Copy nightshift-status.json into the repo's .nightshift dir.
	statusSrc := filepath.Join(fixtureDir, "nightshift-status.json")
	if b, err := os.ReadFile(statusSrc); err != nil {
		t.Fatalf("read nightshift-status.json: %v", err)
	} else if err := os.WriteFile(filepath.Join(nightshiftRepo, ".nightshift", "status.json"), b, 0o644); err != nil {
		t.Fatalf("write status.json: %v", err)
	}

	// Resolve symlinks on the data dir (macOS /tmp -> /private/tmp).
	realDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		realDataDir = dataDir
	}

	// Grab a free port, then start the server on it.
	port, err := dashFreePort()
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}

	cmd := exec.Command(bin, "queue",
		"--port", fmt.Sprint(port),
		"--no-open",
		"--data", dataDir,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start devbrain queue: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	dashWaitUp(t, base, dataDir, realDataDir)

	// Assert each /api/* endpoint normalizes to the golden file.
	for _, ep := range dashEndpoints {
		ep := ep
		t.Run(ep.name, func(t *testing.T) {
			got := dashNormalizeJSON(t, base+ep.path, dataDir, realDataDir)
			goldenPath := filepath.Join(goldenDir, ep.name+".json")
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v", goldenPath, err)
			}
			if string(got) != string(want) {
				t.Errorf("mismatch for %s\n--- golden\n+++ got\n%s",
					ep.name, dashDiff(string(want), string(got)))
			}
		})
	}

	// GET / must byte-equal assets/dashboard.html.
	t.Run("GET /", func(t *testing.T) {
		got := dashFetchBytes(t, base+"/")
		wantPath := filepath.Join(root, "assets", "dashboard.html")
		want, err := os.ReadFile(wantPath)
		if err != nil {
			t.Fatalf("read assets/dashboard.html: %v", err)
		}
		if string(got) != string(want) {
			t.Errorf("GET / differs from assets/dashboard.html (%d vs %d bytes)", len(got), len(want))
		}
	})
}
