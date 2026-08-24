package store

import (
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

const maxReportedMetricBytes = uint64(1 << 60)

func encodeHeartbeatMetrics(input *core.HostMetrics, receivedAt time.Time) ([]byte, error) {
	if input == nil {
		return nil, nil
	}
	metrics := *input
	metrics.CollectedAt = receivedAt.UTC()
	if metrics.CPUAvailable {
		if math.IsNaN(metrics.CPUPercent) || math.IsInf(metrics.CPUPercent, 0) || metrics.CPUPercent < 0 || metrics.CPUPercent > 100 {
			return nil, errors.New("agent reported invalid CPU usage")
		}
	} else {
		metrics.CPUPercent = 0
	}
	if metrics.MemoryAvailable {
		if metrics.MemoryTotalBytes == 0 || metrics.MemoryTotalBytes > maxReportedMetricBytes || metrics.MemoryUsedBytes > metrics.MemoryTotalBytes {
			return nil, errors.New("agent reported invalid memory usage")
		}
	} else {
		metrics.MemoryUsedBytes, metrics.MemoryTotalBytes = 0, 0
	}
	if metrics.DiskAvailable {
		if metrics.DiskTotalBytes == 0 || metrics.DiskTotalBytes > maxReportedMetricBytes || metrics.DiskUsedBytes > metrics.DiskTotalBytes {
			return nil, errors.New("agent reported invalid disk usage")
		}
	} else {
		metrics.DiskUsedBytes, metrics.DiskTotalBytes = 0, 0
	}
	if metrics.NetworkAvailable {
		if metrics.NetworkRXBytes > maxReportedMetricBytes || metrics.NetworkTXBytes > maxReportedMetricBytes || metrics.NetworkRXBPS > maxReportedMetricBytes || metrics.NetworkTXBPS > maxReportedMetricBytes {
			return nil, errors.New("agent reported invalid network usage")
		}
	} else {
		metrics.NetworkRXBytes, metrics.NetworkTXBytes, metrics.NetworkRXBPS, metrics.NetworkTXBPS = 0, 0, 0, 0
	}
	if len(metrics.NetworkInterfaces) > 16 {
		return nil, errors.New("agent reported too many network interfaces")
	}
	for _, networkInterface := range metrics.NetworkInterfaces {
		if !validReportedInterfaceName(networkInterface.Name) || len(networkInterface.Addresses) == 0 || len(networkInterface.Addresses) > 16 {
			return nil, errors.New("agent reported invalid network interface")
		}
		for _, address := range networkInterface.Addresses {
			ip := net.ParseIP(strings.TrimSpace(address))
			if ip == nil || !ip.IsGlobalUnicast() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				return nil, errors.New("agent reported invalid network interface address")
			}
		}
	}
	if value := strings.TrimSpace(metrics.ObservedPublicIP); value != "" {
		metrics.ObservedPublicIP = authn.NormalizePublicIP(value)
		if metrics.ObservedPublicIP == "" {
			return nil, errors.New("control plane observed invalid public agent address")
		}
	} else {
		metrics.ObservedPublicIP = ""
	}
	if err := validateProbedPublicAddress(&metrics.PublicIPv4, true); err != nil {
		return nil, err
	}
	if err := validateProbedPublicAddress(&metrics.PublicIPv6, false); err != nil {
		return nil, err
	}
	return json.Marshal(metrics)
}

// validateProbedPublicAddress normalizes an Agent-probed egress address and
// verifies it belongs to the expected family. Empty stays empty so older
// Agents that never probe keep working.
func validateProbedPublicAddress(value *string, wantIPv4 bool) error {
	if strings.TrimSpace(*value) == "" {
		*value = ""
		return nil
	}
	normalized := authn.NormalizePublicIP(*value)
	if normalized == "" {
		return errors.New("agent reported an invalid probed public address")
	}
	address, err := netip.ParseAddr(normalized)
	if err != nil || address.Is4() != wantIPv4 {
		if wantIPv4 {
			return errors.New("agent reported a non-IPv4 public IPv4 address")
		}
		return errors.New("agent reported a non-IPv6 public IPv6 address")
	}
	if netpolicy.IsCloudflareAddress(address) {
		// A relay can be globally routable but is not the Agent's egress. Clear
		// it so callers retain the interface or verified-WSS fallback instead of
		// rejecting the entire heartbeat/metrics update or surfacing the relay.
		*value = ""
		return nil
	}
	*value = normalized
	return nil
}

func validReportedInterfaceName(name string) bool {
	if name == "" || name == "lo" || len(name) > 64 || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("_.:-", character) {
			continue
		}
		return false
	}
	return true
}
