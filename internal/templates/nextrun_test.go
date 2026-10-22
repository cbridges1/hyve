package templates

import (
	"testing"
	"time"
)

func TestNextRunZones(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-10-21 12:00 in Chicago (CDT) is 17:00 UTC.
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, chicago)
	for sched, want := range map[string]string{
		// Plain cron is UTC, whatever now's zone.
		"0 1 * * *": "2026-10-22T01:00:00Z",
		// CRON_TZ names the zone: 20:00 CDT is 01:00 UTC.
		"CRON_TZ=America/Chicago 0 20 * * *": "2026-10-22T01:00:00Z",
		"CRON_TZ=America/Chicago 0 9 * * *":  "2026-10-22T14:00:00Z",
	} {
		got, err := NextRun(sched, now)
		if err != nil {
			t.Fatal(err)
		}
		if s := got.UTC().Format(time.RFC3339); s != want {
			t.Errorf("NextRun(%q) = %s, want %s", sched, s, want)
		}
	}
}
