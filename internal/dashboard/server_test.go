package dashboard

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	q := newTestQueue(t)
	seedThree(t, q)
	srv := &Server{Q: q, Dashboard: []byte("<html><body>dash</body></html>"), Port: 8799}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return srv, ts
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return resp.StatusCode, m
}

func postJSON(t *testing.T, url string, body any, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", url, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	_ = dec.Decode(&m)
	return resp.StatusCode, m
}

func TestHTTPTodosWhoamiRoot(t *testing.T) {
	t.Parallel()
	srv, ts := newTestServer(t)
	code, todos := getJSON(t, ts.URL+"/api/todos")
	if code != 200 {
		t.Fatalf("todos code = %d", code)
	}
	if _, ok := todos["tasks"]; !ok {
		t.Error("todos must carry tasks")
	}
	projects, _ := todos["projects"].([]any)
	if !reflect.DeepEqual(projects, []any{"proj__a", "proj__b"}) {
		t.Errorf("projects = %v", projects)
	}
	if statuses, _ := todos["statuses"].([]any); len(statuses) != 5 {
		t.Errorf("statuses = %v", todos["statuses"])
	}
	// whoami: identity probe — server tag, realpath'd data dir, a pid
	_, who := getJSON(t, ts.URL+"/api/whoami")
	if who["server"] != "devbrain-queue" {
		t.Errorf("whoami server = %v", who["server"])
	}
	real, _ := filepath.EvalSymlinks(srv.Q.Data)
	if who["data"] != real {
		t.Errorf("whoami data = %v, want %v", who["data"], real)
	}
	if _, err := who["pid"].(json.Number).Int64(); err != nil {
		t.Errorf("whoami pid not an int: %v", who["pid"])
	}
	// root serves the dashboard even with a ?project= query
	resp, err := http.Get(ts.URL + "/?project=proj__a")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := readAll(resp)
	if resp.StatusCode != 200 || !strings.Contains(strings.ToLower(string(body)), "<html") {
		t.Errorf("GET /?project= must serve the dashboard: %d %q", resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	// /index.html byte-equals /
	resp2, err := http.Get(ts.URL + "/index.html")
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := readAll(resp2)
	if !bytes.Equal(body, body2) {
		t.Error("/index.html must serve the same bytes as /")
	}
	// unknown path -> the exact legacy error shape
	resp3, err := http.Get(ts.URL + "/api/nope")
	if err != nil {
		t.Fatal(err)
	}
	body3, _ := readAll(resp3)
	if resp3.StatusCode != 404 || string(body3) != `{"error":"not found"}` {
		t.Errorf("404 shape = %d %q", resp3.StatusCode, body3)
	}
}

func readAll(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}

func TestHTTPSaveCreateDelete(t *testing.T) {
	t.Parallel()
	srv, ts := newTestServer(t)
	code, _ := postJSON(t, ts.URL+"/api/save", map[string]any{
		"project": "proj__b", "id": "0001-other-proj-task", "title": "x", "body": "",
		"priority": 5, "status": "taken", "reason": ""}, nil)
	if code != 200 {
		t.Fatalf("save code = %d", code)
	}
	if got := get(srv.Q, "proj__b", "0001-other-proj-task"); got.Status != "taken" {
		t.Errorf("save did not mutate: %+v", got)
	}
	code, _ = postJSON(t, ts.URL+"/api/save", map[string]any{
		"project": "proj__b", "id": "0001-other-proj-task", "title": "x", "body": "",
		"priority": 5, "status": "held", "reason": "", "approved": true}, nil)
	if code != 200 || !get(srv.Q, "proj__b", "0001-other-proj-task").Approved {
		t.Error("save with approved must set the flag")
	}
	// create + delete round-trip over HTTP
	code, created := postJSON(t, ts.URL+"/api/create", map[string]any{
		"project": "proj__a", "title": "via http", "priority": 42, "body": "b"}, nil)
	if code != 200 || created["id"] == nil {
		t.Fatalf("create = %d %v", code, created)
	}
	code, deleted := postJSON(t, ts.URL+"/api/delete", map[string]any{
		"project": "proj__a", "id": created["id"]}, nil)
	if code != 200 || deleted["ok"] != true {
		t.Errorf("delete = %d %v", code, deleted)
	}
	// a missing required key is the legacy 400 {"error": str}
	code, errBody := postJSON(t, ts.URL+"/api/save", map[string]any{"id": "x"}, nil)
	if code != 400 || errBody["error"] == nil {
		t.Errorf("missing project = %d %v, want 400 error", code, errBody)
	}
}

func TestHTTPLoopbackGuards(t *testing.T) {
	t.Parallel()
	_, ts := newTestServer(t)
	// forged Host -> 403 (DNS rebinding)
	code, body := postJSON(t, ts.URL+"/api/save", map[string]any{"project": "proj__b"},
		map[string]string{"Host": "evil.example"})
	if code != 403 || body["error"] != "forbidden" {
		t.Errorf("forged Host = %d %v, want 403 forbidden", code, body)
	}
	// forged Origin -> 403 (CSRF)
	code, body = postJSON(t, ts.URL+"/api/save", map[string]any{"project": "proj__b"},
		map[string]string{"Origin": "https://evil.example"})
	if code != 403 || body["error"] != "forbidden" {
		t.Errorf("forged Origin = %d %v, want 403 forbidden", code, body)
	}
	// loopback Origin with a port is allowed
	code, _ = postJSON(t, ts.URL+"/api/delete", map[string]any{"project": "proj__a", "id": "zzz"},
		map[string]string{"Origin": "http://localhost:3000"})
	if code != 200 {
		t.Errorf("loopback Origin = %d, want 200", code)
	}
	// GET is guarded too
	req, _ := http.NewRequest("GET", ts.URL+"/api/todos", nil)
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("forged Host GET = %d, want 403", resp.StatusCode)
	}
}

func TestHTTPPromptsParams(t *testing.T) {
	t.Parallel()
	srv, ts := newTestServer(t)
	day := fixedClock().Format("2006-01-02")
	seedScanLogs(t, srv.Q, day)
	_, api := getJSON(t, ts.URL+"/api/prompts?days=30")
	if api["kind"] != "typed" || len(api["prompts"].([]any)) != 3 {
		t.Errorf("default kind = %v with %d prompts, want typed/3", api["kind"], len(api["prompts"].([]any)))
	}
	_, all := getJSON(t, ts.URL+"/api/prompts?days=30&kind=all")
	counts := all["counts"].(map[string]any)
	if counts["typed"].(json.Number).String() != "3" || counts["bot"].(json.Number).String() != "2" {
		t.Errorf("counts = %v", counts)
	}
	if len(all["prompts"].([]any)) != 5 {
		t.Errorf("all prompts = %d, want 5", len(all["prompts"].([]any)))
	}
	_, bot := getJSON(t, ts.URL+"/api/prompts?days=30&kind=bot")
	for _, p := range bot["prompts"].([]any) {
		kind := p.(map[string]any)["kind"]
		if kind == "human" || kind == "command" {
			t.Errorf("bot filter leaked %v", kind)
		}
	}
	_, junk := getJSON(t, ts.URL+"/api/prompts?kind=evil")
	if junk["kind"] != "typed" {
		t.Errorf("bad kind -> %v, want typed", junk["kind"])
	}
	_, baddays := getJSON(t, ts.URL+"/api/prompts?days=nope&kind=all")
	if baddays["days"].(json.Number).String() != "30" {
		t.Errorf("non-digit days -> %v, want 30", baddays["days"])
	}
}

func TestHTTPGbrainTokensPricingPreferences(t *testing.T) {
	t.Parallel()
	srv, ts := newTestServer(t)
	day := fixedClock().Format("2006-01-02")
	gblog := filepath.Join(srv.Q.Data, "projects", "proj__a", "gbrain-queries.log")
	if err := os.WriteFile(gblog, []byte(`{"ts": "`+day+`T10:00:00Z", "project": "proj__a", "cmd": "gbrain search \"x\"", "modes": ["search"], "hits": 1, "slugs": ["proj__a/x"]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, gapi := getJSON(t, ts.URL+"/api/gbrain")
	if len(gapi["queries"].([]any)) != 1 {
		t.Errorf("gbrain queries = %v", gapi["queries"])
	}
	toklog := filepath.Join(srv.Q.Data, "projects", "proj__a", "tokens.jsonl")
	if err := os.WriteFile(toklog, []byte(`{"ts": "`+day+`T10:00:00Z", "session": "s1", "model": "m", "in": 1, "out": 2, "cache_create": 0, "cache_read": 0}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, tapi := getJSON(t, ts.URL+"/api/tokens")
	if len(tapi["usage"].([]any)) != 1 {
		t.Errorf("tokens usage = %v", tapi["usage"])
	}
	_, papi := getJSON(t, ts.URL+"/api/pricing")
	if papi["models"] == nil || papi["tiers"] == nil || papi["default"] == nil {
		t.Errorf("pricing shape = %v", papi)
	}

	// preferences: GET absent -> POST -> GET present -> non-string 400
	_, pref0 := getJSON(t, ts.URL+"/api/preferences")
	if pref0["exists"] != false || pref0["content"] != "" {
		t.Errorf("absent preferences = %v", pref0)
	}
	if p, _ := pref0["path"].(string); !strings.HasSuffix(p, "/preferences/global.md") {
		t.Errorf("preferences path = %v", pref0["path"])
	}
	code, saved := postJSON(t, ts.URL+"/api/preferences",
		map[string]any{"content": "# Prefs\n\n- No warm colors.\n"}, nil)
	if code != 200 || saved["ok"] != true || saved["bytes"].(json.Number).String() != strconv.Itoa(len("# Prefs\n\n- No warm colors.\n")) {
		t.Errorf("preferences POST = %d %v", code, saved)
	}
	onDisk, _ := os.ReadFile(filepath.Join(srv.Q.Data, "preferences", "global.md"))
	if string(onDisk) != "# Prefs\n\n- No warm colors.\n" {
		t.Errorf("preferences file = %q", onDisk)
	}
	_, pref1 := getJSON(t, ts.URL+"/api/preferences")
	if pref1["exists"] != true || !strings.Contains(pref1["content"].(string), "No warm colors") {
		t.Errorf("present preferences = %v", pref1)
	}
	code, badresp := postJSON(t, ts.URL+"/api/preferences", map[string]any{"content": 5}, nil)
	if code != 400 || badresp["error"] != "content must be a string" {
		t.Errorf("non-string content = %d %v", code, badresp)
	}
}

func countEntries(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, "## ") && len(ln) > 3 && ln[3] >= '0' && ln[3] <= '9' {
			n++
		}
	}
	return n
}

// The n=0 unified-diff ledger appended to preferences/edits.md: additions,
// removals, no context lines, timestamped `· you` headers, no no-op entries.
func TestPreferencesEditLedger(t *testing.T) {
	t.Parallel()
	srv, ts := newTestServer(t)
	post := func(content string) {
		code, _ := postJSON(t, ts.URL+"/api/preferences", map[string]any{"content": content}, nil)
		if code != 200 {
			t.Fatalf("preferences POST = %d", code)
		}
	}
	post("# Prefs\n\n- No warm colors.\n")
	histf := filepath.Join(srv.Q.Data, "preferences", "edits.md")
	hist, err := os.ReadFile(histf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hist), "· you") || !strings.Contains(string(hist), "+- No warm colors.") {
		t.Errorf("first save must log additions:\n%s", hist)
	}
	// the injected LOCAL-time stamp, isoformat(timespec="seconds")
	if !strings.Contains(string(hist), "## 2026-07-02T12:30:45 · you") {
		t.Errorf("ledger header must use the injected clock:\n%s", hist)
	}
	// a save that removes a bullet and adds another records BOTH
	post("# Prefs\n\n- Prefer teal accents.\n")
	hist, _ = os.ReadFile(histf)
	if !strings.Contains(string(hist), "-- No warm colors.") {
		t.Errorf("removal (- line) missing:\n%s", hist)
	}
	if !strings.Contains(string(hist), "+- Prefer teal accents.") {
		t.Errorf("addition (+ line) missing:\n%s", hist)
	}
	// context-free (n=0): the unchanged "# Prefs" line must NOT appear
	for _, ln := range strings.Split(string(hist), "\n") {
		if ln == " # Prefs" {
			t.Error("diff must be context-free (no unchanged lines)")
		}
	}
	// an identical re-save changes nothing -> no new entry
	n := countEntries(string(hist))
	post("# Prefs\n\n- Prefer teal accents.\n")
	hist, _ = os.ReadFile(histf)
	if countEntries(string(hist)) != n {
		t.Error("a no-op save must log nothing")
	}
}

