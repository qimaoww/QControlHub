//go:build linux

package agent

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

var chronyOffsetPattern = regexp.MustCompile(`(?m)^Last offset\s*:\s*([+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+))\s+seconds\b`)

func collectClockSync(ctx context.Context) *core.ClockSyncStatus {
	checkedAt := time.Now().UTC()
	if path, err := exec.LookPath("chronyc"); err == nil {
		commandContext, cancel := context.WithTimeout(ctx, 2*time.Second)
		output, runErr := exec.CommandContext(commandContext, path, "tracking").Output()
		cancel()
		if runErr == nil {
			return parseChronyTracking(string(output), checkedAt)
		}
	}
	if path, err := exec.LookPath("timedatectl"); err == nil {
		commandContext, cancel := context.WithTimeout(ctx, 2*time.Second)
		output, runErr := exec.CommandContext(commandContext, path, "show", "--property=NTPSynchronized", "--value").Output()
		cancel()
		if runErr == nil {
			status := "unsynchronized"
			if strings.EqualFold(strings.TrimSpace(string(output)), "yes") {
				status = "synchronized"
			}
			return &core.ClockSyncStatus{Status: status, CheckedAt: checkedAt}
		}
	}
	return &core.ClockSyncStatus{Status: "unavailable", CheckedAt: checkedAt}
}

func (c *Client) cachedClockSync(ctx context.Context) *core.ClockSyncStatus {
	c.clockSyncMu.Lock()
	defer c.clockSyncMu.Unlock()
	if c.clockSync != nil && time.Since(c.clockSync.CheckedAt) < 30*time.Second {
		copy := *c.clockSync
		if c.clockSync.OffsetMilliseconds != nil {
			offset := *c.clockSync.OffsetMilliseconds
			copy.OffsetMilliseconds = &offset
		}
		return &copy
	}
	c.clockSync = collectClockSync(ctx)
	copy := *c.clockSync
	if c.clockSync.OffsetMilliseconds != nil {
		offset := *c.clockSync.OffsetMilliseconds
		copy.OffsetMilliseconds = &offset
	}
	return &copy
}

func parseChronyTracking(output string, checkedAt time.Time) *core.ClockSyncStatus {
	status := "unsynchronized"
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "Leap status" && strings.EqualFold(strings.TrimSpace(value), "Normal") {
			status = "synchronized"
			break
		}
	}
	result := &core.ClockSyncStatus{Status: status, CheckedAt: checkedAt}
	if match := chronyOffsetPattern.FindStringSubmatch(output); len(match) == 2 {
		if seconds, err := strconv.ParseFloat(match[1], 64); err == nil {
			milliseconds := seconds * 1000
			result.OffsetMilliseconds = &milliseconds
		}
	}
	return result
}
