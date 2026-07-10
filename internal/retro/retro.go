// Package retro renders the monthly retro page: a deterministic HTML report
// over the journal day-cache ($DATA/journal/<date>.md) plus the same files
// the dashboard reads (tokens.jsonl, todo frontmatter, gbrain-queries.log).
// The model's only contribution is the cached journal prose; everything on
// this page — numbers, charts, layout — is computed here so the design can't
// drift between generations.
package retro

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	_ "embed"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/dashboard"
	"github.com/TheWeiHu/devbrain/internal/frontmatter"
	"github.com/TheWeiHu/devbrain/internal/pricing"
)

//go:embed template.html
var pageTemplate string

// pinned per-project colors (the user-approved retro palette); projects
// outside this map draw from fallback in a deterministic order.
var pinned = map[string]string{
	"devbrain":      "#58a6ff",
	"chess-equity":  "#a371f7",
	"llm-as-judge":  "#2dd4bf",
	"redlens":       "#3fb950",
	"miscellaneous": "#8b949e",
}

var fallback = []string{"#76e3ea", "#66d4cf", "#a5b4fc", "#d2a8ff", "#79c0ff", "#56d364", "#b2bfff"}

type Opts struct {
	Data string
	Days int
	Out  string
	Now  time.Time
}

type group struct {
	Project string
	Color   string
	Items   []template.HTML
}

type day struct {
	Date    string // 20260705
	Weekday string
	Groups  []group // bullets grouped by project, first-appearance order
}

type barRow struct {
	Label string
	Pct   float64
	Color string
	Value string
	Title string // hover tooltip (grade rows: the dimension's definition)
}

type col struct{ Pct float64 }

type axisCap struct {
	Label string
	Left  float64 // % offset of the label's own column in the strip
}

type pageData struct {
	Since, Today          string
	RangeNice             string
	Score                 int
	Letter                string
	GradeColor            string
	GradeRows             []barRow
	Projects              int
	Prompts, Sessions     string
	Shipped, Opened       string
	Spend                 string
	HitRate               string
	Queries               string
	SpendProj, SpendModel []barRow
	Shipped2              []barRow
	DayCols               []col
	DayCaps               []axisCap
	PeakNote              string
	Days                  []day
	Suggestions           []template.HTML
}

// short maps a projects/<dir> name to its display name (owner prefix dropped).
func short(p string) string {
	if _, rest, ok := strings.Cut(p, "__"); ok {
		return rest
	}
	return p
}

