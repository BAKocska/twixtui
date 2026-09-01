package humantime

import (
	"strings"
	"testing"
	"time"
)

// TestAgoAgreesWithItsNumbers is the reason this package exists: a count and its
// unit have to agree, so that one minute is never reported as "1 minutes".
func TestAgoAgreesWithItsNumbers(t *testing.T) {
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{95 * time.Second, "1 minute ago"},
		{2 * time.Minute, "2 minutes ago"},
		{59 * time.Minute, "59 minutes ago"},
		{90 * time.Minute, "1 hour ago"},
		{5 * time.Hour, "5 hours ago"},
		{30 * time.Hour, "yesterday"},
		{72 * time.Hour, "3 days ago"},
		{29 * 24 * time.Hour, "29 days ago"},
	} {
		if got := Ago(now, now.Add(-tc.ago)); got != tc.want {
			t.Errorf("%s ago rendered as %q, want %q", tc.ago, got, tc.want)
		}
	}
}

// TestAgoGivesADateOnceCountingDaysStopsHelping checks the far end of the scale
// names the day rather than a count nobody can convert.
func TestAgoGivesADateOnceCountingDaysStopsHelping(t *testing.T) {
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	got := Ago(now, now.Add(-100*24*time.Hour))
	if strings.Contains(got, "ago") {
		t.Errorf("a date was expected for an old moment, got %q", got)
	}
	if !strings.Contains(got, "2026") {
		t.Errorf("the date should carry the year, got %q", got)
	}
}
