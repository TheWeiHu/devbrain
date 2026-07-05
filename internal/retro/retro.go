// Package retro renders the monthly retro page: a deterministic HTML report
// over the journal day-cache ($DATA/journal/<date>.md) plus the same files
// the dashboard reads (tokens.jsonl, todo frontmatter, gbrain-queries.log).
// The model's only contribution is the cached journal prose; everything on
// this page — numbers, charts, layout — is computed here so the design can't
// drift between generations.
package retro

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
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
	"github.com/TheWeiHu/devbrain/internal/frontmatter"
	"github.com/TheWeiHu/devbrain/internal/pricing"
	"github.com/TheWeiHu/devbrain/internal/queue"
)

//go:embed template.html
var pageTemplate string

// pinned per-project colors (the user-approved retro palette); projects
// outside this map draw from fallback in a deterministic order.
var pinned = map[string]string{
	"devbrain":     "#58a6ff",
	"chess-equity": "#a371f7",
	"llm-as-judge": "#2dd4bf",
	"redlens":      "#3fb950",
	"miscellaneous": "#8b949e",
}

var fallback = []string{"#76e3ea", "#66d4cf", "#a5b4fc", "#d2a8ff", "#79c0ff", "#56d364", "#b2bfff"}

type Opts struct {
	Data string
	Days int
	Out  string
	Now  time.Time
}

type bullet struct {
	Project string
	Color   string
	HTML    template.HTML
}

type day struct {
	Date    string // 20260705
	Weekday string
	Bullets []bullet
}

type barRow struct {
	Label string
	Pct   float64
	Color string
	Value string
}

type col struct{ Pct float64 }

type pageData struct {
	Since, Today, Generated string
	RangeNice               string
	Projects                int
	Prompts, Sessions       string
	Shipped, Opened         string
	Spend                   string
	HitRate                 string
	Queries                 string
	SpendProj, SpendModel   []barRow
	Shipped2                []barRow
	DayCols                 []col
	DayCaps                 []string
	PeakNote                string
	Days                    []day
	Suggestions             []template.HTML
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

var bulletRe = regexp.MustCompile(`^- ([a-z0-9._-]+): (.+)$`)
var promptRe = regexp.MustCompile(`(?m)^## \d\d:\d\d:\d\d`)
var codeRe = regexp.MustCompile("`([^`]+)`")

// renderText escapes a journal bullet and converts `code` spans.
func renderText(s string) template.HTML {
	esc := template.HTMLEscapeString(s)
	esc = codeRe.ReplaceAllString(esc, "<code>$1</code>")
	return template.HTML(esc)
}

func money(v float64) string    { return "$" + comma(int64(v+0.5)) }
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

	// Spend comes through the dashboard's deduped reader (queue.TokenUsage),
	// NOT a raw tokens.jsonl scan: the Stop hook re-captures a growing turn,
	// and only the (session, turn) keep-latest dedup counts it once.
	q := queue.New(o.Data)
	q.Now = func() time.Time { return o.Now }
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
			}
		}
		if f, err := os.Open(filepath.Join(pd, "gbrain-queries.log")); err == nil {
			dec := json.NewDecoder(f)
			for {
				var r struct {
					TS   string `json:"ts"`
					Hits int    `json:"hits"`
				}
				if err := dec.Decode(&r); err != nil {
					break
				}
				if len(r.TS) >= 10 && in(r.TS[:10]) {
					queries++
					if r.Hits > 0 {
						hits++
					}
				}
			}
			f.Close()
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
		var bl []bullet
		for _, line := range strings.Split(string(b), "\n") {
			m := bulletRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			bl = append(bl, bullet{Project: m[1], Color: color(m[1]), HTML: renderText(m[2])})
		}
		if len(bl) > 0 {
			days = append(days, day{Date: t.Format("20060102"), Weekday: t.Format("Monday"), Bullets: bl})
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
	var caps []string
	peakDay, peak := "", 0.0
	for d := since; d <= today; d = nextDay(d) {
		if v := spendDay[d]; v > peak {
			peak, peakDay = v, d
		}
	}
	n := 0
	for d := since; d <= today; d = nextDay(d) {
		pct := 0.0
		if peak > 0 {
			pct = spendDay[d] / peak * 100
		}
		cols = append(cols, col{Pct: pct})
		if n%7 == 0 {
			if t, err := time.Parse("2006-01-02", d); err == nil {
				caps = append(caps, t.Format("Jan 02"))
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
	data := pageData{
		Since: since, Today: today, Generated: today,
		RangeNice: niceRange(since, today),
		Projects:  len(active),
		Prompts:  comma(int64(prompts)), Sessions: comma(int64(sessions)),
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
	best, bv := "", -1.0
	for _, k := range keysBy(m) {
		if m[k] > bv {
			best, bv = k, m[k]
		}
	}
	return best, bv
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Run is the `devbrain retro` CLI.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("retro", flag.ContinueOnError)
	fs.SetOutput(stderr)
	days := fs.Int("days", 30, "window in days")
	out := fs.String("out", "", "output file (default $DATA/retro/<today>.html)")
	data := fs.String("data", "", "data dir (default resolved devbrain data dir)")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	if err := fs.Parse(args); err != nil {
		fmt.Fprint(stderr, "usage: devbrain retro [--days N] [--out FILE] [--data DIR] [--no-open]\n")
		return 2
	}
	o := Opts{Data: *data, Days: *days, Now: time.Now()}
	if o.Data == "" {
		o.Data = config.DataDir()
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
	if err := os.WriteFile(dest, []byte(html), 0o644); err != nil {
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