func TestEditLedgerDiff(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		old, new []string
		want     []string
	}{
		{"no change", []string{"a", "b"}, []string{"a", "b"}, nil},
		{"pure add", []string{"a"}, []string{"a", "b"}, []string{"+b"}},
		{"pure delete", []string{"a", "b"}, []string{"a"}, []string{"-b"}},
		{"replace keeps -then+ order", []string{"x", "mid", "y"}, []string{"x", "MID", "y"},
			[]string{"-mid", "+MID"}},
		{"two hunks in order", []string{"a", "b", "c", "d"}, []string{"A", "b", "c", "D"},
			[]string{"-a", "+A", "-d", "+D"}},
		{"empty old", nil, []string{"a"}, []string{"+a"}},
		{"empty new", []string{"a"}, nil, []string{"-a"}},
	}
	for _, c := range cases {
		if got := editLedgerDiff(c.old, c.new); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: editLedgerDiff = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNightshiftStartEndpoint(t *testing.T) {
	t.Parallel()
	srv, ts := newTestServer(t)
	srv.Port = 8123
	checkout := filepath.Join(srv.Q.Data, "checkout-a")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	seedInteractiveLog(t, srv.Q, "proj__a", checkout)
	var spawnedEnv []string
	srv.Q.Running = func(string) bool { return false }
	srv.Q.EnsureClone = func(c string) (string, string) { return c, "stub" }
	srv.Q.Spawn = func(argv, env []string) error { spawnedEnv = env; return nil }
	// a failed start is a 422 with the error payload
	code, body := postJSON(t, ts.URL+"/api/nightshift/start",
		map[string]any{"project": "proj__a", "ids": []string{"bogus"}}, nil)
	if code != 422 || body["error"] == nil {
		t.Errorf("failed start = %d %v, want 422 error", code, body)
	}
	code, body = postJSON(t, ts.URL+"/api/nightshift/start",
		map[string]any{"project": "proj__a", "ids": []string{"0081-foo"}}, nil)
	if code != 200 || body["ok"] != true {
		t.Fatalf("start = %d %v", code, body)
	}
	found := false
	for _, e := range spawnedEnv {
		if e == "DEVBRAIN_QUEUE_PORT=8123" {
			found = true
		}
	}
	if !found {
		t.Errorf("bound port must reach the fleet env: %v", spawnedEnv)
	}
}

func TestIsDevbrainQueueAndSelectPort(t *testing.T) {
	t.Parallel()
	_, ts := newTestServer(t)
	addr := ts.Listener.Addr().(*net.TCPAddr)
	if !IsDevbrainQueue(addr.Port) {
		t.Error("live queue server must probe true")
	}
	// a listener that is NOT a queue probes false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer other.Close()
	if IsDevbrainQueue(other.Listener.Addr().(*net.TCPAddr).Port) {
		t.Error("foreign server must probe false")
	}

	// SelectPort: pure control flow, I/O injected (no real sockets)
	bound := &net.TCPListener{} // sentinel
	fakeBind := func(taken map[int]bool) func(int) net.Listener {
		return func(p int) net.Listener {
			if taken[p] {
				return nil
			}
			return bound
		}
	}
	never := func(int) bool { return false }
	if kind, ln, port := SelectPort(9000, 20, fakeBind(nil), never); kind != "serve" || ln != net.Listener(bound) || port != 9000 {
		t.Errorf("free start port: %v %v %v", kind, ln, port)
	}
	if kind, _, port := SelectPort(9000, 20, fakeBind(map[int]bool{9000: true, 9001: true}), never); kind != "serve" || port != 9002 {
		t.Errorf("step past busy: %v %v", kind, port)
	}
	if kind, ln, port := SelectPort(9000, 20, fakeBind(map[int]bool{9000: true}), func(p int) bool { return p == 9000 }); kind != "reuse" || ln != nil || port != 9000 {
		t.Errorf("reuse a live queue: %v %v %v", kind, ln, port)
	}
	if kind, ln, port := SelectPort(9000, 3, fakeBind(map[int]bool{9000: true, 9001: true, 9002: true}), never); kind != "none" || ln != nil || port != 0 {
		t.Errorf("all busy: %v %v %v", kind, ln, port)
	}
}
