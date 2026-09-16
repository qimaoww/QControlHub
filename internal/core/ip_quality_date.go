package core

import (
	"errors"
	"time"
	_ "time/tzdata" // Date-scoped history also works on minimal panel images.
)

// IPQualityDateRange uses local calendar boundaries (including DST), while
// persistence and task timestamps remain UTC. Empty timezone means UTC.
func IPQualityDateRange(date, timezone string) (time.Time, time.Time, error) {
	if len(date) != len(time.DateOnly) {
		return time.Time{}, time.Time{}, errors.New("date must use YYYY-MM-DD")
	}
	day, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("date must be a valid YYYY-MM-DD")
	}
	if timezone == "" {
		timezone = "UTC"
	}
	if len(timezone) > 100 || timezone == "Local" {
		return time.Time{}, time.Time{}, errors.New("timezone must be an IANA time zone")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("timezone must be an IANA time zone")
	}
	// Compute each boundary from its requested calendar date, not from a
	// midnight that time.Date may have normalized into another hour or day.
	return ipQualityDateBoundary(day, location), ipQualityDateBoundary(day.AddDate(0, 0, 1), location), nil
}

func ipQualityDateBoundary(day time.Time, location *time.Location) time.Time {
	// IANA offsets fit within this two-day margin. Walk actual constant-offset
	// intervals to select the first midnight in a fold, or the transition
	// instant if midnight was skipped. An entirely skipped date has an empty
	// range because both calendar boundaries resolve to the same transition.
	cursor := day.Add(-48 * time.Hour).In(location)
	for {
		_, offset := cursor.Zone()
		boundary := day.Add(-time.Duration(offset) * time.Second)
		if boundary.Before(cursor) {
			boundary = cursor
		}
		_, end := cursor.ZoneBounds()
		if !end.IsZero() && !end.After(cursor) {
			// Go's POSIX extrapolation can report a synthetic 365-day year
			// end on December 31 of a leap year. Do not revisit that bound.
			end = time.Date(cursor.UTC().Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC).In(location)
		}
		if end.IsZero() || boundary.Before(end) {
			return boundary.UTC()
		}
		cursor = end
	}
}
