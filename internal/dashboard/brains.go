package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/task"
)

type BrainQueue struct {
	Name string
	Q    *Queue
}

type namedPrompt struct {
	*Prompt
	Brain string `json:"brain"`
}
type namedToken struct {
	*TokenRec
	Brain string `json:"brain"`
}
type namedQuery struct {
	*GBQuery
	Brain string `json:"brain"`
}
type namedTask struct {
	*task.Task
	Brain string `json:"brain"`
}
type projectSource struct {
	Project string `json:"project"`
	Brain   string `json:"brain"`
}

func (s *Server) sources() []BrainQueue {
	if len(s.Brains) > 0 {
		return s.Brains
	}
	return []BrainQueue{{Name: "default", Q: s.Q}}
}

func (b BrainQueue) available() error {
	st, err := os.Stat(b.Q.projectsDir())
	if err != nil || !st.IsDir() {
		return fmt.Errorf("brain %q is unavailable; deselect it to view the other brains", b.Name)
	}
	return nil
}

func (s *Server) selected(q url.Values) ([]BrainQueue, error) {
	names, explicit := q["brain"]
	want := map[string]bool{}
	for _, name := range names {
		if name != "" {
			want[name] = true
		}
	}
	out := []BrainQueue{}
	for _, b := range s.sources() {
		if explicit && !want[b.Name] {
			continue
		}
		delete(want, b.Name)
		if err := b.available(); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if len(want) > 0 {
		return nil, fmt.Errorf("unknown brain selection")
	}
	return out, nil
}

func (s *Server) serveBrains(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/api/brains" && !(len(s.Brains) > 1 && strings.HasPrefix(path, "/api/")) {
		return false
	}
	if !s.loopback(r) {
		s.sendJSON(w, 403, map[string]string{"error": "forbidden"})
		return true
	}
	if r.Method == http.MethodGet && path == "/api/brains" {
		rows := []map[string]any{}
		for _, b := range s.sources() {
			rows = append(rows, map[string]any{"name": b.Name, "available": b.available() == nil})
		}
		s.sendJSON(w, 200, map[string]any{"brains": rows})
		return true
	}
	if path == "/api/whoami" && r.Method == http.MethodGet {
		brains := []config.Brain{}
		for _, b := range s.sources() {
			brains = append(brains, config.Brain{Name: b.Name, Data: b.Q.Data})
		}
		s.sendJSON(w, 200, map[string]any{"server": "devbrain-queue", "data": s.Q.Data, "pid": os.Getpid(), "brains": brains})
		return true
	}
	if r.Method == http.MethodPost || (r.Method == http.MethodGet && (path == "/api/preferences" || path == "/api/nightshift/resolve")) {
		return s.serveOneBrain(w, r)
	}
	if r.Method != http.MethodGet {
		return false
	}
	switch path {
	case "/api/todos", "/api/prompts", "/api/tokens", "/api/gbrain", "/api/nightshift":
	default:
		return false
	}
	sources, err := s.selected(r.URL.Query())
	if err != nil {
		s.sendJSON(w, 400, map[string]string{"error": err.Error()})
		return true
	}
	qs := r.URL.Query()
	switch path {
	case "/api/todos":
		projects, tasks, origins := []string{}, []namedTask{}, []projectSource{}
		seen := map[string]bool{}
		for _, b := range sources {
			for _, p := range b.Q.Projects() {
				origins = append(origins, projectSource{Project: p, Brain: b.Name})
				if !seen[p] {
					projects = append(projects, p)
					seen[p] = true
				}
			}
			for _, t := range b.Q.AllTasks() {
				tasks = append(tasks, namedTask{Task: t, Brain: b.Name})
			}
		}
		sort.Strings(projects)
		s.sendJSON(w, 200, map[string]any{"projects": projects, "tasks": tasks, "statuses": task.Statuses, "project_sources": origins})
	case "/api/prompts":
		days, kind := pyDays(qs.Get("days"), 30), qs.Get("kind")
		if kind != "typed" && kind != "bot" && kind != "all" {
			kind = "typed"
		}
		rows, typed, total := []namedPrompt{}, 0, 0
		for _, b := range sources {
			recs := b.Q.ScanPrompts(days, qs.Get("project"))
			total += len(recs)
			for _, p := range recs {
				if typedKinds[p.Kind] {
					typed++
				}
			}
			for _, p := range FilterKind(recs, kind) {
				rows = append(rows, namedPrompt{Prompt: p, Brain: b.Name})
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].DT < rows[j].DT })
		s.sendJSON(w, 200, map[string]any{"prompts": rows, "days": days, "kind": kind, "counts": map[string]int{"typed": typed, "bot": total - typed}})
	case "/api/tokens":
		rows := []namedToken{}
		for _, b := range sources {
			for _, x := range b.Q.TokenUsage(pyDays(qs.Get("days"), 0), qs.Get("project")) {
				rows = append(rows, namedToken{TokenRec: x, Brain: b.Name})
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].TS < rows[j].TS })
		s.sendJSON(w, 200, map[string]any{"usage": rows})
	case "/api/gbrain":
		rows := []namedQuery{}
		for _, b := range sources {
			for _, x := range b.Q.GBrainQueries(pyDays(qs.Get("days"), 0), qs.Get("project")) {
				rows = append(rows, namedQuery{GBQuery: x, Brain: b.Name})
			}
		}
		s.sendJSON(w, 200, map[string]any{"queries": rows})
	case "/api/nightshift":
		runs := []any{}
		for _, b := range sources {
			for _, x := range b.Q.Nightshift()["runs"].([]any) {
				row := x.(map[string]any)
				row["brain"] = b.Name
				runs = append(runs, row)
			}
		}
		s.sendJSON(w, 200, map[string]any{"runs": runs})
	}
	return true
}

