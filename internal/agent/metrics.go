package agent

import (
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/hostmetrics"
)

type MetricsCollector = hostmetrics.Collector

func NewMetricsCollector() *MetricsCollector {
	return hostmetrics.NewCollector()
}

func metricsHaveData(metrics core.HostMetrics) bool {
	return hostmetrics.HasData(metrics)
}
