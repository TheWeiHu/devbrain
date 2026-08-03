package dashboard

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPromptProfileSummaryIsBodyFreeAndFeatureComplete(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := fixedClock().Format("2006-01-02")
	seedScanLogs(t, q, day)

	s := q.PromptProfileSummary(promptProfileFilter{Days: 30, Kind: "all"})
	if s.Total != 7 || len(s.Prompts) != 7 {
		t.Fatalf("summary prompts = %d/%d, want 7", s.Total, len(s.Prompts))
	}
	if s.Counts["typed"] != 5 || s.Counts["bot"] != 2 {
		t.Fatalf("summary counts = %v, want 5 typed / 2 bot", s.Counts)
	}
	if s.MinDT == "" || s.MaxDT == "" || s.Stopwords.Base == "" {
		t.Fatalf("summary metadata incomplete: min=%q max=%q stops=%d",
			s.MinDT, s.MaxDT, len(s.Stopwords.Base))
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"how do we fix the parser?", "a model response summary that must be ignored"} {
		if bytes.Contains(b, []byte(body)) {
			t.Errorf("summary leaked full body %q", body)
		}
	}

	recs := q.ScanPrompts(30, "")
	byID := map[string]promptProfileRow{}
	for _, row := range s.Prompts {
		if len(row.ID) != 24 {
			t.Errorf("opaque id length = %d, want 24", len(row.ID))
		}
		if _, exists := byID[row.ID]; exists {
			t.Errorf("duplicate prompt id %q", row.ID)
		}
		byID[row.ID] = row
	}
	for _, p := range recs {
		row := byID[promptProfileID(p)]
		switch p.X {
		case "how do we fix the parser?":
			for _, bit := range []uint{0, 3, 7} { // question, how, fix/bug
				if row.Tone&(1<<bit) == 0 {
					t.Errorf("parser prompt missing tone bit %d: %012b", bit, row.Tone)
				}
			}
		case "/distill and then release":
			if row.Lead != "distill" {
				t.Errorf("lead skill = %q, want distill", row.Lead)
			}
		}
	}
}

