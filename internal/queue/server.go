// The HTTP surface of `devbrain queue`: route matching, loopback guards and
// response shapes are a quirk-for-quirk port of the legacy BaseHTTPRequestHandler
// (exact match on some /api paths, prefix match on others, raw-path matching
// including the query string, Cache-Control: no-store on everything).
package queue

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TheWeiHu/devbrain/assets"
	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/pricing"
	"github.com/TheWeiHu/devbrain/internal/task"
)

// Server binds one Queue to the HTTP handler. Port is the port actually
// bound — passed to a dashboard-launched nightshift run.
type Server struct {
	Q         *Queue
	Dashboard []byte
	Port      int
}

// NewServer wires the embedded dashboard to a queue.
func NewServer(q *Queue) *Server {
	return &Server{Q: q, Dashboard: assets.DashboardHTML, Port: 8799}
}

// loopbackHost reports whether a Host/Origin value names a loopback host,
// mirroring the legacy parse: strip scheme, strip one trailing :port,
// unbracket, lowercase.
func loopbackHost(v string) bool {
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	if i := strings.LastIndex(v, ":"); i >= 0 {
		v = v[:i]
	}
	v = strings.ToLower(strings.Trim(v, "[]"))
	return v == "127.0.0.1" || v == "localhost" || v == "::1"
}

// loopback: bound to 127.0.0.1; also require a loopback Host (DNS-rebinding)
// and Origin (CSRF).
func (s *Server) loopback(r *http.Request) bool {
	if !loopbackHost(r.Host) {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		return loopbackHost(o)
	}
	return true
}

func (s *Server) send(w http.ResponseWriter, code int, body []byte, ctype string) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

func (s *Server) sendJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(`{"error":"marshal"}`)
	}
	s.send(w, code, b, "application/json")
}

// ServeHTTP implements the legacy do_GET/do_POST routing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.doGET(w, r)
	case http.MethodPost:
		s.doPOST(w, r)
	default:
		// BaseHTTPRequestHandler: 501 for unimplemented methods
		http.Error(w, "Unsupported method", http.StatusNotImplemented)
	}
}

// rawQuery is parse_qs over the raw path's query: blank values dropped,
// first value wins.
func rawQuery(rawPath string) url.Values {
	q := url.Values{}
	if i := strings.Index(rawPath, "?"); i >= 0 {
		parsed, _ := url.ParseQuery(rawPath[i+1:])
		for k, vs := range parsed {
			for _, v := range vs {
				if v != "" { // parse_qs drops blank values
					q.Add(k, v)
				}
			}
		}
	}
	return q
}

