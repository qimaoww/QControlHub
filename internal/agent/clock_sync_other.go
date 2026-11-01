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

func (c *Client) cachedClockSync(ctx context.Context) *core.ClockSyncStatus {
	c.clockSyncMu.Lock()
	defer c.clockSyncMu.Unlock()
	if c.clockSync == nil || time.Since(c.clockSync.CheckedAt) >= 30*time.Second {
		c.clockSync = collectClockSync(ctx)
	}
	copy := *c.clockSync
	return &copy
}
