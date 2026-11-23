package store

import (
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// A row is indexed by the last possible UTC date. Bytes spanning several
// dates are stored separately from the amount attributable to that day.
type trafficDayAllocation struct {
	Day             string `json:"day"`
	Since           string `json:"since,omitempty"`
	Received        uint64 `json:"received"`
	Sent            uint64 `json:"sent"`
	UndatedReceived uint64 `json:"undated_received"`
	UndatedSent     uint64 `json:"undated_sent"`
}

func allocateTrafficDay(first, last time.Time, received, sent uint64) trafficDayAllocation {
	row := trafficDayAllocation{Day: last.UTC().Format(time.DateOnly), Received: received, Sent: sent}
	if since := first.UTC().Format(time.DateOnly); since < row.Day && (received > 0 || sent > 0) {
		row.Since = since
		row.UndatedReceived, row.UndatedSent = received, sent
		row.Received, row.Sent = 0, 0
	}
	return row
}

func trafficSampleDays(usage core.PortTrafficUsage, previous *time.Time, sampledAt time.Time, received, sent uint64) []trafficDayAllocation {
	// Older Agents provide arrival-based totals without a collection timestamp.
	if usage.CollectedAt.IsZero() {
		return []trafficDayAllocation{allocateTrafficDay(sampledAt, sampledAt, received, sent)}
	}
	first := usage.PeriodStart
	if previous != nil {
		first = previous.UTC()
	}
	if received > usage.ReceivedBytes || sent > usage.SentBytes {
		// A restored epoch can predate the last policy sample. When its
		// beginning is unavailable, the previous tail has no lower date bound.
		if previous == nil || !first.Before(usage.PeriodStart) {
			first = time.Time{}
		}
	}
	if first.Before(usage.PeriodStart) {
		// Within this epoch only the Agent's current-period counters can
		// belong to the new period. Preserve the remainder before rollover.
		rx, tx := min(received, usage.ReceivedBytes), min(sent, usage.SentBytes)
		if rx < received || tx < sent {
			tail := allocateTrafficDay(first, usage.PeriodStart.Add(-time.Nanosecond), received-rx, sent-tx)
			return []trafficDayAllocation{tail, allocateTrafficDay(usage.PeriodStart, sampledAt, rx, tx)}
		}
		first = usage.PeriodStart
	}
	return []trafficDayAllocation{allocateTrafficDay(first, sampledAt, received, sent)}
}
