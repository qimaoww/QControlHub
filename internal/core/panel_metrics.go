package core

import "time"

// PanelMetricSample stores one lightweight in-memory host telemetry point for
// rolling dashboard sparklines without adding database or procfs overhead.
type PanelMetricSample struct {
	CollectedAt          time.Time `json:"collected_at"`
	CPUAvailable         bool      `json:"cpu_available"`
	CPUPercent           float64   `json:"cpu_percent"`
	MemoryAvailable      bool      `json:"memory_available"`
	MemoryUsedBytes      uint64    `json:"memory_used_bytes"`
	MemoryTotalBytes     uint64    `json:"memory_total_bytes"`
	DiskAvailable        bool      `json:"disk_available,omitempty"`
	DiskUsedBytes        uint64    `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes       uint64    `json:"disk_total_bytes,omitempty"`
	NetworkAvailable     bool      `json:"network_available"`
	NetworkRateAvailable *bool     `json:"network_rate_available,omitempty"`
	NetworkRXBPS         uint64    `json:"network_rx_bps"`
	NetworkTXBPS         uint64    `json:"network_tx_bps"`
}

// PanelRuntimeStatus exposes non-sensitive control-plane sampler state and
// process runtime counters alongside /panel-metrics.
type PanelRuntimeStatus struct {
	SampleIntervalSeconds int    `json:"sample_interval_seconds"`
	SampleWindowCount     int    `json:"sample_window_count"`
	SampleWindowCapacity  int    `json:"sample_window_capacity"`
	Goroutines            int    `json:"goroutines,omitempty"`
	GoVersion             string `json:"go_version,omitempty"`
	ProcessUptimeSeconds  int64  `json:"process_uptime_seconds,omitempty"`
}

// PanelMetricsSnapshot extends HostMetrics at the top level with recent
// in-memory samples and runtime health while keeping legacy HostMetrics fields.
type PanelMetricsSnapshot struct {
	HostMetrics
	History      []PanelMetricSample `json:"history,omitempty"`
	PanelRuntime *PanelRuntimeStatus `json:"panel_runtime,omitempty"`
}
