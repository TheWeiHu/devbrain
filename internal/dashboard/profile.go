package dashboard

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	profilePageDefault  = 50
	profilePageMax      = 100
	profilePreviewRunes = 360
	profileRecapRunes   = 240
	profileToneBytes    = 8192
)

// prompt-stopwords.json is the single vocabulary used by the server-side
// prompt cloud and the dashboard's gbrain cloud. Keeping it outside the JS
// prevents the two views from drifting when the profile bootstrap stops
// carrying complete prompt bodies.
//
//go:embed prompt-stopwords.json
var promptStopwordsJSON []byte

type promptStopwordConfig struct {
	Base string `json:"base"`
	Deny string `json:"deny"`
	Keep string `json:"keep"`
}

var (
	profileStopsOnce sync.Once
	profileStopsCfg  promptStopwordConfig
	profileStops     map[string]bool
	profileWordRE    = regexp.MustCompile(`[a-z][a-z'+\-]{2,}`)
	profileLeadRE    = regexp.MustCompile(`^[/\$]([a-z][a-z0-9-]*)(?:\s|$)`)
)

func loadProfileStops() (promptStopwordConfig, map[string]bool) {
	profileStopsOnce.Do(func() {
		if err := json.Unmarshal(promptStopwordsJSON, &profileStopsCfg); err != nil {
			panic("dashboard: embedded prompt-stopwords.json is invalid: " + err.Error())
		}
		profileStops = map[string]bool{}
		for _, w := range strings.Fields(profileStopsCfg.Base + " " + profileStopsCfg.Deny) {
			profileStops[w] = true
		}
		for _, w := range strings.Fields(profileStopsCfg.Keep) {
			delete(profileStops, w)
		}
	})
	return profileStopsCfg, profileStops
}

func writeProfileHashString(h hash.Hash, s string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(s)))
	_, _ = h.Write(size[:])
	_, _ = h.Write([]byte(s))
}

