package agent

import (
	"context"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (c *Client) clientConnectionReport(ctx context.Context) *core.ClientConnectionReport {
	c.connectionSampleMu.Lock()
	defer c.connectionSampleMu.Unlock()
	if time.Now().Before(c.nextConnectionSample) {
		return nil
	}
	report := c.executor.collectClientConnections(ctx)
	c.nextConnectionSample = time.Now().Add(15 * time.Second)
	return report
}
