package core

import (
	"time"
)

type HostMetrics struct {
	BBR               *SystemBBRStatus       `json:"bbr,omitempty"`
	CollectedAt       time.Time              `json:"collected_at"`
	CPUAvailable      bool                   `json:"cpu_available"`
	CPUPercent        float64                `json:"cpu_percent"`
	MemoryAvailable   bool                   `json:"memory_available"`
	MemoryUsedBytes   uint64                 `json:"memory_used_bytes"`
	MemoryTotalBytes  uint64                 `json:"memory_total_bytes"`
	DiskAvailable     bool                   `json:"disk_available"`
	DiskUsedBytes     uint64                 `json:"disk_used_bytes"`
	DiskTotalBytes    uint64                 `json:"disk_total_bytes"`
	NetworkAvailable  bool                   `json:"network_available"`
	NetworkRXBytes    uint64                 `json:"network_rx_bytes"`
	NetworkTXBytes    uint64                 `json:"network_tx_bytes"`
	NetworkRXBPS      uint64                 `json:"network_rx_bps"`
	NetworkTXBPS      uint64                 `json:"network_tx_bps"`
	NetworkInterfaces []HostNetworkInterface `json:"network_interfaces,omitempty"`
	// ObservedPublicIP is assigned by the control plane from the authenticated
	// WSS source. Agent-provided values are never trusted. It only ever
	// reflects the address family the WSS connection itself used.
	ObservedPublicIP string `json:"observed_public_ip,omitempty"`
	// PublicIPv4 and PublicIPv6 are the egress addresses the Agent probed
	// outbound over each family. Together with ObservedPublicIP they close the
	// single-family blind spot: a node that connects over IPv4 still reports
	// its routable IPv6, and vice versa.
	PublicIPv4 string `json:"public_ipv4,omitempty"`
	PublicIPv6 string `json:"public_ipv6,omitempty"`
	// Probe sources are fixed audit enums and never contain endpoint URLs.
	PublicIPv4Source string `json:"public_ipv4_source,omitempty"`
	PublicIPv6Source string `json:"public_ipv6_source,omitempty"`
}

// HostNetworkInterface describes addresses assigned to an interface that
// carries a default route. Agents only report usable unicast addresses; the
// control plane never needs wildcard, loopback, multicast or link-local
// addresses to generate client connection details.
type HostNetworkInterface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses,omitempty"`
}

// MetricSample is one historical resource sample recorded for an agent.
// Rates are stored in bytes per second and percentages as 0-100 values.
type MetricSample struct {
	SampledAt     time.Time `json:"sampled_at"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemoryPercent float64   `json:"memory_percent"`
	RXRateBPS     uint64    `json:"rx_rate_bps"`
	TXRateBPS     uint64    `json:"tx_rate_bps"`
}
