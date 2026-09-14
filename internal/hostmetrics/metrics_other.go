//go:build !linux

package hostmetrics

import (
	"context"
	"errors"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func collectHostMetrics(ctx context.Context, previous metricSample) (core.HostMetrics, metricSample, error) {
	if err := ctx.Err(); err != nil {
		return core.HostMetrics{}, previous, err
	}
	return core.HostMetrics{}, previous, errors.New("host metrics require Linux")
}