// pyDays is the legacy days param handling: int if str.isdigit(), else def.
func pyDays(raw string, def int) int {
	if raw == "" {
		return def
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return def
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func (s *Server) doGET(w http.ResponseWriter, r *http.Request) {
	if !s.loopback(r) {
		s.send(w, 403, []byte(`{"error":"forbidden"}`), "application/json")
		return
	}
	raw := r.RequestURI // the legacy handler matches the RAW path (query included)
	pathOnly := raw
	if i := strings.Index(pathOnly, "?"); i >= 0 {
		pathOnly = pathOnly[:i]
	}
	if pathOnly == "/" || pathOnly == "/index.html" { // ignore ?project=… (client-side only)
		s.send(w, 200, s.Dashboard, "text/html; charset=utf-8")
		return
	}
	switch {
	case raw == "/api/whoami": // identity probe so `nightshift watch` can spot a foreign queue
		real, err := filepath.EvalSymlinks(s.Q.Data)
		if err != nil {
			real = s.Q.Data
		}
		s.sendJSON(w, 200, map[string]any{"server": "devbrain-queue", "data": real, "pid": os.Getpid()})
	case raw == "/api/todos":
		s.sendJSON(w, 200, map[string]any{"projects": s.Q.Projects(),
			"statuses": task.Statuses, "tasks": s.Q.AllTasks()})
	case strings.HasPrefix(raw, "/api/nightshift/resolve"): // where would a launch run + is one going?
		qs := rawQuery(raw)
		proj := qs.Get("project")
		var checkout string
		if proj != "" {
			checkout = s.Q.ProjectRepo(proj)
		}
		var repo string
		if checkout != "" {
			repo = s.Q.NightshiftClonePath(checkout)
			if repo == "" {
				repo = checkout
			}
		}
		exists := false
		if repo != "" {
			_, err := os.Stat(filepath.Join(repo, ".git"))
			exists = err == nil
		}
		var repoJSON any // null when unresolved, like the legacy None
		if repo != "" {
			repoJSON = repo
		}
		s.sendJSON(w, 200, map[string]any{"repo": repoJSON, "cloned": exists,
			"running": exists && s.Q.Running(repo)})
	case strings.HasPrefix(raw, "/api/nightshift"):
		s.sendJSON(w, 200, s.Q.Nightshift())
	case strings.HasPrefix(raw, "/api/prompts"):
		qs := rawQuery(raw)
		days := pyDays(qs.Get("days"), 30)
		proj := qs.Get("project")
		kind := qs.Get("kind")
		if kind != "typed" && kind != "bot" && kind != "all" {
			kind = "typed"
		}
		recs := s.Q.ScanPrompts(days, proj)
		typed := 0
		for _, rec := range recs {
			if typedKinds[rec.Kind] {
				typed++
			}
		}
		s.sendJSON(w, 200, map[string]any{"prompts": FilterKind(recs, kind), "days": days,
			"kind": kind, "counts": map[string]int{"typed": typed, "bot": len(recs) - typed}})
	case strings.HasPrefix(raw, "/api/gbrain"):
		qs := rawQuery(raw)
		s.sendJSON(w, 200, map[string]any{"queries": s.Q.GBrainQueries(pyDays(qs.Get("days"), 0), "")})
	case strings.HasPrefix(raw, "/api/tokens"):
		qs := rawQuery(raw)
		s.sendJSON(w, 200, map[string]any{"usage": s.Q.TokenUsage(pyDays(qs.Get("days"), 0), "")})
	case raw == "/api/pricing": // the ONE pricing table — no JS copy to drift
		s.sendJSON(w, 200, pricing.APIPayload())
	case raw == "/api/preferences":
		// The global preferences page /distill maintains and Claude Code @imports.
		p := filepath.Join(s.Q.Data, "preferences", "global.md")
		content, exists := "", false
		if b, err := os.ReadFile(p); err == nil {
			content, exists = string(b), true
		}
		s.sendJSON(w, 200, map[string]any{"path": p, "content": content, "exists": exists})
	default:
		s.send(w, 404, []byte(`{"error":"not found"}`), "application/json")
	}
}

// postErr mirrors the legacy do_POST catch-all: any exception becomes a 400
// with {"error": str(e)}.
func (s *Server) postErr(w http.ResponseWriter, err error) {
	s.sendJSON(w, 400, map[string]any{"error": err.Error()})
}

// getKey is d["project"]-style access: a missing key raises (KeyError -> 400).
func getKey(d map[string]any, k string) (any, error) {
	v, ok := d[k]
	if !ok {
		return nil, errors.New("'" + k + "'")
	}
	return v, nil
}

func getStr(d map[string]any, k, def string) string {
	if v, ok := d[k].(string); ok {
		return v
	}
	return def
}

// pyInt is Python int(v) over a decoded JSON value: numbers truncate toward
// zero, digit strings parse, everything else raises.
func pyInt(v any) (int, error) {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return int(n), nil
		}
		if f, err := x.Float64(); err == nil {
			return int(f), nil // truncation toward zero
		}
		return 0, errors.New("invalid literal for int(): " + string(x))
	case string:
		n, err := strconv.Atoi(pyStrip(x))
		if err != nil {
			return 0, errors.New("invalid literal for int() with base 10: '" + x + "'")
		}
		return n, nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	}
	return 0, errors.New("int() argument must be a number, not its JSON type")
}