func TestPromptProfileWindowKindAndWordCounts(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := fixedClock().Format("2006-01-02")
	seedScanLogs(t, q, day)

	from := time.Date(fixedClock().Year(), fixedClock().Month(), fixedClock().Day(), 9, 14, 0, 0, time.UTC).UnixMilli()
	to := time.Date(fixedClock().Year(), fixedClock().Month(), fixedClock().Day(), 9, 21, 0, 0, time.UTC).UnixMilli()
	f, err := parsePromptProfileFilter(url.Values{
		"kind": {"typed"}, "from_ms": {strconv.FormatInt(from, 10)}, "to_ms": {strconv.FormatInt(to, 10)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Days != 0 {
		t.Fatalf("timestamp-bounded filter kept default days=%d, want 0", f.Days)
	}
	s := q.PromptProfileSummary(f)
	if s.Total != 2 {
		t.Fatalf("bounded typed summary = %d, want parser + continue", s.Total)
	}
	wordResponse := q.PromptProfileWords(f)
	words := map[string]int{}
	for _, w := range wordResponse.Words {
		words[w.Word] = w.Count
	}
	if words["parser"] != 1 {
		t.Errorf("word counts = %v, want parser=1", words)
	}
	if words["continue"] != 0 {
		t.Errorf("leading command leaked into word cloud: %v", words)
	}
}

func TestPromptProfileSummaryPayloadDoesNotScaleWithPromptBody(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := fixedClock().Format("2006-01-02")
	sentinel := "private-tail-sentinel"
	longText := strings.Repeat("large agent payload evidence ", 20_000) + sentinel
	writeSession(t, q, "proj__large", day, "large", [][2]string{{"12:00", longText}})

	b, err := json.Marshal(q.PromptProfileSummary(promptProfileFilter{Days: 30, Kind: "all"}))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(sentinel)) {
		t.Fatal("summary payload includes prompt body tail")
	}
	if len(b) >= 20_000 {
		t.Fatalf("one-row summary = %d bytes; metadata payload grew with the prompt body", len(b))
	}
}

func TestPromptProfilePageCursorPreviewAndDetail(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := fixedClock().Format("2006-01-02")
	seedScanLogs(t, q, day)
	longText := "inspect " + strings.Repeat("very-long-evidence ", 40)
	writeSession(t, q, "proj__a", day, "long", [][2]string{{"11:00", longText}})
	f := promptProfileFilter{Days: 30, Kind: "all"}

	first, err := q.PromptProfilePage(f, url.Values{"page_size": {"2"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Prompts) != 2 || first.TotalSize != 8 || first.NextPageToken == "" {
		t.Fatalf("first page = %+v", first)
	}
	if first.Prompts[0].DT < first.Prompts[1].DT {
		t.Errorf("page is not newest-first: %s then %s", first.Prompts[0].DT, first.Prompts[1].DT)
	}
	if !first.Prompts[0].Truncated || utf8RuneCount(first.Prompts[0].X) != profilePreviewRunes {
		t.Errorf("long preview = truncated:%v runes:%d", first.Prompts[0].Truncated, utf8RuneCount(first.Prompts[0].X))
	}

	second, err := q.PromptProfilePage(f, url.Values{"page_size": {"2"}, "page_token": {first.NextPageToken}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Prompts) != 2 || second.Prompts[0].ID == first.Prompts[1].ID {
		t.Fatalf("second page repeated cursor item: first=%+v second=%+v", first.Prompts, second.Prompts)
	}
	if _, err := q.PromptProfilePage(f, url.Values{"page_token": {"not-a-token"}}); err == nil {
		t.Error("invalid page token succeeded")
	}
	if _, err := q.PromptProfilePage(promptProfileFilter{Days: 30, Kind: "typed"}, url.Values{
		"page_size": {"2"}, "page_token": {first.NextPageToken},
	}); err == nil {
		t.Error("page token succeeded after kind filter changed")
	}
	if _, err := q.PromptProfilePage(f, url.Values{"page_size": {"-1"}}); err == nil {
		t.Error("negative page size succeeded")
	}
	coerced, err := q.PromptProfilePage(f, url.Values{"page_size": {"1000"}})
	if err != nil || coerced.PageSize != 8 {
		t.Fatalf("max-coerced page = %+v, err=%v", coerced, err)
	}
	if got, err := profilePageSize("1000"); err != nil || got != profilePageMax {
		t.Fatalf("page_size coercion = %d, %v; want %d", got, err, profilePageMax)
	}

	detail, ok := q.PromptProfileDetail(first.Prompts[0].ID)
	if !ok || detail.X != strings.TrimSpace(longText) {
		t.Fatalf("detail did not restore full prompt: ok=%v chars=%d", ok, len(detail.X))
	}
	ids := first.Prompts[1].ID + "," + first.Prompts[0].ID
	batch, err := q.PromptProfilePage(f, url.Values{"ids": {ids}})
	if err != nil || len(batch.Prompts) != 2 || batch.Prompts[0].ID != first.Prompts[1].ID {
		t.Fatalf("id batch order = %+v, err=%v", batch.Prompts, err)
	}
}

func TestPromptProfilePageSearchAndExactWordFilter(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := fixedClock().Format("2006-01-02")
	seedScanLogs(t, q, day)
	f := promptProfileFilter{Days: 30, Kind: "all"}

	search, err := q.PromptProfilePage(f, url.Values{"q": {"response summary"}})
	if err != nil || search.TotalSize != 1 || search.Prompts[0].X != "how do we fix the parser?" {
		t.Fatalf("recap search = %+v, err=%v", search, err)
	}
	word, err := q.PromptProfilePage(f, url.Values{"word": {"parser"}})
	if err != nil || word.TotalSize != 1 || word.Prompts[0].X != "how do we fix the parser?" {
		t.Fatalf("exact word filter = %+v, err=%v", word, err)
	}
	notSubstring, err := q.PromptProfilePage(f, url.Values{"word": {"parse"}})
	if err != nil || notSubstring.TotalSize != 0 {
		t.Fatalf("word filter accepted substring = %+v, err=%v", notSubstring, err)
	}
}

func TestPromptProfileIDsDistinguishExactDuplicateRecords(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := fixedClock().Format("2006-01-02")
	writeSession(t, q, "proj__dupes", day, "same-session", [][2]string{
		{"12:00", "repeat me exactly"},
		{"12:00", "repeat me exactly"},
	})
	f := promptProfileFilter{Days: 30, Kind: "all"}
	s := q.PromptProfileSummary(f)
	if len(s.Prompts) != 2 {
		t.Fatalf("duplicate summary rows = %d, want 2", len(s.Prompts))
	}
	if s.Prompts[0].ID == s.Prompts[1].ID {
		t.Fatalf("exact duplicates share profile id %q", s.Prompts[0].ID)
	}
	for _, row := range s.Prompts {
		detail, ok := q.PromptProfileDetail(row.ID)
		if !ok || detail.ID != row.ID || detail.X != "repeat me exactly" {
			t.Fatalf("detail lookup for %q = %+v, ok=%v", row.ID, detail, ok)
		}
	}
}

func utf8RuneCount(s string) int { return len([]rune(s)) }

func TestHTTPPromptProfileEndpointsAndCompression(t *testing.T) {
	t.Parallel()
	srv, ts := newTestServer(t)
	day := fixedClock().Format("2006-01-02")
	seedScanLogs(t, srv.Q, day)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/prompts/summary?days=30&kind=all", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("summary response = %d encoding=%q", resp.StatusCode, resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	_ = zr.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(compressed) >= len(plain) {
		t.Fatalf("gzip did not reduce summary: compressed=%d plain=%d", len(compressed), len(plain))
	}
	var summary map[string]any
	if err := json.Unmarshal(plain, &summary); err != nil {
		t.Fatal(err)
	}
	rows := summary["prompts"].([]any)
	if len(rows) != 7 {
		t.Fatalf("HTTP summary rows = %d, want 7", len(rows))
	}
	if _, leaked := rows[0].(map[string]any)["x"]; leaked {
		t.Error("HTTP summary leaked x body field")
	}
	code, words := getJSON(t, ts.URL+"/api/prompts/words?days=30&kind=all")
	total, _ := words["total"].(json.Number).Int64()
	if code != http.StatusOK || total != 7 || len(words["words"].([]any)) == 0 {
		t.Fatalf("HTTP words = %d %v", code, words)
	}

	_, page := getJSON(t, ts.URL+"/api/prompts/page?days=30&kind=all&page_size=2")
	items := page["prompts"].([]any)
	if len(items) != 2 || page["next_page_token"] == "" {
		t.Fatalf("HTTP page = %v", page)
	}
	id := items[0].(map[string]any)["i"].(string)
	code, detail := getJSON(t, ts.URL+"/api/prompts/detail?id="+id)
	if code != http.StatusOK || detail["x"] == nil {
		t.Fatalf("HTTP detail = %d %v", code, detail)
	}
	code, bad := getJSON(t, ts.URL+"/api/prompts/detail?id=missing")
	if code != http.StatusNotFound || bad["error"] != "prompt not found" {
		t.Fatalf("missing detail = %d %v", code, bad)
	}
	code, bad = getJSON(t, ts.URL+"/api/prompts/summary?from_ms=2&to_ms=1")
	if code != http.StatusBadRequest || bad["error"] == nil {
		t.Fatalf("invalid profile range = %d %v", code, bad)
	}
}

func TestDashboardProfileUsesBoundedPromptEndpoints(t *testing.T) {
	t.Parallel()
	js := string(NewServer(&Queue{}).DashJS)
	if strings.Contains(js, "/api/prompts?days=0&kind=all") {
		t.Fatal("Profile bootstrap still fetches the unbounded legacy prompt corpus")
	}
	for _, endpoint := range []string{"/api/prompts/summary", "/api/prompts/words", "/api/prompts/page", "/api/prompts/detail"} {
		if !strings.Contains(js, endpoint) {
			t.Errorf("Profile asset does not use %s", endpoint)
		}
	}
}
