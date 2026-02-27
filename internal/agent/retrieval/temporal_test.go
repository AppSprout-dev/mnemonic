package retrieval

import (
	"testing"
	"time"
)

func TestParseTemporalIntent(t *testing.T) {
	// Fixed reference time: Wednesday, 2026-02-25 14:30:00 UTC
	now := time.Date(2026, 2, 25, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name         string
		query        string
		wantDetected bool
		wantFrom     time.Time
		wantTo       time.Time
	}{
		// --- "last/past N units" patterns ---
		{
			name:         "last 12 hours",
			query:        "what recent work has been performed in the last 12 hours?",
			wantDetected: true,
			wantFrom:     now.Add(-12 * time.Hour),
			wantTo:       now,
		},
		{
			name:         "last 3 days",
			query:        "show me changes from the last 3 days",
			wantDetected: true,
			wantFrom:     now.Add(-3 * 24 * time.Hour),
			wantTo:       now,
		},
		{
			name:         "past 2 weeks",
			query:        "what happened in the past 2 weeks",
			wantDetected: true,
			wantFrom:     now.Add(-2 * 7 * 24 * time.Hour),
			wantTo:       now,
		},
		{
			name:         "last 30 minutes",
			query:        "anything in the last 30 minutes",
			wantDetected: true,
			wantFrom:     now.Add(-30 * time.Minute),
			wantTo:       now,
		},
		{
			name:         "last 1 month",
			query:        "work from the last 1 month",
			wantDetected: true,
			wantFrom:     now.Add(-30 * 24 * time.Hour),
			wantTo:       now,
		},
		{
			name:         "plural units",
			query:        "past 4 days of activity",
			wantDetected: true,
			wantFrom:     now.Add(-4 * 24 * time.Hour),
			wantTo:       now,
		},
		{
			name:         "case insensitive",
			query:        "LAST 5 HOURS of work",
			wantDetected: true,
			wantFrom:     now.Add(-5 * time.Hour),
			wantTo:       now,
		},

		// --- "last/past unit" (implicit N=1) patterns ---
		{
			name:         "last hour",
			query:        "what happened in the last hour",
			wantDetected: true,
			wantFrom:     now.Add(-1 * time.Hour),
			wantTo:       now,
		},
		{
			name:         "past day",
			query:        "events from the past day",
			wantDetected: true,
			wantFrom:     now.Add(-24 * time.Hour),
			wantTo:       now,
		},
		{
			name:         "past week",
			query:        "show me the past week",
			wantDetected: true,
			wantFrom:     now.Add(-7 * 24 * time.Hour),
			wantTo:       now,
		},
		{
			name:         "last month",
			query:        "last month of changes",
			wantDetected: true,
			wantFrom:     now.Add(-30 * 24 * time.Hour),
			wantTo:       now,
		},

		// --- Named period patterns ---
		{
			name:         "today",
			query:        "what was done today",
			wantDetected: true,
			wantFrom:     time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC),
			wantTo:       now,
		},
		{
			name:         "yesterday",
			query:        "show me yesterday's work",
			wantDetected: true,
			wantFrom:     time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC),
			wantTo:       time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC),
		},
		{
			name:         "this week",
			query:        "what happened this week",
			wantDetected: true,
			wantFrom:     time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC), // Monday
			wantTo:       now,
		},
		{
			name:         "this month",
			query:        "show this month's activity",
			wantDetected: true,
			wantFrom:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			wantTo:       now,
		},

		// --- Implicit recency ---
		{
			name:         "recent",
			query:        "show me recent changes",
			wantDetected: true,
			wantFrom:     now.Add(-24 * time.Hour),
			wantTo:       now,
		},
		{
			name:         "recently",
			query:        "what was recently modified",
			wantDetected: true,
			wantFrom:     now.Add(-24 * time.Hour),
			wantTo:       now,
		},

		// --- Priority: "last N" beats "recent" ---
		{
			name:         "last N takes priority over recent",
			query:        "what recent work in the last 6 hours",
			wantDetected: true,
			wantFrom:     now.Add(-6 * time.Hour),
			wantTo:       now,
		},

		// --- No temporal intent ---
		{
			name:         "no temporal intent - technical query",
			query:        "how does the authentication system work",
			wantDetected: false,
		},
		{
			name:         "no temporal intent - general query",
			query:        "what is the database schema",
			wantDetected: false,
		},
		{
			name:         "no temporal intent - empty query",
			query:        "",
			wantDetected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTemporalIntent(tc.query, now)

			if got.Detected != tc.wantDetected {
				t.Fatalf("Detected = %v, want %v", got.Detected, tc.wantDetected)
			}
			if !tc.wantDetected {
				return
			}
			if !got.From.Equal(tc.wantFrom) {
				t.Errorf("From = %v, want %v", got.From, tc.wantFrom)
			}
			if !got.To.Equal(tc.wantTo) {
				t.Errorf("To = %v, want %v", got.To, tc.wantTo)
			}
		})
	}
}

func TestStartOfWeek(t *testing.T) {
	tests := []struct {
		name string
		date time.Time
		want time.Time
	}{
		{
			name: "wednesday",
			date: time.Date(2026, 2, 25, 14, 30, 0, 0, time.UTC),
			want: time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "monday is itself",
			date: time.Date(2026, 2, 23, 10, 0, 0, 0, time.UTC),
			want: time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "sunday wraps to previous monday",
			date: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
			want: time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "saturday",
			date: time.Date(2026, 2, 28, 23, 59, 0, 0, time.UTC),
			want: time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := startOfWeek(tc.date)
			if !got.Equal(tc.want) {
				t.Errorf("startOfWeek(%v) = %v, want %v (weekday: %v)", tc.date, got, tc.want, tc.date.Weekday())
			}
		})
	}
}

func TestUnitToDuration(t *testing.T) {
	tests := []struct {
		unit string
		n    int
		want time.Duration
	}{
		{"minute", 30, 30 * time.Minute},
		{"hour", 12, 12 * time.Hour},
		{"day", 3, 3 * 24 * time.Hour},
		{"week", 2, 2 * 7 * 24 * time.Hour},
		{"month", 1, 30 * 24 * time.Hour},
		{"unknown", 5, 5 * time.Hour}, // defaults to hours
	}

	for _, tc := range tests {
		t.Run(tc.unit, func(t *testing.T) {
			got := unitToDuration(tc.unit, tc.n)
			if got != tc.want {
				t.Errorf("unitToDuration(%q, %d) = %v, want %v", tc.unit, tc.n, got, tc.want)
			}
		})
	}
}
