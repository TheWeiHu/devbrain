// Package heartbeat reads flush health straight from the data repo's own
// history: every flush commit is titled "capture: <ts> on <host>", so the
// newest capture commit per host IS that host's liveness signal — no extra
// state, no new channel. A host that has committed inside the active window
// but not recently is stale; unmerged files mean flush is wedged locally.
// Surfaced at brain-read time (brain search/get) and session start, because
// that is the moment stale history can mislead (satellites rarely run
// /continue; the impetuous box flushed nothing for 24 days unnoticed).
package heartbeat

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/gitx"
)

const (
	staleAfter   = 48 * time.Hour       // silent longer than this -> warn
	activeWindow = 60 * 24 * time.Hour  // silent longer than this -> retired, stop warning
	scanLimit    = "500"                // capture commits to scan; ~weeks of history
)

// Now is swappable for tests.
var Now = time.Now

// Host is one capture source and its newest capture commit time.
type Host struct {
	Name string
	Last time.Time
}

// Status is the health of the data repo's capture flow.
type Status struct {
	Wedged      bool   // unmerged files: flush aborts until resolved
	Self        *Host  // this machine's entry, if it has ever captured
	SelfStale   bool
	StaleOthers []Host // other active hosts gone silent
}

// Check inspects the data repo. Git failures degrade to a healthy Status —
// a broken heartbeat must never break the brain read it decorates.
func Check(dataDir string) Status {
	repo := gitx.Repo{Dir: dataDir}
	var st Status
	if out, err := repo.Run("ls-files", "-u"); err == nil && out != "" {
		st.Wedged = true
	}
	out, err := repo.Run("log", "--grep=^capture:", "-n", scanLimit,
		"--format=%ct%x09%s")
	if err != nil {
		return st
	}
	newest := map[string]time.Time{}
	for _, line := range strings.Split(out, "\n") {
		ts, sub, ok := strings.Cut(line, "\t")
		host := hostOf(sub)
		if !ok || host == "" {
			continue
		}
		sec, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			continue
		}
		when := time.Unix(sec, 0)
		if when.After(newest[host]) {
			newest[host] = when
		}
	}
	self := selfHost()
	now := Now()
	for name, when := range newest {
		age := now.Sub(when)
		if name == self {
			h := Host{Name: name, Last: when}
			st.Self = &h
			st.SelfStale = age > staleAfter
			continue
		}
		if age > staleAfter && age <= activeWindow {
			st.StaleOthers = append(st.StaleOthers, Host{Name: name, Last: when})
		}
	}
	return st
}

// Warning renders Check as one line, or "" when healthy. Local trouble
// (wedged, self stale) wins over remote staleness: fix your own flush first.
func Warning(dataDir string) string {
	st := Check(dataDir)
	if st.Wedged {
		return "devbrain: flush is WEDGED on unmerged files in the data repo — " +
			"captures are piling up unshipped; resolve with `git status` there, then `devbrain flush`"
	}
	if st.SelfStale && st.Self != nil {
		return fmt.Sprintf("devbrain: this host has not shipped its capture log since %s (%s) — "+
			"the shared brain is missing its recent history; check `devbrain flush`",
			st.Self.Last.Format("2006-01-02"), ageOf(Now().Sub(st.Self.Last)))
	}
	if len(st.StaleOthers) > 0 {
		names := make([]string, len(st.StaleOthers))
		for i, h := range st.StaleOthers {
			names[i] = fmt.Sprintf("%s since %s (%s)",
				h.Name, h.Last.Format("2006-01-02"), ageOf(Now().Sub(h.Last)))
		}
		return "devbrain: brain may be missing recent history — no capture from " +
			strings.Join(names, ", ")
	}
	return ""
}

// selfHost mirrors flush's commit-message host: `hostname -s`.
func selfHost() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "host"
	}
	return strings.SplitN(h, ".", 2)[0]
}

func hostOf(subject string) string {
	if !strings.HasPrefix(subject, "capture:") {
		return ""
	}
	_, host, ok := strings.Cut(subject, " on ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(host)
}

func ageOf(d time.Duration) string {
	if days := int(d.Hours() / 24); days >= 1 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
