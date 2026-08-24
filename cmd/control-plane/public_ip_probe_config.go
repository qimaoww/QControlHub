//go:build linux

package main

import (
	"os"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const publicIPProbeEnabledEnv = "QCH_AGENT_PUBLIC_IP_PROBE_ENABLED"

func publicIPProbeConfigFromEnv(interval time.Duration) core.PublicIPProbeConfig {
	return publicIPProbeConfigFromValues(
		envBool(publicIPProbeEnabledEnv, true),
		interval,
		os.Getenv("QCH_AGENT_PUBLIC_IP_PROBE_IPV4_ENDPOINT"),
		os.Getenv("QCH_AGENT_PUBLIC_IP_PROBE_IPV6_ENDPOINT"),
	)
}

func publicIPProbeConfigFromValues(enabled bool, interval time.Duration, ipv4Endpoint, ipv6Endpoint string) core.PublicIPProbeConfig {
	config := core.PublicIPProbeConfig{IntervalSeconds: uint32(interval / time.Second)}
	if !enabled {
		return config
	}
	config.IPv4Endpoint = strings.TrimSpace(ipv4Endpoint)
	if config.IPv4Endpoint == "" {
		config.IPv4Endpoint = core.DefaultPublicIPProbeIPv4Endpoint
	}
	config.IPv6Endpoint = strings.TrimSpace(ipv6Endpoint)
	if config.IPv6Endpoint == "" {
		config.IPv6Endpoint = core.DefaultPublicIPProbeIPv6Endpoint
	}
	return config
}
