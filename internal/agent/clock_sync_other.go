//go:build !linux

package agent

import (
	"context"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func collectClockSync(context.Context) *core.ClockSyncStatus {
	return &core.ClockSyncStatus{Status: "unavailable", CheckedAt: time.Now().UTC()}
}

func cloneClockSync(src *core.ClockSyncStatus) *core.ClockSyncStatus {
	if src == nil {
		return nil
	}
	copyStatus := *src
	if src.OffsetMilliseconds != nil {
		offset := *src.OffsetMilliseconds
		copyStatus.OffsetMilliseconds = &offset
	}
	return &copyStatus
}

func (c *Client) cachedClockSync(ctx context.Context) *core.ClockSyncStatus {
	c.clockSyncMu.Lock()
	defer c.clockSyncMu.Unlock()
	if c.clockSync == nil || time.Since(c.clockSync.CheckedAt) >= 30*time.Second {
		c.clockSync = collectClockSync(ctx)
	}
	return cloneClockSync(c.clockSync)
}

func (c *Client) cachedClockSyncSnapshot(ctx context.Context) *core.ClockSyncStatus {
	return c.cachedClockSync(ctx)
}
