package nightshift

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	explicitUsageLimitRe = regexp.MustCompile(`(?im)^\s*(?:error:\s*)?(?:you.?ve hit (?:your )?(?:usage )?limit|you have hit (?:your )?(?:usage )?limit|(?:usage|monthly|weekly) (?:limit|quota) (?:reached|exceeded)|out of [^\n]*credits?)\b`)
	genericUsageLimitRe  = regexp.MustCompile(`(?i)\busage limit\b|\blimit reached\b|\bout of [^\n]*credits?\b|\bquota exceeded\b|\btry again (?:at|in)\b|\bresets? (?:at|in)\b`)
	datedResetRe         = regexp.MustCompile(`(?i)(?:resets?|try again)(?:\s+on)?\s+(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:tember)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\s+([0-9]{1,2})(?:\s+at)?\s+([0-9]{1,2})(?::([0-9]{2}))?\s*(am|pm)(?:\s*\(([A-Za-z_]+/[A-Za-z_]+)\))?`)
	clockResetRe         = regexp.MustCompile(`(?i)(?:try again at|resets?(?:\s+at)?)\s+([0-9]{1,2})(?::([0-9]{2}))?\s*(am|pm)(?:\s*\(([A-Za-z_]+/[A-Za-z_]+)\))?`)
	relativeResetRe      = regexp.MustCompile(`(?i)(?:resets?|try again)\s+in\s+([0-9]+)\s*(seconds?|minutes?|hours?)`)
)

type usageLimitSignal struct {
	limited bool
	until   time.Time
}

// detectUsageLimit inspects only the terminal tail of a turn. Successful turns
// require a provider-shaped line at the start of a line, so a brain page that
// merely discusses an old usage limit cannot pause the fleet.
func detectUsageLimit(log string, exitCode int, now time.Time, fallback time.Duration) usageLimitSignal {
	tail := terminalLogTail(log, 64)
	explicit := explicitUsageLimitRe.MatchString(tail)
	if !explicit && (exitCode == 0 || !genericUsageLimitRe.MatchString(tail)) {
		return usageLimitSignal{}
	}
	until, ok := parseUsageReset(tail, now)
	if !ok {
		until = now.Add(fallback)
	}
	return usageLimitSignal{limited: true, until: until}
}

func terminalLogTail(log string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(log, "\r\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

func parseUsageReset(text string, now time.Time) (time.Time, bool) {
	if m := relativeResetRe.FindStringSubmatch(text); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := strings.ToLower(m[2])
		d := time.Duration(n) * time.Second
		if strings.HasPrefix(unit, "minute") {
			d = time.Duration(n) * time.Minute
		} else if strings.HasPrefix(unit, "hour") {
			d = time.Duration(n) * time.Hour
		}
		return now.Add(d).Add(time.Minute), true
	}
	if m := datedResetRe.FindStringSubmatch(text); m != nil {
		loc := resetLocation(m[6], now.Location())
		localNow := now.In(loc)
		month, ok := resetMonth(m[1])
		if !ok {
			return time.Time{}, false
		}
		day, _ := strconv.Atoi(m[2])
		hour, minute, ok := resetClock(m[3], m[4], m[5])
		if !ok {
			return time.Time{}, false
		}
		reset := time.Date(localNow.Year(), month, day, hour, minute, 0, 0, loc)
		if !reset.After(localNow) {
			reset = reset.AddDate(1, 0, 0)
		}
		return reset.Add(time.Minute), true
	}
	if m := clockResetRe.FindStringSubmatch(text); m != nil {
		loc := resetLocation(m[4], now.Location())
		localNow := now.In(loc)
		hour, minute, ok := resetClock(m[1], m[2], m[3])
		if !ok {
			return time.Time{}, false
		}
		reset := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, loc)
		if !reset.After(localNow) {
			reset = reset.AddDate(0, 0, 1)
		}
		return reset.Add(time.Minute), true
	}
	return time.Time{}, false
}

func resetLocation(name string, fallback *time.Location) *time.Location {
	if name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return fallback
}

func resetClock(hourText, minuteText, meridiem string) (hour, minute int, ok bool) {
	hour, err := strconv.Atoi(hourText)
	if err != nil || hour < 1 || hour > 12 {
		return 0, 0, false
	}
	if minuteText != "" {
		minute, err = strconv.Atoi(minuteText)
		if err != nil || minute < 0 || minute > 59 {
			return 0, 0, false
		}
	}
	if strings.EqualFold(meridiem, "am") {
		if hour == 12 {
			hour = 0
		}
	} else if hour != 12 {
		hour += 12
	}
	return hour, minute, true
}

func resetMonth(name string) (time.Month, bool) {
	months := []string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
	name = strings.ToLower(name)
	for i, month := range months {
		if strings.HasPrefix(month, name) || strings.HasPrefix(name, month[:3]) {
			return time.Month(i + 1), true
		}
	}
	return 0, false
}