func promptProfileFingerprint(p *Prompt) [sha256.Size]byte {
	h := sha256.New()
	for _, s := range []string{p.P, p.S, p.DT, p.Kind, p.X, p.Recap} {
		writeProfileHashString(h, s)
	}
	for _, skill := range p.Skills {
		writeProfileHashString(h, skill)
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func profileIDFromFingerprint(base [sha256.Size]byte, occurrence uint64) string {
	h := sha256.New()
	_, _ = h.Write(base[:])
	var ordinal [8]byte
	binary.BigEndian.PutUint64(ordinal[:], occurrence)
	_, _ = h.Write(ordinal[:])
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// assignPromptProfileIDs makes exact duplicate records addressable without
// exposing a source path or row number. Stable sort order gives each duplicate
// a deterministic occurrence ordinal; unique records always use ordinal zero.
func assignPromptProfileIDs(recs []*Prompt) {
	seen := make(map[[sha256.Size]byte]uint64, len(recs))
	for _, p := range recs {
		base := promptProfileFingerprint(p)
		occurrence := seen[base]
		seen[base] = occurrence + 1
		p.profileID = profileIDFromFingerprint(base, occurrence)
	}
}

func promptProfileID(p *Prompt) string {
	if p.profileID != "" {
		return p.profileID
	}
	return profileIDFromFingerprint(promptProfileFingerprint(p), 0)
}

func promptLeadSkill(text string) string {
	m := profileLeadRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(text)))
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func promptToneFlags(text string) uint16 {
	// Tone is an opener-level analytic. Bound the scan so a multi-megabyte agent
	// payload cannot make an all-history chart proportional to body volume.
	if len(text) > profileToneBytes {
		text = text[:profileToneBytes]
	}
	lower := strings.ToLower(strings.TrimSpace(text))
	var flags uint16
	if strings.Contains(text, "?") {
		flags |= 1
	}
	// The old browser implementation ran twelve regular expressions over every
	// full body. A single ASCII token pass preserves the same word-boundary
	// semantics while keeping all-history summaries proportional to capped input.
	for i := 0; i < len(lower); {
		for i < len(lower) && !profileWordByte(lower[i]) {
			i++
		}
		start := i
		for i < len(lower) && profileWordByte(lower[i]) {
			i++
		}
		if start == i {
			continue
		}
		switch lower[start:i] {
		case "ship", "commit", "push", "pr", "merge", "deploy":
			flags |= 1 << 1
		case "how":
			flags |= 1 << 3
		case "test", "verify", "check", "confirm":
			flags |= 1 << 4
		case "wait", "actually", "revert", "undo":
			flags |= 1 << 5
		case "just", "simple", "quick", "minimal", "small":
			flags |= 1 << 6
		case "fix", "bug", "broken", "error", "fail", "wrong", "issue":
			flags |= 1 << 7
		case "instead", "better", "idea", "approach", "design":
			flags |= 1 << 8
		case "why":
			flags |= 1 << 9
		case "great", "perfect", "nice", "lovely", "awesome", "excellent", "beautiful", "cool":
			flags |= 1 << 10
		case "damn", "shit", "fuck", "wtf", "hell", "crap", "ugh", "annoying":
			flags |= 1 << 11
		}
	}
	if promptOpening(lower, "yes", "yup", "yep", "ok", "okay", "sure", "cool", "nice", "good", "do it", "go ahead") {
		flags |= 1 << 2
	}
	if promptOpening(lower, "no") || strings.Contains(lower, "not what i") || strings.Contains(lower, "that's wrong") {
		flags |= 1 << 5
	}
	if strings.Contains(lower, "make sure") {
		flags |= 1 << 4
	}
	if strings.Contains(lower, "should we") || strings.Contains(lower, "what if") {
		flags |= 1 << 8
	}
	return flags
}

func profileWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_'
}

func promptOpening(lower string, options ...string) bool {
	for _, option := range options {
		if lower == option || strings.HasPrefix(lower, option) &&
			(len(lower) == len(option) || !profileWordByte(lower[len(option)])) {
			return true
		}
	}
	return false
}

func promptUnixMilli(p *Prompt) int64 {
	t, err := time.ParseInLocation("2006-01-02T15:04:05", p.DT, time.UTC)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

type promptProfileFilter struct {
	Days           int
	Kind, Project  string
	FromMS, ToMS   int64
	HasFrom, HasTo bool
}

func parsePromptProfileFilter(qs url.Values) (promptProfileFilter, error) {
	f := promptProfileFilter{Days: pyDays(qs.Get("days"), 30), Kind: qs.Get("kind"), Project: qs.Get("project")}
	if f.Kind != "typed" && f.Kind != "bot" && f.Kind != "all" {
		f.Kind = "typed"
	}
	parseBound := func(name string) (int64, bool, error) {
		raw := qs.Get(name)
		if raw == "" {
			return 0, false, nil
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, false, errors.New(name + " must be Unix milliseconds")
		}
		return n, true, nil
	}
	var err error
	if f.FromMS, f.HasFrom, err = parseBound("from_ms"); err != nil {
		return f, err
	}
	if f.ToMS, f.HasTo, err = parseBound("to_ms"); err != nil {
		return f, err
	}
	if f.HasFrom && f.HasTo && f.FromMS > f.ToMS {
		return f, errors.New("from_ms must be at or before to_ms")
	}
	// Explicit timestamp bounds own the window. Without this, the default 30-day
	// cutoff would silently clip a custom or all-history date selection.
	if (f.HasFrom || f.HasTo) && qs.Get("days") == "" {
		f.Days = 0
	}
	return f, nil
}

func promptInProfileWindow(p *Prompt, f promptProfileFilter, cutoff string) bool {
	if cutoff != "0000-00-00" && p.Date < cutoff {
		return false
	}
	if f.HasFrom || f.HasTo {
		ms := promptUnixMilli(p)
		if (f.HasFrom && ms < f.FromMS) || (f.HasTo && ms > f.ToMS) {
			return false
		}
	}
	return true
}

func promptMatchesKind(p *Prompt, kind string) bool {
	if kind == "all" {
		return true
	}
	typed := typedKinds[p.Kind]
	return (kind == "typed" && typed) || (kind == "bot" && !typed)
}

type promptProfileRow struct {
	ID     string   `json:"i"`
	P      string   `json:"p"`
	S      string   `json:"s,omitempty"`
	DT     string   `json:"dt"`
	Chars  int      `json:"c"`
	Words  int      `json:"w"`
	Kind   string   `json:"kind"`
	Skills []string `json:"sk,omitempty"`
	Lead   string   `json:"ls,omitempty"`
	Tone   uint16   `json:"f,omitempty"`
}

type promptProfileWord struct {
	Word  string `json:"word"`
	Count int    `json:"count"`
}

type promptProfileSummary struct {
	Prompts   []promptProfileRow   `json:"prompts"`
	Counts    map[string]int       `json:"counts"`
	Days      int                  `json:"days"`
	Kind      string               `json:"kind"`
	Total     int                  `json:"total"`
	MinDT     string               `json:"min_dt"`
	MaxDT     string               `json:"max_dt"`
	Stopwords promptStopwordConfig `json:"stopwords"`
}

type promptWordsResponse struct {
	Words []promptProfileWord `json:"words"`
	Total int                 `json:"total"`
}

func promptProfileWords(recs []*Prompt, limit int) []promptProfileWord {
	_, stops := loadProfileStops()
	counts := map[string]int{}
	for _, p := range recs {
		text := strings.ToLower(strings.TrimSpace(p.X))
		if p.Kind == "command" {
			if loc := profileLeadRE.FindStringIndex(text); loc != nil && loc[0] == 0 {
				text = text[loc[1]:]
			}
		}
		seen := map[string]bool{}
		for _, word := range profileWordRE.FindAllString(text, -1) {
			if !stops[word] {
				seen[word] = true
			}
		}
		for word := range seen {
			counts[word]++
		}
	}
	words := make([]promptProfileWord, 0, len(counts))
	for word, count := range counts {
		words = append(words, promptProfileWord{Word: word, Count: count})
	}
	sort.Slice(words, func(i, j int) bool {
		if words[i].Count != words[j].Count {
			return words[i].Count > words[j].Count
		}
		return words[i].Word < words[j].Word
	})
	if len(words) > limit {
		words = words[:limit]
	}
	return words
}

func (q *Queue) PromptProfileSummary(f promptProfileFilter) promptProfileSummary {
	all := q.ScanPrompts(0, f.Project)
	minDT, maxDT := "", ""
	if len(all) > 0 {
		minDT, maxDT = all[0].DT, all[len(all)-1].DT
	}
	cutoff := q.cutoffDate(f.Days)
	selected := make([]*Prompt, 0, len(all))
	counts := map[string]int{"typed": 0, "bot": 0}
	for _, p := range all {
		if !promptInProfileWindow(p, f, cutoff) {
			continue
		}
		if typedKinds[p.Kind] {
			counts["typed"]++
		} else {
			counts["bot"]++
		}
		if promptMatchesKind(p, f.Kind) {
			selected = append(selected, p)
		}
	}
	rows := make([]promptProfileRow, 0, len(selected))
	for _, p := range selected {
		rows = append(rows, promptProfileRow{
			ID: promptProfileID(p), P: p.P, S: p.S, DT: p.DT, Chars: p.Chars,
			Words: p.Words, Kind: p.Kind, Skills: p.Skills,
			Lead: promptLeadSkill(p.X), Tone: promptToneFlags(p.X),
		})
	}
	cfg, _ := loadProfileStops()
	return promptProfileSummary{Prompts: rows,
		Counts: counts, Days: f.Days, Kind: f.Kind, Total: len(rows),
		MinDT: minDT, MaxDT: maxDT, Stopwords: cfg}
}

func (q *Queue) PromptProfileWords(f promptProfileFilter) promptWordsResponse {
	all := q.ScanPrompts(0, f.Project)
	cutoff := q.cutoffDate(f.Days)
	selected := make([]*Prompt, 0, len(all))
	for _, p := range all {
		if promptInProfileWindow(p, f, cutoff) && promptMatchesKind(p, f.Kind) {
			selected = append(selected, p)
		}
	}
	return promptWordsResponse{Words: promptProfileWords(selected, 46), Total: len(selected)}
}

type promptPageItem struct {
	ID        string `json:"i"`
	P         string `json:"p"`
	Date      string `json:"date"`
	Time      string `json:"time"`
	DT        string `json:"dt"`
	Kind      string `json:"kind"`
	X         string `json:"x"`
	Recap     string `json:"r,omitempty"`
	Truncated bool   `json:"tr,omitempty"`
	RecapCut  bool   `json:"rr,omitempty"`
}

type promptPageResponse struct {
	Prompts       []promptPageItem `json:"prompts"`
	NextPageToken string           `json:"next_page_token"`
	TotalSize     int              `json:"total_size"`
	PageSize      int              `json:"page_size"`
}

type promptDetailResponse struct {
	ID string `json:"i"`
	*Prompt
}

func truncatePromptText(s string, limit int) (string, bool) {
	r := []rune(s)
	if len(r) <= limit {
		return s, false
	}
	return string(r[:limit]), true
}

func makePromptPageItem(p *Prompt) promptPageItem {
	x, tr := truncatePromptText(p.X, profilePreviewRunes)
	r, rr := truncatePromptText(p.Recap, profileRecapRunes)
	return promptPageItem{ID: promptProfileID(p), P: p.P, Date: p.Date, Time: p.Time,
		DT: p.DT, Kind: p.Kind, X: x, Recap: r, Truncated: tr, RecapCut: rr}
}

func profilePageSize(raw string) (int, error) {
	if raw == "" || raw == "0" {
		return profilePageDefault, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errors.New("page_size must be a non-negative integer")
	}
	if n > profilePageMax {
		n = profilePageMax
	}
	return n, nil
}

func promptHasWord(text, want string) bool {
	want = strings.ToLower(want)
	for _, word := range profileWordRE.FindAllString(strings.ToLower(text), -1) {
		if word == want {
			return true
		}
	}
	return false
}

func promptPageFilterSignature(f promptProfileFilter, qs url.Values) [sha256.Size]byte {
	h := sha256.New()
	for _, value := range []string{
		strconv.Itoa(f.Days), f.Kind, f.Project,
		strconv.FormatBool(f.HasFrom), strconv.FormatInt(f.FromMS, 10),
		strconv.FormatBool(f.HasTo), strconv.FormatInt(f.ToMS, 10),
		strings.ToLower(strings.TrimSpace(qs.Get("q"))),
		strings.ToLower(strings.TrimSpace(qs.Get("word"))),
	} {
		writeProfileHashString(h, value)
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func encodePromptPageToken(id string, signature [sha256.Size]byte) string {
	idBytes, err := hex.DecodeString(id)
	if err != nil || len(idBytes) != 12 {
		return ""
	}
	raw := make([]byte, 0, 20)
	raw = append(raw, idBytes...)
	raw = append(raw, signature[:8]...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodePromptPageToken(token string, signature [sha256.Size]byte) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 20 || subtle.ConstantTimeCompare(raw[12:], signature[:8]) != 1 {
		return "", errors.New("page_token is invalid for these filters")
	}
	return hex.EncodeToString(raw[:12]), nil
}

func (q *Queue) PromptProfilePage(f promptProfileFilter, qs url.Values) (promptPageResponse, error) {
	pageSize, err := profilePageSize(qs.Get("page_size"))
	if err != nil {
		return promptPageResponse{}, err
	}
	all := q.ScanPrompts(0, f.Project)
	if rawIDs := qs.Get("ids"); rawIDs != "" {
		ids := strings.Split(rawIDs, ",")
		if len(ids) > profilePageMax {
			return promptPageResponse{}, errors.New("ids may contain at most 100 prompt ids")
		}
		byID := make(map[string]*Prompt, len(all))
		for _, p := range all {
			byID[promptProfileID(p)] = p
		}
		items := make([]promptPageItem, 0, len(ids))
		for _, id := range ids {
			if p := byID[id]; p != nil {
				items = append(items, makePromptPageItem(p))
			}
		}
		return promptPageResponse{Prompts: items, TotalSize: len(items), PageSize: len(items)}, nil
	}

	cutoff := q.cutoffDate(f.Days)
	query := strings.ToLower(strings.TrimSpace(qs.Get("q")))
	word := strings.ToLower(strings.TrimSpace(qs.Get("word")))
	filtered := make([]*Prompt, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		p := all[i]
		if !promptInProfileWindow(p, f, cutoff) || !promptMatchesKind(p, f.Kind) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(p.X), query) &&
			!strings.Contains(strings.ToLower(p.Recap), query) {
			continue
		}
		if word != "" && !promptHasWord(p.X, word) {
			continue
		}
		filtered = append(filtered, p)
	}
	signature := promptPageFilterSignature(f, qs)
	start := 0
	if token := qs.Get("page_token"); token != "" {
		cursorID, err := decodePromptPageToken(token, signature)
		if err != nil {
			return promptPageResponse{}, err
		}
		found := false
		for i, p := range filtered {
			if promptProfileID(p) == cursorID {
				start, found = i+1, true
				break
			}
		}
		if !found {
			return promptPageResponse{}, errors.New("page_token is invalid for these filters")
		}
	}
	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	items := make([]promptPageItem, 0, end-start)
	for _, p := range filtered[start:end] {
		items = append(items, makePromptPageItem(p))
	}
	next := ""
	if end < len(filtered) && len(items) > 0 {
		next = encodePromptPageToken(items[len(items)-1].ID, signature)
	}
	return promptPageResponse{Prompts: items, NextPageToken: next,
		TotalSize: len(filtered), PageSize: len(items)}, nil
}

func (q *Queue) PromptProfileDetail(id string) (*promptDetailResponse, bool) {
	for _, p := range q.ScanPrompts(0, "") {
		if promptProfileID(p) == id {
			return &promptDetailResponse{ID: id, Prompt: p}, true
		}
	}
	return nil, false
}

func (s *Server) sendProfileJSON(w http.ResponseWriter, r *http.Request, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": "marshal"})
		return
	}
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		s.send(w, http.StatusOK, b, "application/json")
		return
	}
	var z bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&z, gzip.BestSpeed)
	_, _ = zw.Write(b)
	_ = zw.Close()
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Vary", "Accept-Encoding")
	s.send(w, http.StatusOK, z.Bytes(), "application/json")
}

func (s *Server) servePromptSummary(w http.ResponseWriter, r *http.Request, qs url.Values) {
	f, err := parsePromptProfileFilter(qs)
	if err != nil {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.sendProfileJSON(w, r, s.Q.PromptProfileSummary(f))
}

func (s *Server) servePromptWords(w http.ResponseWriter, r *http.Request, qs url.Values) {
	f, err := parsePromptProfileFilter(qs)
	if err != nil {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.sendProfileJSON(w, r, s.Q.PromptProfileWords(f))
}

func (s *Server) servePromptPage(w http.ResponseWriter, r *http.Request, qs url.Values) {
	f, err := parsePromptProfileFilter(qs)
	if err != nil {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	page, err := s.Q.PromptProfilePage(f, qs)
	if err != nil {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.sendJSON(w, http.StatusOK, page)
}

func (s *Server) servePromptDetail(w http.ResponseWriter, qs url.Values) {
	id := qs.Get("id")
	if id == "" {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
		return
	}
	item, ok := s.Q.PromptProfileDetail(id)
	if !ok {
		s.sendJSON(w, http.StatusNotFound, map[string]string{"error": "prompt not found"})
		return
	}
	s.sendJSON(w, http.StatusOK, item)
}
