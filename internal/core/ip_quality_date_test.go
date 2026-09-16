package core

import (
	"testing"
	"time"
)

func TestIPQualityDateRangeCalendarTransitions(t *testing.T) {
	for _, test := range []struct {
		date, zone, start, end string
	}{
		{"2026-09-06", "America/Santiago", "2026-09-06T04:00:00Z", "2026-09-07T03:00:00Z"},
		{"2026-04-04", "America/Santiago", "2026-04-04T03:00:00Z", "2026-04-05T04:00:00Z"},
		{"2026-03-29", "Asia/Beirut", "2026-03-28T22:00:00Z", "2026-03-29T21:00:00Z"},
		{"2026-10-24", "Asia/Beirut", "2026-10-23T21:00:00Z", "2026-10-24T22:00:00Z"},
		{"2026-11-01", "America/Havana", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z"},
		{"2026-10-04", "Australia/Lord_Howe", "2026-10-03T13:30:00Z", "2026-10-04T13:00:00Z"},
		{"2011-12-29", "Pacific/Apia", "2011-12-29T10:00:00Z", "2011-12-30T10:00:00Z"},
		{"2011-12-30", "Pacific/Apia", "2011-12-30T10:00:00Z", "2011-12-30T10:00:00Z"},
		{"2011-12-31", "Pacific/Apia", "2011-12-30T10:00:00Z", "2011-12-31T10:00:00Z"},
		{"2040-12-31", "America/New_York", "2040-12-31T05:00:00Z", "2041-01-01T05:00:00Z"},
		{"2040-12-31", "Australia/Sydney", "2040-12-30T13:00:00Z", "2040-12-31T13:00:00Z"},
	} {
		t.Run(test.date+"/"+test.zone, func(t *testing.T) {
			start, end, err := IPQualityDateRange(test.date, test.zone)
			if err != nil {
				t.Fatal(err)
			}
			if start.Format(time.RFC3339) != test.start || end.Format(time.RFC3339) != test.end {
				t.Fatalf("actual=[%s,%s), expected=[%s,%s)", start, end, test.start, test.end)
			}
			day, err := time.Parse(time.DateOnly, test.date)
			if err != nil {
				t.Fatal(err)
			}
			next, _, err := IPQualityDateRange(day.AddDate(0, 0, 1).Format(time.DateOnly), test.zone)
			if err != nil || !end.Equal(next) {
				t.Fatalf("adjacent days overlap or leave a gap: end=%s next=%s error=%v", end, next, err)
			}
		})
	}
}