func (s *Server) doPOST(w http.ResponseWriter, r *http.Request) {
	if !s.loopback(r) {
		s.send(w, 403, []byte(`{"error":"forbidden"}`), "application/json")
		return
	}
	raw := r.RequestURI
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.postErr(w, err)
		return
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	d, err := decodeJSONMap(string(body))
	if err != nil {
		s.postErr(w, err)
		return
	}
	switch {
	case raw == "/api/save":
		project, err := getKey(d, "project")
		if err != nil {
			s.postErr(w, err)
			return
		}
		id, err := getKey(d, "id")
		if err != nil {
			s.postErr(w, err)
			return
		}
		prioRaw, ok := d["priority"]
		if !ok {
			prioRaw = json.Number("0")
		}
		prio, err := pyInt(prioRaw)
		if err != nil {
			s.postErr(w, err)
			return
		}
		if prio < 0 {
			prio = 0
		}
		if prio > 100 {
			prio = 100
		}
		u := &Updates{}
		if st, ok := d["status"].(string); ok {
			u.Set("status", strp(st))
		} else {
			u.Set("status", nil)
		}
		u.Set("priority", strp(strconv.Itoa(prio)))
		if reason, ok := d["reason"].(string); ok && reason != "" {
			u.Set("reason", strp(reason))
		} else {
			u.Set("reason", nil) // write() handles done_at from status
		}
		if pyTruthy(d["approved"]) {
			u.Set("approved", strp("true")) // greenlight unattended pickup
		} else {
			u.Set("approved", nil)
		}
		t, err := s.Q.Write(fmt.Sprint(project), fmt.Sprint(id), u,
			getStr(d, "title", ""), getStr(d, "body", ""))
		if err != nil {
			s.postErr(w, err)
			return
		}
		s.sendJSON(w, 200, t)
	case raw == "/api/create":
		project, err := getKey(d, "project")
		if err != nil {
			s.postErr(w, err)
			return
		}
		prioRaw, ok := d["priority"]
		if !ok {
			prioRaw = json.Number("0")
		}
		var prio int
		if !pyTruthy(prioRaw) { // int(priority or 0)
			prio = 0
		} else if prio, err = pyInt(prioRaw); err != nil {
			s.postErr(w, err)
			return
		}
		t, err := s.Q.Create(fmt.Sprint(project), getStr(d, "title", ""), prio, getStr(d, "body", ""))
		if err != nil {
			s.postErr(w, err)
			return
		}
		s.sendJSON(w, 200, t)
	case raw == "/api/delete":
		project, err := getKey(d, "project")
		if err != nil {
			s.postErr(w, err)
			return
		}
		id, err := getKey(d, "id")
		if err != nil {
			s.postErr(w, err)
			return
		}
		s.sendJSON(w, 200, map[string]any{"ok": s.Q.Delete(fmt.Sprint(project), fmt.Sprint(id))})
	case raw == "/api/preferences":
		// Write the global preferences page back (the Profile tab editor).
		contentAny, hasContent := d["content"]
		if !hasContent {
			contentAny = ""
		}
		content, isStr := contentAny.(string)
		if !isStr {
			s.sendJSON(w, 400, map[string]any{"error": "content must be a string"})
			return
		}
		pdir := filepath.Join(s.Q.Data, "preferences")
		if err := os.MkdirAll(pdir, 0o755); err != nil {
			s.postErr(w, err)
			return
		}
		gp := filepath.Join(pdir, "global.md")
		old := ""
		if b, err := os.ReadFile(gp); err == nil {
			old = string(b)
		}
		if err := os.WriteFile(gp, []byte(content), 0o644); err != nil {
			s.postErr(w, err)
			return
		}
		// Record the DIFF of this hand-edit in preferences/edits.md so
		// /distill can SEE what was added and removed. n=0: every line in the
		// entry is a real change; nothing unchanged to mistake for an edit.
		if diffBody := editLedgerDiff(splitPyLines(old), splitPyLines(content)); len(diffBody) > 0 {
			ts := s.Q.Now().Format("2006-01-02T15:04:05") // local time, seconds
			entry := "## " + ts + " · you\n\n```diff\n" + strings.Join(diffBody, "\n") + "\n```\n\n"
			f, err := os.OpenFile(filepath.Join(pdir, "edits.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				s.postErr(w, err)
				return
			}
			_, werr := f.WriteString(entry)
			f.Close()
			if werr != nil {
				s.postErr(w, werr)
				return
			}
		}
		s.sendJSON(w, 200, map[string]any{"ok": true, "bytes": len(content)})
	case raw == "/api/nightshift/start":
		project, err := getKey(d, "project")
		if err != nil {
			s.postErr(w, err)
			return
		}
		var ids []string
		if l, ok := d["ids"].([]any); ok {
			for _, v := range l {
				ids = append(ids, pyStr(v))
			}
		}
		res := s.Q.StartNightshift(fmt.Sprint(project), ids, s.Port)
		code := 422
		if pyTruthy(res["ok"]) {
			code = 200
		}
		s.sendJSON(w, code, res)
	case raw == "/api/nightshift/stop":
		project, err := getKey(d, "project")
		if err != nil {
			s.postErr(w, err)
			return
		}
		res := s.Q.StopNightshift(fmt.Sprint(project))
		code := 422
		if pyTruthy(res["ok"]) {
			code = 200
		}
		s.sendJSON(w, code, res)
	default:
		s.send(w, 404, []byte(`{"error":"not found"}`), "application/json")
	}
}

// pyStr renders a decoded JSON value like Python str() for the id shapes
// start_nightshift stringifies.
func pyStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return string(x)
	case bool:
		if x {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	}
	return fmt.Sprint(v)
}

