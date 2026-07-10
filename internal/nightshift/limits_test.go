package nightshift

import (
	"strings"
	"testing"
	"time"
)

func TestDetectUsageLimitUsesCodexResetClock(t *testing.T) {
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 9, 6, 31, 0, 0, loc)
	sig := detectUsageLimit("ERROR: You've hit your usage limit. Visit settings or try again at 7:02 AM.\n", 1, now, 5*time.Minute)
	if !sig.limited {
		t.Fatal("Codex terminal error must be classified as a usage limit")
	}
	want := time.Date(2026, 7, 9, 7, 3, 0, 0, loc)
	if !sig.until.Equal(want) {
		t.Fatalf("until = %s, want %s", sig.until, want)
	}
}

func TestDetectUsageLimitUsesDatedClaudeReset(t *testing.T) {
	loc, _ := time.LoadLocation("America/Toronto")
	now := time.Date(2026, 7, 9, 23, 0, 0, 0, loc)
	sig := detectUsageLimit("You've hit your limit - resets Jul 11 at 5am (America/Toronto)\n", 0, now, 5*time.Minute)
	want := time.Date(2026, 7, 11, 5, 1, 0, 0, loc)
	if !sig.limited || !sig.until.Equal(want) {
		t.Fatalf("signal = %+v, want until %s", sig, want)
	}
}

func TestDetectUsageLimitUsesRelativeReset(t *testing.T) {
	now := time.Date(2026, 7, 9, 2, 0, 0, 0, time.UTC)
	sig := detectUsageLimit("ERROR: usage limit reached; try again in 30 minutes\n", 1, now, 5*time.Minute)
	want := now.Add(31 * time.Minute)
	if !sig.limited || !sig.until.Equal(want) {
		t.Fatalf("signal = %+v, want until %s", sig, want)
	}
}

func TestDetectUsageLimitIgnoresHistoricalContext(t *testing.T) {
	log := strings.Repeat("ordinary output\n", 70) +
		"- The old fleet said: You've hit your usage limit.\n" +
		"Implemented the requested quota documentation and opened the PR.\n"
	if sig := detectUsageLimit(log, 0, time.Now(), 5*time.Minute); sig.limited {
		t.Fatalf("successful turn discussion must not pause the fleet: %+v", sig)
	}
}

func TestDetectUsageLimitFallsBackForNonzeroGenericError(t *testing.T) {
	now := time.Date(2026, 7, 9, 2, 0, 0, 0, time.UTC)
	sig := detectUsageLimit("request failed: monthly quota exceeded\n", 1, now, 5*time.Minute)
	if !sig.limited || !sig.until.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("signal = %+v", sig)
	}
}
