package retrieval

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TemporalIntent represents a time range extracted from a natural language query.
type TemporalIntent struct {
	From     time.Time
	To       time.Time
	Detected bool
}

// Compiled patterns for temporal expressions, ordered from most specific to least.
var (
	// "last/past N hour(s)/day(s)/week(s)/month(s)/minute(s)"
	reRelativeN = regexp.MustCompile(`(?i)\b(?:last|past)\s+(\d+)\s+(minute|hour|day|week|month)s?\b`)
	// "last/past hour/day/week/month" (implicit N=1)
	reRelativeUnit = regexp.MustCompile(`(?i)\b(?:last|past)\s+(hour|day|week|month)\b`)
	// Named periods
	reToday     = regexp.MustCompile(`(?i)\btoday\b`)
	reYesterday = regexp.MustCompile(`(?i)\byesterday\b`)
	reThisWeek  = regexp.MustCompile(`(?i)\bthis\s+week\b`)
	reThisMonth = regexp.MustCompile(`(?i)\bthis\s+month\b`)
	// Implicit recency
	reRecent = regexp.MustCompile(`(?i)\brecent(?:ly)?\b`)
)

// parseTemporalIntent extracts a time range from a natural language query.
// Patterns are checked from most specific to least specific so that
// "last 12 hours" takes priority over "recent".
func parseTemporalIntent(query string, now time.Time) TemporalIntent {
	// Most specific: "last/past N units"
	if m := reRelativeN.FindStringSubmatch(query); m != nil {
		n, err := strconv.Atoi(m[1])
		if err == nil && n > 0 {
			dur := unitToDuration(strings.ToLower(m[2]), n)
			return TemporalIntent{From: now.Add(-dur), To: now, Detected: true}
		}
	}

	// "last/past unit" (implicit N=1)
	if m := reRelativeUnit.FindStringSubmatch(query); m != nil {
		dur := unitToDuration(strings.ToLower(m[1]), 1)
		return TemporalIntent{From: now.Add(-dur), To: now, Detected: true}
	}

	// "today" — from midnight to now
	if reToday.MatchString(query) {
		sod := startOfDay(now)
		return TemporalIntent{From: sod, To: now, Detected: true}
	}

	// "yesterday" — from yesterday midnight to today midnight
	if reYesterday.MatchString(query) {
		sod := startOfDay(now)
		return TemporalIntent{From: sod.Add(-24 * time.Hour), To: sod, Detected: true}
	}

	// "this week" — from Monday midnight to now
	if reThisWeek.MatchString(query) {
		sow := startOfWeek(now)
		return TemporalIntent{From: sow, To: now, Detected: true}
	}

	// "this month" — from 1st of month midnight to now
	if reThisMonth.MatchString(query) {
		som := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return TemporalIntent{From: som, To: now, Detected: true}
	}

	// Implicit recency: "recent" / "recently" — default to 24 hours
	if reRecent.MatchString(query) {
		return TemporalIntent{From: now.Add(-24 * time.Hour), To: now, Detected: true}
	}

	return TemporalIntent{}
}

// unitToDuration converts a time unit name and count to a time.Duration.
func unitToDuration(unit string, n int) time.Duration {
	switch unit {
	case "minute":
		return time.Duration(n) * time.Minute
	case "hour":
		return time.Duration(n) * time.Hour
	case "day":
		return time.Duration(n) * 24 * time.Hour
	case "week":
		return time.Duration(n) * 7 * 24 * time.Hour
	case "month":
		return time.Duration(n) * 30 * 24 * time.Hour
	default:
		return time.Duration(n) * time.Hour
	}
}

// startOfDay returns midnight (00:00:00) of the given day in the same location.
func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// startOfWeek returns midnight of Monday of the ISO week containing t.
func startOfWeek(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday → 7 (ISO week starts on Monday)
	}
	monday := t.AddDate(0, 0, -(weekday - 1))
	return startOfDay(monday)
}