func matchesDashboard(port int, sources []BrainQueue) bool {
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/whoami", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var who struct {
		Server, Data string
		Brains       []config.Brain
	}
	if json.NewDecoder(resp.Body).Decode(&who) != nil || who.Server != "devbrain-queue" {
		return false
	}
	if len(who.Brains) == 0 {
		return len(sources) == 1 && config.DataID(who.Data) == config.DataID(sources[0].Q.Data)
	}
	if len(who.Brains) != len(sources) {
		return false
	}
	for _, b := range sources {
		found := false
		for _, other := range who.Brains {
			if b.Name == other.Name && config.DataID(b.Q.Data) == config.DataID(other.Data) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *Server) serveOneBrain(w http.ResponseWriter, r *http.Request) bool {
	qs := r.URL.Query()
	if r.Method == http.MethodPost {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			s.postErr(w, err)
			return true
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var body map[string]any
		if err = json.Unmarshal(raw, &body); err != nil {
			s.postErr(w, err)
			return true
		}
		name, ok := body["brain"].(string)
		if !ok || name == "" {
			s.postErr(w, fmt.Errorf("select a brain for this edit"))
			return true
		}
		if values, exists := qs["brain"]; exists && (len(values) != 1 || values[0] != name) {
			s.postErr(w, fmt.Errorf("conflicting brain selection"))
			return true
		}
		qs.Set("brain", name)
	}
	if len(qs["brain"]) != 1 || qs.Get("brain") == "" {
		s.postErr(w, fmt.Errorf("select one brain"))
		return true
	}
	sources, err := s.selected(qs)
	if err != nil {
		s.postErr(w, err)
		return true
	}
	if len(sources) != 1 {
		s.postErr(w, fmt.Errorf("select one brain"))
		return true
	}
	one := *s
	one.Q = sources[0].Q
	one.Brains = nil
	request := r.Clone(r.Context())
	u := *r.URL
	request.URL = &u
	qs.Del("brain")
	request.URL.RawQuery = qs.Encode()
	request.RequestURI = request.URL.RequestURI()
	if r.Method == http.MethodPost {
		one.doPOST(w, request)
	} else {
		one.doGET(w, request)
	}
	return true
}