// num coerces a TokenRec's python-shaped field (json.Number / float64 / int)
// to float64; anything else counts as 0.
func num(v any) float64 {
	switch x := v.(type) {
	case json.Number:
		f, _ := x.Float64()
		return f
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// bulletRe accepts both journal bullet shapes: `- proj: text` and the
// TODO-fold form `- proj opened: text` / `- proj shipped: text`.
var bulletRe = regexp.MustCompile(`^- ([a-z0-9._-]+)(?: (opened|shipped))?: (.+)$`)
var promptRe = regexp.MustCompile(`(?m)^## \d\d:\d\d:\d\d`)
var codeRe = regexp.MustCompile("`([^`]+)`")

// renderText escapes a journal bullet and converts `code` spans.
func renderText(s string) template.HTML {
	esc := template.HTMLEscapeString(s)
	esc = codeRe.ReplaceAllString(esc, "<code>$1</code>")
	return template.HTML(esc)
}

func money(v float64) string { return "$" + comma(int64(v+0.5)) }
func comma(n int64) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// Generate computes the report and returns the HTML.
func Generate(o Opts) (string, error) {
	if o.Days <= 0 {
		o.Days = 30
	}
	if o.Days > 3660 { // ~10 years — beyond that it's a typo, not a window
		o.Days = 3660
	}
	// Window = today plus the previous N days (N+1 dates) — deliberately the
	// same `date -v-Nd` + `>=` math the journal and distill skills use, so a
	// 30-day retro covers exactly the days a `/journal 30` covers.
	today := o.Now.Format("2006-01-02")
	since := o.Now.AddDate(0, 0, -o.Days).Format("2006-01-02")
	in := func(d string) bool { return d >= since && d <= today }

	// ---- aggregates from the dashboard's data files -------------------------
	spendProj := map[string]float64{}
	spendModel := map[string]float64{}
	spendDay := map[string]float64{}
	prompts, sessions := 0, 0
	openedN, shippedN := 0, 0
	shippedProj := map[string]int{}
	queries, hits := 0, 0
	active := map[string]bool{}
	var cycles []float64
	staleTasks := 0

	// Spend comes through the dashboard's deduped reader (TokenUsage), NOT a
	// raw tokens.jsonl scan: the Stop hook re-captures a growing turn, and
	// only the (session, turn) keep-latest dedup counts it once.
	q := dashboard.New(o.Data)
	q.Now = func() time.Time { return o.Now }
	crCost := 0.0
	autoTurns, totalTurns := 0, 0
	for _, r := range q.TokenUsage(o.Days, "") {
		if !in(r.Date) {
			continue
		}
		p := short(r.P)
		rates := pricing.BillingRates(str(r.Model))
		c := (num(r.In)*rates[0] + num(r.Out)*rates[1] +
			num(r.CC)*rates[2] + num(r.CR)*rates[3]) / 1e6
		spendProj[p] += c
		spendModel[strings.TrimPrefix(str(r.Model), "claude-")] += c
		spendDay[r.Date] += c
		crCost += num(r.CR) * rates[3] / 1e6
		totalTurns++
		if r.Auto {
			autoTurns++
		}
		if c > 0 {
			active[p] = true
		}
	}

	projDirs, _ := filepath.Glob(filepath.Join(o.Data, "projects", "*"))
	sort.Strings(projDirs)
	for _, pd := range projDirs {
		p := short(filepath.Base(pd))
		dayDirs, _ := filepath.Glob(filepath.Join(pd, "log", "20*"))
		for _, dd := range dayDirs {
			if !in(filepath.Base(dd)) {
				continue
			}
			files, _ := filepath.Glob(filepath.Join(dd, "*.md"))
			sessions += len(files)
			for _, lf := range files {
				b, err := os.ReadFile(lf)
				if err != nil {
					continue
				}
				prompts += len(promptRe.FindAllIndex(b, -1))
				active[p] = true
			}
		}
		taskFiles, _ := filepath.Glob(filepath.Join(pd, "todo", "*.md"))
		for _, tf := range taskFiles {
			b, err := os.ReadFile(tf)
			if err != nil {
				continue
			}
			fm := frontmatter.Parse(string(b)).FM
			if c := fm["created"]; len(c) >= 10 && in(c[:10]) {
				openedN++
				active[p] = true
			}
			if d := fm["done_at"]; len(d) >= 10 && in(d[:10]) {
				shippedN++
				shippedProj[p]++
				active[p] = true
				// cycle time: created → done_at, when both parse
				ct, e1 := time.Parse(time.RFC3339, fm["created"])
				dt, e2 := time.Parse(time.RFC3339, fm["done_at"])
				if e1 == nil && e2 == nil && dt.After(ct) {
					cycles = append(cycles, dt.Sub(ct).Hours()/24)
				}
			}
			// queue hygiene: a claim or hold older than 7 days is stale
			if st := fm["status"]; st == "taken" || st == "held" {
				ref := fm["claimed_at"]
				if ref == "" {
					ref = fm["created"]
				}
				if t, err := time.Parse(time.RFC3339, ref); err == nil &&
					o.Now.Sub(t) > 7*24*time.Hour {
					staleTasks++
				}
			}
		}
		if b, err := os.ReadFile(filepath.Join(pd, "gbrain-queries.log")); err == nil {
			// per-line parse so one torn/corrupt record costs one record, not
			// the rest of the file (the log is append-only and crash-prone)
			for _, line := range strings.Split(string(b), "\n") {
				var r struct {
					TS   string `json:"ts"`
					Hits int    `json:"hits"`
				}
				if json.Unmarshal([]byte(line), &r) != nil {
					continue
				}
				if len(r.TS) >= 10 && in(r.TS[:10]) {
					queries++
					if r.Hits > 0 {
						hits++
					}
				}
			}
		}
	}

	// deterministic project colors: pinned first, then fallback by spend rank.
	colorOf := map[string]string{}
	rank := keysBy(spendProj)
	i := 0
	for _, p := range rank {
		if c, ok := pinned[p]; ok {
			colorOf[p] = c
		} else {
			colorOf[p] = fallback[i%len(fallback)]
			i++
		}
	}
	color := func(p string) string {
		if c, ok := colorOf[p]; ok {
			return c
		}
		if c, ok := pinned[p]; ok {
			return c
		}
		return "#8b949e"
	}

	// ---- journal day cards ---------------------------------------------------
	var days []day
	cacheFiles, _ := filepath.Glob(filepath.Join(o.Data, "journal", "20*.md"))
	sort.Sort(sort.Reverse(sort.StringSlice(cacheFiles)))
	for _, cf := range cacheFiles {
		d := strings.TrimSuffix(filepath.Base(cf), ".md")
		if !in(d) {
			continue
		}
		t, err := time.Parse("2006-01-02", d)
		if err != nil {
			continue
		}
		b, err := os.ReadFile(cf)
		if err != nil {
			continue
		}
		var groups []group
		idx := map[string]int{}
		for _, line := range strings.Split(string(b), "\n") {
			m := bulletRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			text := m[3]
			if m[2] != "" {
				text = m[2] + ": " + text
			}
			p, item := m[1], renderText(text)
			if i, ok := idx[p]; ok {
				groups[i].Items = append(groups[i].Items, item)
				continue
			}
			idx[p] = len(groups)
			groups = append(groups, group{Project: p, Color: color(p), Items: []template.HTML{item}})
		}
		if len(groups) > 0 {
			days = append(days, day{Date: t.Format("20060102"), Weekday: t.Format("Monday"), Groups: groups})
		}
	}

	// ---- chart rows ------------------------------------------------------------
	totalSpend := 0.0
	for _, v := range spendProj {
		totalSpend += v
	}
	spendRows := topRows(spendProj, 7, color, money)
	modelRows := topRows(spendModel, 4, func(string) string { return "" }, money)
	for i := range modelRows { // models use a fixed cool sequence, not project colors
		modelRows[i].Color = []string{"#58a6ff", "#a371f7", "#2dd4bf", "#484f58", "#484f58"}[min(i, 4)]
	}
	shippedRows := topRows(toF(shippedProj), 8, color, func(v float64) string { return comma(int64(v + 0.5)) })

	// spend-by-day strip, one column per day since..today
	var cols []col
	var caps []axisCap
	peakDay, peak := "", 0.0
	for d := since; d <= today; d = nextDay(d) {
		if v := spendDay[d]; v > peak {
			peak, peakDay = v, d
		}
	}
	nDatesTotal := o.Days + 1
	n := 0
	for d := since; d <= today; d = nextDay(d) {
		pct := 0.0
		if peak > 0 {
			pct = spendDay[d] / peak * 100
		}
		cols = append(cols, col{Pct: pct})
		if n%7 == 0 {
			if t, err := time.Parse("2006-01-02", d); err == nil {
				// absolute position so the label sits under its own column
				// instead of being spread evenly by the flex row
				caps = append(caps, axisCap{Label: t.Format("Jan 02"),
					Left: float64(n) / float64(nDatesTotal) * 100})
			}
		}
		n++
	}
	peakNote := ""
	if peakDay != "" {
		if t, err := time.Parse("2006-01-02", peakDay); err == nil {
			peakNote = fmt.Sprintf("peak %s on %s", money(peak), t.Format("Jan 2"))
		}
	}

	// ---- deterministic suggestions ----------------------------------------------
	var sugg []template.HTML
	if totalSpend > 0 {
		if m, v := maxOf(spendModel); v/totalSpend >= 0.6 {
			sugg = append(sugg, template.HTML(fmt.Sprintf(
				"<b>%.0f%% of spend is %s (%s of %s)</b> — route bulk autonomous work to cheaper models where possible.",
				v/totalSpend*100, template.HTMLEscapeString(m), money(v), money(totalSpend))))
		}
	}
	if queries >= 50 && float64(hits)/float64(queries) < 0.5 {
		sugg = append(sugg, template.HTML(fmt.Sprintf(
			"<b>Brain hit rate is %.1f%%</b> — %s of %s queries returned nothing; tune slugs and query phrasing.",
			float64(hits)/float64(queries)*100, comma(int64(queries-hits)), comma(int64(queries)))))
	}
	if openedN > shippedN {
		sugg = append(sugg, template.HTML(fmt.Sprintf(
			"<b>%s tasks opened vs %s shipped</b> — the backlog grew by %s this period.",
			comma(int64(openedN)), comma(int64(shippedN)), comma(int64(openedN-shippedN)))))
	}
	if peakNote != "" && totalSpend > 0 && peak > totalSpend/float64(o.Days)*3 {
		sugg = append(sugg, template.HTML(fmt.Sprintf(
			"<b>Spend is spiky</b> — %s, %.1f× the period's daily average; spikes usually track fleet runs.",
			template.HTMLEscapeString(peakNote), peak/(totalSpend/float64(o.Days)))))
	}

	hitRate := "—"
	if queries > 0 {
		hitRate = fmt.Sprintf("%.1f%%", float64(hits)/float64(queries)*100)
	}

	// month grade — same inputs, fixed rubric
	nDates := o.Days + 1 // window = today + previous N days
	activeSpendDays := 0
	for _, v := range spendDay {
		if v > 0 {
			activeSpendDays++
		}
	}
	autoShare, cacheShare := 0.0, 0.0
	if totalTurns > 0 {
		autoShare = float64(autoTurns) / float64(totalTurns)
	}
	if totalSpend > 0 {
		cacheShare = crCost / totalSpend
	}
	// No signal, no grade: an idle month must not outgrade a mediocre one via
	// the rubric's benefit-of-the-doubt defaults.
	score, letter, gcolor := 0, "", ""
	var gradeRows []barRow
	if totalSpend > 0 || shippedN > 0 || prompts > 0 {
		var parts []gradePart
		score, parts = grade(gradeInput{
			Shipped: shippedN, Opened: openedN, Spend: totalSpend,
			Queries: queries, Hits: hits,
			JournalDays: len(days), ActiveDays: activeSpendDays, WindowDays: nDates,
			PeakDay: peak, AvgDay: totalSpend / float64(nDates),
			CycleMedianDays: median(cycles), StaleTasks: staleTasks,
			AutoShare: autoShare, CacheShare: cacheShare,
		})
		letter, gcolor = letterOf(score), gradeColor(score)
		for _, p := range parts {
			// one decimal when fractional, so "4.8/5" never shows as a full-bar "5/5"
			earned := strings.TrimSuffix(fmt.Sprintf("%.1f", p.Earned), ".0")
			gradeRows = append(gradeRows, barRow{
				Label: p.Label, Pct: p.Earned / p.Max * 100, Color: "#58a6ff",
				Value: fmt.Sprintf("%s/%.0f", earned, p.Max), Title: p.Def,
			})
		}
	}

	data := pageData{
		Score: score, Letter: letter, GradeColor: gcolor,
		GradeRows: gradeRows,
		Since:     since, Today: today,
		RangeNice: niceRange(since, today),
		Projects:  len(active),
		Prompts:   comma(int64(prompts)), Sessions: comma(int64(sessions)),
		Shipped: comma(int64(shippedN)), Opened: comma(int64(openedN)),
		Spend: money(totalSpend), HitRate: hitRate, Queries: comma(int64(queries)),
		SpendProj: spendRows, SpendModel: modelRows, Shipped2: shippedRows,
		DayCols: cols, DayCaps: caps, PeakNote: peakNote,
		Days: days, Suggestions: sugg,
	}
	tmpl, err := template.New("retro").Parse(pageTemplate)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// topRows turns a metric map into ranked bar rows: top n plus an "others" row.
func topRows(m map[string]float64, n int, color func(string) string, fmtv func(float64) string) []barRow {
	keys := keysBy(m)
	max := 0.0
	if len(keys) > 0 {
		max = m[keys[0]]
	}
	var rows []barRow
	othersSum, others := 0.0, 0
	for i, k := range keys {
		if i < n {
			pct := 0.0
			if max > 0 {
				pct = m[k] / max * 100
			}
			c := color(k)
			if c == "" {
				c = "#58a6ff"
			}
			rows = append(rows, barRow{Label: k, Pct: pct, Color: c, Value: fmtv(m[k])})
		} else {
			othersSum += m[k]
			others++
		}
	}
	if others > 0 && max > 0 {
		rows = append(rows, barRow{Label: fmt.Sprintf("%d others", others),
			Pct: othersSum / max * 100, Color: "#484f58", Value: fmtv(othersSum)})
	}
	return rows
}

// keysBy returns map keys sorted by value desc, then name (deterministic).
func keysBy(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

func toF(m map[string]int) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = float64(v)
	}
	return out
}

func maxOf(m map[string]float64) (string, float64) {
	keys := keysBy(m) // already value-desc
	if len(keys) == 0 {
		return "", -1
	}
	return keys[0], m[keys[0]]
}

// ---- month grade -----------------------------------------------------------
// Deterministic rubric over the same inputs as the rest of the page. Weights
// are the rubric; tune them here, never at render time.

type gradeInput struct {
	Shipped, Opened int
	Spend           float64
	Queries, Hits   int
	JournalDays     int // days in window with a journal entry
	ActiveDays      int // days in window with any spend
	WindowDays      int
	PeakDay, AvgDay float64
	CycleMedianDays float64 // median created→done_at of shipped tasks (-1 = none)
	StaleTasks      int     // taken/held for >7 days as of Now
	AutoShare       float64 // 0..1 fraction of turns with the auto flag
	CacheShare      float64 // 0..1 cache-read dollars ÷ total dollars
}

type gradePart struct {
	Label  string
	Earned float64
	Max    float64
	Def    string // hover definition on the grade row
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// grade scores the month 0–100 across twelve dimensions (user-tuned
// 2026-07-05; "model mix" was tried and cut as not meaningful):
// Shipping 40 — throughput 12 (vs 3 tasks/day) · flow 12 (shipped ÷ opened) ·
//
//	cycle time 8 (median created→done ≤2d full, ≥14d zero) ·
//	cost per shipped task 8 ($5 full → $50 zero, log-scaled).
//
// Leverage 20 — delegation share 12 (auto-turn fraction, 30–70% band full) ·
//
//	cache discipline 8 (cache-read $ share ≤75% full, 100% zero).
//
// Brain 22    — hit rate 8 (vs 70%) · brain usage 8 (queries/active-day vs 3) ·
//
//	journal coverage 6 (journal days ÷ ACTIVE days — rest days
//	don't count against coverage; active days has its own line).
//
// Cadence 18  — active days 6 · queue hygiene 8 (0 stale taken/held full,
//
//	4+ zero) · spend smoothness 4 (peak ≤3× daily avg full).
func grade(g gradeInput) (int, []gradePart) {
	parts := []gradePart{
		{"throughput", 12 * clamp01(float64(g.Shipped)/(3*float64(g.WindowDays))), 12,
			"tasks shipped per day vs a 3/day target"},
		{"flow (shipped ÷ opened)", 12, 12,
			"tasks shipped ÷ tasks opened — a growing backlog costs points"},
		{"cycle time", 8, 8,
			"median created → done per shipped task; ≤2 days full marks, ≥14 days zero"},
		{"cost per shipped task", 8, 8,
			"total spend ÷ tasks shipped; $5/task full marks → $50/task zero, log-scaled"},
		{"delegation share", 0, 12,
			"fraction of turns run autonomously (nightshift/bot); 30–70% is the full-marks band"},
		{"cache discipline", 8 * clamp01((1-g.CacheShare)/0.25), 8,
			"cache-read dollars ÷ total dollars; ≤75% full marks, 100% zero — a high share means long-context re-read loops"},
		{"brain hit rate", 0, 8,
			"gbrain queries returning at least one page vs a 70% target"},
		{"brain usage", 0, 8,
			"gbrain queries per active day vs a 3/day target — was the brain consulted at all"},
		{"journal coverage", 6, 6,
			"active days that have a journal entry (rest days don't count against coverage)"},
		{"active days", 6 * clamp01(float64(g.ActiveDays)/float64(g.WindowDays)), 6,
			"days with any spend ÷ days in the window"},
		{"queue hygiene", 8 * clamp01(1-float64(g.StaleTasks)/4), 8,
			"tasks stuck taken/held for more than 7 days; zero stale is full marks, 4+ is zero"},
		{"spend smoothness", 4, 4,
			"peak spend day vs the daily average; ≤3× full marks, ≥10× zero"},
	}
	if g.Opened > 0 {
		parts[1].Earned = 12 * clamp01(float64(g.Shipped)/float64(g.Opened))
	}
	if g.CycleMedianDays >= 0 {
		parts[2].Earned = 8 * clamp01((14-g.CycleMedianDays)/12) // ≤2d full, ≥14d zero
	}
	if g.Shipped > 0 && g.Spend > 0 {
		perTask := g.Spend / float64(g.Shipped)
		parts[3].Earned = 8 * clamp01(math.Log(50/math.Max(perTask, 5))/math.Log(50/5.0))
	} else if g.Spend > 0 {
		parts[3].Earned = 0 // spent money, shipped nothing
	}
	switch s := g.AutoShare; {
	case s >= 0.3 && s <= 0.7:
		parts[4].Earned = 12
	case s < 0.3:
		parts[4].Earned = 12 * clamp01(s/0.3)
	default:
		parts[4].Earned = 12 * clamp01((1-s)/0.3)
	}
	if g.Queries > 0 {
		parts[6].Earned = 8 * clamp01(float64(g.Hits)/float64(g.Queries)/0.7)
	}
	if g.ActiveDays > 0 {
		parts[7].Earned = 8 * clamp01(float64(g.Queries)/float64(g.ActiveDays)/3)
		parts[8].Earned = 6 * clamp01(float64(g.JournalDays)/float64(g.ActiveDays))
	}
	if g.AvgDay > 0 {
		ratio := g.PeakDay / g.AvgDay
		parts[11].Earned = 4 * clamp01(1-(ratio-3)/7)
	}
	total := 0.0
	for _, p := range parts {
		total += p.Earned
	}
	return int(total + 0.5), parts
}

// median of a slice (destructive sort); -1 when empty.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return -1
	}
	sort.Float64s(xs)
	n := len(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}

// letterOf maps a /100 score to the uOttawa letter scheme.
func letterOf(score int) string {
	switch {
	case score >= 90:
		return "A+"
	case score >= 85:
		return "A"
	case score >= 80:
		return "A-"
	case score >= 75:
		return "B+"
	case score >= 70:
		return "B"
	case score >= 66:
		return "C+"
	case score >= 60:
		return "C"
	case score >= 55:
		return "D+"
	case score >= 50:
		return "D"
	case score >= 40:
		return "E"
	}
	return "F"
}

// gradeColor keeps the badge in the cool palette: green for A-range, blue for
// B, muted for C/D, danger red only for E/F.
func gradeColor(score int) string {
	switch {
	case score >= 80:
		return "#3fb950"
	case score >= 70:
		return "#58a6ff"
	case score >= 50:
		return "#8b949e"
	}
	return "#f85149"
}

// niceRange renders the window the way the dashboard renders dates for
// humans — short month names, no ISO strings ("Jun 5 → Jul 5, 2026").
func niceRange(since, today string) string {
	a, err1 := time.Parse("2006-01-02", since)
	b, err2 := time.Parse("2006-01-02", today)
	if err1 != nil || err2 != nil {
		return since + " → " + today
	}
	if a.Year() == b.Year() {
		return fmt.Sprintf("%s → %s", a.Format("Jan 2"), b.Format("Jan 2, 2006"))
	}
	return fmt.Sprintf("%s → %s", a.Format("Jan 2, 2006"), b.Format("Jan 2, 2006"))
}

func nextDay(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return "9999-99-99"
	}
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

// Run is the `devbrain retro` CLI.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("devbrain retro", flag.ContinueOnError)
	fs.SetOutput(stderr)
	days := fs.Int("days", 30, "window in days")
	out := fs.String("out", "", "output file (default $DATA/retro/<today>.html)")
	data := fs.String("data", "", "data dir (default resolved devbrain data dir)")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: devbrain retro [--days N] [--out FILE] [--data DIR] [--no-open]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0 // -h/--help exits clean, like the sibling verbs
		}
		return 2
	}
	// UTC, like every date the data files carry (hooks stamp UTC): a local
	// clock here would drop end-of-day records for west-of-UTC users.
	o := Opts{Data: *data, Days: *days, Now: time.Now().UTC()}
	if o.Data == "" {
		var err error
		o.Data, err = config.ResolveDataDir()
		if err != nil {
			fmt.Fprintf(stderr, "retro: %v\n", err)
			return 1
		}
	}
	html, err := Generate(o)
	if err != nil {
		fmt.Fprintf(stderr, "retro: %v\n", err)
		return 1
	}
	dest := *out
	if dest == "" {
		dest = filepath.Join(o.Data, "retro", o.Now.Format("2006-01-02")+".html")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintf(stderr, "retro: %v\n", err)
		return 1
	}
	// tmp+rename so the flusher's `git add -A` (or a concurrent retro) can
	// never commit a half-written report; pid-unique so two retros don't
	// interleave on one temp path
	tmp := fmt.Sprintf("%s.%d.tmp", dest, os.Getpid())
	if err := os.WriteFile(tmp, []byte(html), 0o644); err != nil {
		fmt.Fprintf(stderr, "retro: %v\n", err)
		return 1
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		fmt.Fprintf(stderr, "retro: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, dest)
	if !*noOpen {
		openCmd := "xdg-open"
		if runtime.GOOS == "darwin" {
			openCmd = "open"
		}
		_ = exec.Command(openCmd, dest).Start()
	}
	return 0
}
