package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The edit ledger must say who wrote the page: only the dashboard editor is the
// owner's hand ("· you"); any other API caller is an agent ("· agent"), so
// /distill never mistakes agent wording for the owner's protected lines.
func TestPreferencesEditSourceLabel(t *testing.T) {
	srv, ts := newTestServer(t)
	ledger := filepath.Join(srv.Q.Data, "preferences", "edits.md")
	page := filepath.Join(srv.Q.Data, "preferences", "global.md")

	if code, resp := postJSON(t, ts.URL+"/api/preferences", map[string]any{"content": "# P\n\n- one\n"}, nil); code != 200 {
		t.Fatalf("unlabelled POST = %d %v", code, resp)
	}
	b, _ := os.ReadFile(ledger)
	if !strings.Contains(string(b), " · agent\n") || strings.Contains(string(b), " · you\n") {
		t.Errorf("a write without a source must be recorded as · agent, got:\n%s", b)
	}

	if code, resp := postJSON(t, ts.URL+"/api/preferences", map[string]any{"content": "# P\n\n- two\n", "source": "you"}, nil); code != 200 {
		t.Fatalf("source=you POST = %d %v", code, resp)
	}
	b, _ = os.ReadFile(ledger)
	if !strings.Contains(string(b), " · you\n") {
		t.Errorf("the editor's write (source=you) must be recorded as · you, got:\n%s", b)
	}

	code, resp := postJSON(t, ts.URL+"/api/preferences", map[string]any{"content": "# P\n\n- three\n", "source": "distill"}, nil)
	if code != 400 || !strings.Contains(resp["error"].(string), "source") {
		t.Errorf("unknown source must be rejected with 400 naming source, got %d %v", code, resp)
	}
	if onDisk, _ := os.ReadFile(page); string(onDisk) != "# P\n\n- two\n" {
		t.Errorf("rejected write must not touch the page, got %q", onDisk)
	}
}
