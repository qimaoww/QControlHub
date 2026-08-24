//go:build linux

package main

import (
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPublicIPProbeConfigFromValues(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		ipv4    string
		ipv6    string
		want    core.PublicIPProbeConfig
	}{
		{
			name:    "enabled defaults both families",
			enabled: true,
			want: core.PublicIPProbeConfig{
				IPv4Endpoint:         core.DefaultPublicIPProbeIPv4Endpoint,
				IPv4FallbackEndpoint: core.DefaultPublicIPProbeIPv4Fallback,
				IPv6Endpoint:         core.DefaultPublicIPProbeIPv6Endpoint,
				IPv6FallbackEndpoint: core.DefaultPublicIPProbeIPv6Fallback,
				IntervalSeconds:      300,
			},
		},
		{
			name:    "custom family overrides keep the other default",
			enabled: true,
			ipv4:    " https://probe.example.test/v4 ",
			want: core.PublicIPProbeConfig{
				IPv4Endpoint:         "https://probe.example.test/v4",
				IPv6Endpoint:         core.DefaultPublicIPProbeIPv6Endpoint,
				IPv6FallbackEndpoint: core.DefaultPublicIPProbeIPv6Fallback,
				IntervalSeconds:      300,
			},
		},
		{
			name:    "disabled clears both families",
			enabled: false,
			ipv4:    "https://probe.example.test/v4",
			ipv6:    "https://probe.example.test/v6",
			want: core.PublicIPProbeConfig{
				IntervalSeconds: 300,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := publicIPProbeConfigFromValues(test.enabled, 5*time.Minute, test.ipv4, test.ipv6)
			if got != test.want {
				t.Fatalf("publicIPProbeConfigFromValues() = %+v, want %+v", got, test.want)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("default/configured probe config is invalid: %v", err)
			}
		})
	}
}

func TestPublicIPProbeConfigFromEnvDefaultsAndOptOut(t *testing.T) {
	for _, key := range []string{
		publicIPProbeEnabledEnv,
		"QCH_AGENT_PUBLIC_IP_PROBE_IPV4_ENDPOINT",
		"QCH_AGENT_PUBLIC_IP_PROBE_IPV6_ENDPOINT",
	} {
		t.Setenv(key, "")
	}
	defaultConfig := publicIPProbeConfigFromEnv(5 * time.Minute)
	if defaultConfig.IPv4Endpoint != core.DefaultPublicIPProbeIPv4Endpoint || defaultConfig.IPv4FallbackEndpoint != core.DefaultPublicIPProbeIPv4Fallback || defaultConfig.IPv6Endpoint != core.DefaultPublicIPProbeIPv6Endpoint || defaultConfig.IPv6FallbackEndpoint != core.DefaultPublicIPProbeIPv6Fallback {
		t.Fatalf("empty/unset control-plane settings = %+v, want ident.me defaults", defaultConfig)
	}

	t.Setenv(publicIPProbeEnabledEnv, "false")
	disabledConfig := publicIPProbeConfigFromEnv(5 * time.Minute)
	if disabledConfig.IPv4Endpoint != "" || disabledConfig.IPv6Endpoint != "" {
		t.Fatalf("disabled control-plane probe = %+v, want both endpoints empty", disabledConfig)
	}

	t.Setenv(publicIPProbeEnabledEnv, "0")
	zeroDisabledConfig := publicIPProbeConfigFromEnv(5 * time.Minute)
	if zeroDisabledConfig.IPv4Endpoint != "" || zeroDisabledConfig.IPv6Endpoint != "" {
		t.Fatalf("zero-disabled control-plane probe = %+v, want both endpoints empty", zeroDisabledConfig)
	}
}