// --- port selection / process entrypoint -----------------------------------------

// IsDevbrainQueue probes /api/todos on a loopback port for the queue's shape,
// so a second `devbrain queue` reuses a live server instead of erroring.
func IsDevbrainQueue(port int) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/api/todos")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	head := make([]byte, 4096)
	n, _ := io.ReadFull(resp.Body, head)
	return strings.Contains(string(head[:n]), `"statuses"`)
}

// SelectPort picks where to serve, never crashing on a busy port. Walk ports
// from start:
//   - ("serve", ln, port)  first port we could bind (use it);
//   - ("reuse", nil, port) a busy port already hosting a devbrain queue;
//   - ("none", nil, 0)     start..start+tries-1 all busy with something else.
//
// I/O is injected so it unit-tests without real sockets.
func SelectPort(start, tries int, tryBind func(int) net.Listener, isReusable func(int) bool) (string, net.Listener, int) {
	for port := start; port < start+tries; port++ {
		if ln := tryBind(port); ln != nil {
			return "serve", ln, port
		}
		if isReusable(port) {
			return "reuse", nil, port
		}
	}
	return "none", nil, 0
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", url)
	} else {
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// Run is the `devbrain queue` verb: parse flags, resolve the data repo,
// bind (or reuse) a port and serve until interrupted.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("devbrain queue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	// 8799: uncommon local-HTTP port, off the crowded dev clusters;
	// SelectPort walks 8799->8818 on collision.
	port := fs.Int("port", 8799, "port to serve on (walks forward if busy)")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	data := fs.String("data", "", "devbrain data dir (default: $DEVBRAIN_DATA or ~/devbrain-data)")
	fs.Usage = func() {
		fmt.Fprint(stderr, "devbrain queue — localhost TODO-queue kanban\n\n"+
			"usage: devbrain queue [--port N] [--no-open] [--data DIR]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	dataDir := *data
	if dataDir == "" {
		dataDir = config.DataDir()
	}
	abs, err := filepath.Abs(dataDir)
	if err == nil {
		dataDir = abs
	}
	q := New(dataDir)
	srv := NewServer(q)
	if fi, err := os.Stat(q.projectsDir()); err != nil || !fi.IsDir() {
		fmt.Fprintf(stderr, "devbrain queue: no projects dir at %s\n", q.projectsDir())
		return 1
	}
	tryBind := func(p int) net.Listener {
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) {
				return nil
			}
			fmt.Fprintf(stderr, "devbrain queue: %v\n", err)
			os.Exit(1)
		}
		return ln
	}
	kind, ln, got := SelectPort(*port, 20, tryBind, IsDevbrainQueue)
	if kind == "none" {
		fmt.Fprintf(stderr, "devbrain queue: no free port in %d–%d\n", *port, *port+19)
		return 1
	}
	srv.Port = got // a dashboard-launched nightshift run advertises THIS port
	url := fmt.Sprintf("http://127.0.0.1:%d/", got)
	if kind == "reuse" {
		fmt.Fprintf(stdout, "devbrain queue already running → %s  (opening it)\n", url)
		if !*noOpen {
			openBrowser(url)
		}
		return 0
	}
	if got != *port {
		fmt.Fprintf(stdout, "devbrain queue: port %d busy — using %d\n", *port, got)
	}
	fmt.Fprintf(stdout, "devbrain queue → %s  (Ctrl-C to stop)\n", url)
	if !*noOpen {
		openBrowser(url)
	}
	if err := http.Serve(ln, srv); err != nil {
		fmt.Fprintf(stderr, "devbrain queue: %v\n", err)
		return 1
	}
	return 0
}
