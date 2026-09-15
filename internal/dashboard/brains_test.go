package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func multiBrainServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	a, b := newTestQueue(t), newTestQueue(t)
	for _, q := range []*Queue{a, b} {
		seedThree(t, q)
		seedScanLogs(t, q, "2026-07-02")
	}
	s := NewServer(a)
	s.Brains = []BrainQueue{{Name: "default", Q: a}, {Name: "work", Q: b}}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, ts
}

func TestBrainsAggregateAndFilter(t *testing.T) {
	t.Parallel()
	s, ts := multiBrainServer(t)
	for i, b := range s.Brains {
		for file, content := range map[string]string{
			"tokens.jsonl":       fmt.Sprintf(`{"ts":"2026-07-02T10:00:00Z","session":"same","turn":"1","model":"m","in":%d,"out":2}`+"\n", i+1),
			"gbrain-queries.log": `{"ts":"2026-07-02T10:00:00Z","project":"proj__a","cmd":"gbrain search x","modes":["search"],"hits":1,"slugs":["proj__a/x"]}` + "\n",
		} {
			if err := os.WriteFile(filepath.Join(b.Q.Data, "projects", "proj__a", file), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		path, key string
		each      int
	}{
		{"/api/todos?", "tasks", 3}, {"/api/prompts?days=0&kind=all", "prompts", 7},
		{"/api/tokens?", "usage", 1}, {"/api/gbrain?", "queries", 1}, {"/api/nightshift?", "runs", 0},
	} {
		t.Run(tc.key, func(t *testing.T) {
			code, all := getJSON(t, ts.URL+tc.path)
			if code != 200 || len(all[tc.key].([]any)) != 2*tc.each {
				t.Fatalf("all = %d %v", code, all)
			}
			for _, name := range []string{"default", "work"} {
				code, one := getJSON(t, ts.URL+tc.path+"&brain="+name)
				rows := one[tc.key].([]any)
				if code != 200 || len(rows) != tc.each {
					t.Fatalf("selected = %d %v", code, one)
				}
				for _, row := range rows {
					if row.(map[string]any)["brain"] != name {
						t.Fatalf("wrong source: %v", row)
					}
				}
			}
			code, none := getJSON(t, ts.URL+tc.path+"&brain=")
			if code != 200 || len(none[tc.key].([]any)) != 0 {
				t.Fatalf("none = %d %v", code, none)
			}
			if code, _ := getJSON(t, ts.URL+tc.path+"&brain=unknown"); code != 400 {
				t.Fatalf("unknown = %d", code)
			}
		})
	}
	_, todos := getJSON(t, ts.URL+"/api/todos")
	if len(todos["projects"].([]any)) != 2 || len(todos["project_sources"].([]any)) != 4 {
		t.Fatalf("duplicate project sources lost: %v", todos)
	}
}

func TestBrainsMutationIsolation(t *testing.T) {
	t.Parallel()
	s, ts := multiBrainServer(t)
	body := map[string]any{"project": "proj__b", "id": "0001-other-proj-task", "title": "changed", "body": "", "priority": 5, "status": "taken"}
	for _, name := range []any{nil, "", "unknown", []string{"default", "work"}} {
		body["brain"] = name
		if code, _ := postJSON(t, ts.URL+"/api/save", body, nil); code != 400 {
			t.Fatalf("invalid brain %v: %d", name, code)
		}
	}
	body["brain"] = "work"
	if code, _ := postJSON(t, ts.URL+"/api/save?brain=default", body, nil); code != 400 {
		t.Fatalf("conflicting brain: %d", code)
	}
	if code, _ := postJSON(t, ts.URL+"/api/save", body, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Fatalf("origin guard: %d", code)
	}
	if code, result := postJSON(t, ts.URL+"/api/save", body, nil); code != 200 {
		t.Fatalf("save: %d %v", code, result)
	}
	if get(s.Brains[0].Q, "proj__b", "0001-other-proj-task").Status != "open" || get(s.Brains[1].Q, "proj__b", "0001-other-proj-task").Status != "taken" {
		t.Fatal("save crossed brain boundary")
	}
	if code, _ := postJSON(t, ts.URL+"/api/delete", body, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if get(s.Brains[0].Q, "proj__b", "0001-other-proj-task") == nil || get(s.Brains[1].Q, "proj__b", "0001-other-proj-task") != nil {
		t.Fatal("delete crossed brain boundary")
	}
	body["title"] = "unique created task"
	if code, _ := postJSON(t, ts.URL+"/api/create", body, nil); code != 200 {
		t.Fatalf("create: %d", code)
	}
	for i, b := range s.Brains {
		found := false
		for _, task := range b.Q.AllTasks() {
			found = found || task.Title == "unique created task"
		}
		if found != (i == 1) {
			t.Fatal("create crossed brain boundary")
		}
	}
	for _, query := range []string{"", "?brain=", "?brain=work&brain=default"} {
		if code, _ := getJSON(t, ts.URL+"/api/preferences"+query); code != 400 {
			t.Fatalf("ambiguous preferences %q: %d", query, code)
		}
	}
	if code, _ := postJSON(t, ts.URL+"/api/preferences", map[string]any{"brain": "work", "content": "Work preferences"}, nil); code != 200 {
		t.Fatalf("preferences: %d", code)
	}
	for _, b := range s.Brains {
		_, prefs := getJSON(t, ts.URL+"/api/preferences?brain="+b.Name)
		want := ""
		if b.Name == "work" {
			want = "Work preferences"
		}
		if strings.TrimSpace(prefs["content"].(string)) != want {
			t.Fatalf("preferences crossed boundary: %v", prefs)
		}
	}
}

func TestBrainsUnavailableAndIdentity(t *testing.T) {
	t.Parallel()
	s, ts := multiBrainServer(t)
	port, _ := strconv.Atoi(strings.Split(ts.URL, ":")[2])
	if !matchesDashboard(port, s.sources()) || matchesDashboard(port, s.sources()[:1]) {
		t.Fatal("multi-brain identity mismatch")
	}
	s.Brains[1].Q.Data = filepath.Join(t.TempDir(), "missing")
	_, catalog := getJSON(t, ts.URL+"/api/brains")
	if catalog["brains"].([]any)[1].(map[string]any)["available"] != false {
		t.Fatalf("missing brain hidden: %v", catalog)
	}
	if code, _ := getJSON(t, ts.URL+"/api/todos"); code != 400 {
		t.Fatalf("partial data returned: %d", code)
	}
	if code, _ := getJSON(t, ts.URL+"/api/todos?brain=default"); code != 200 {
		t.Fatalf("available selection failed: %d", code)
	}
	if code, _ := postJSON(t, ts.URL+"/api/create", map[string]any{"brain": "work", "project": "proj__a", "title": "x"}, nil); code != 400 {
		t.Fatalf("unavailable write allowed: %d", code)
	}
	single, one := newTestServer(t)
	port, _ = strconv.Atoi(strings.Split(one.URL, ":")[2])
	if !matchesDashboard(port, single.sources()) || matchesDashboard(port, s.sources()) {
		t.Fatal("legacy identity mismatch")
	}
}

func TestBrainsLaunchUsesSourceData(t *testing.T) {
	t.Parallel()
	s, ts := multiBrainServer(t)
	var spawned []string
	for _, b := range s.Brains {
		q := b.Q
		checkout := filepath.Join(q.Data, "checkout")
		if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0700); err != nil {
			t.Fatal(err)
		}
		seedRemote(t, q, "proj__a", "https://github.com/proj/a.git")
		q.Running = func(string) bool { return false }
		q.EnsureClone = func(string) (string, string) { return checkout, "stub" }
		q.Spawn = func(argv, env []string) error { spawned = env; return nil }
	}
	code, result := postJSON(t, ts.URL+"/api/nightshift/start", map[string]any{"brain": "work", "project": "proj__a", "ids": []string{"0001-alpha-task"}}, nil)
	if code != 200 {
		t.Fatalf("start: %d %v", code, result)
	}
	if !strings.Contains(strings.Join(spawned, "\n"), "DEVBRAIN_DATA="+s.Brains[1].Q.Data) || result["repo"] != filepath.Join(s.Brains[1].Q.Data, "checkout") {
		t.Fatalf("wrong launch destination: %v %v", spawned, result)
	}
}

func TestRunDashboardSelection(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("DEVBRAIN_DATA", "")
	t.Setenv("DEVBRAIN_BRAIN", "")
	s, ts := multiBrainServer(t)
	s.Brains[0].Q.Data = filepath.Join(t.TempDir(), "offline")
	data := s.Brains[1].Q.Data
	if err := os.MkdirAll(filepath.Join(data, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]any{"data": s.Brains[0].Q.Data, "brains": map[string]string{"work": data}})
	if err := os.MkdirAll(filepath.Join(cfgDir, "devbrain"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "devbrain", "config.json"), cfg, 0600); err != nil {
		t.Fatal(err)
	}
	port := strings.Split(ts.URL, ":")[2]
	var output bytes.Buffer
	if code := Run([]string{"--port", port, "--no-open"}, &output, &output); code != 0 || !strings.Contains(output.String(), ts.URL) {
		t.Fatalf("catalog with offline default: %d %s", code, output.String())
	}
	one := httptest.NewServer(NewServer(s.Brains[1].Q))
	defer one.Close()
	port = strings.Split(one.URL, ":")[2]
	for _, mode := range []string{"data", "brain", "env"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{"--port", port, "--no-open"}
			switch mode {
			case "data":
				args = append(args, "--data", data)
			case "brain":
				t.Setenv("DEVBRAIN_BRAIN", "work")
			case "env":
				t.Setenv("DEVBRAIN_DATA", data)
			}
			output.Reset()
			if code := Run(args, &output, &output); code != 0 || !strings.Contains(output.String(), one.URL) {
				t.Fatalf("explicit selection: %d %s", code, output.String())
			}
		})
	}
}
