package core

import (
	"errors"
	"net/url"
	"strings"
)

const (
	PublicIPProbeSourceAgent         = "agent-config"
	PublicIPProbeSourceControlPlane  = "control-plane-config"
	DefaultPublicIPProbeIPv4Endpoint = "https://api.ipify.org/"
	DefaultPublicIPProbeIPv4Fallback = "https://4.ident.me"
	DefaultPublicIPProbeIPv6Endpoint = "https://api6.ipify.org"
	DefaultPublicIPProbeIPv6Fallback = "https://6.ident.me"
	publicIPProbeEndpointMaxBytes    = 2048
)

// PublicIPProbeConfig is an operator-controlled, per-family direct egress
// probe configuration. Managed defaults may carry one approved same-family
// fallback after the ipify primary; an explicit operator endpoint replaces its
// complete family chain and never inherits a hidden public fallback.
type PublicIPProbeConfig struct {
	IPv4Endpoint         string `json:"ipv4_endpoint,omitempty"`
	IPv4FallbackEndpoint string `json:"ipv4_fallback_endpoint,omitempty"`
	IPv6Endpoint         string `json:"ipv6_endpoint,omitempty"`
	IPv6FallbackEndpoint string `json:"ipv6_fallback_endpoint,omitempty"`
	IntervalSeconds      uint32 `json:"interval_seconds,omitempty"`
}

func (config PublicIPProbeConfig) Validate() error {
	if config.IntervalSeconds != 0 && (config.IntervalSeconds < 60 || config.IntervalSeconds > 24*60*60) {
		return errors.New("public IP probe interval must be between 60 and 86400 seconds")
	}
	for index, endpoint := range []string{config.IPv4Endpoint, config.IPv4FallbackEndpoint, config.IPv6Endpoint, config.IPv6FallbackEndpoint} {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" {
			continue
		}
		if len(endpoint) > publicIPProbeEndpointMaxBytes {
			return errors.New("public IP probe endpoint exceeds the length limit")
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("public IP probe endpoint must be an absolute HTTPS URL without credentials, query, or fragment")
		}
		if index == 1 && endpoint != DefaultPublicIPProbeIPv4Fallback || index == 3 && endpoint != DefaultPublicIPProbeIPv6Fallback {
			return errors.New("public IP probe fallback endpoint is not an approved same-family endpoint")
		}
	}
	if fallback := strings.TrimSpace(config.IPv4FallbackEndpoint); fallback != "" && strings.TrimSpace(config.IPv4Endpoint) != DefaultPublicIPProbeIPv4Endpoint {
		return errors.New("public IP probe IPv4 fallback requires the approved ipify primary")
	}
	if fallback := strings.TrimSpace(config.IPv6FallbackEndpoint); fallback != "" && strings.TrimSpace(config.IPv6Endpoint) != DefaultPublicIPProbeIPv6Endpoint {
		return errors.New("public IP probe IPv6 fallback requires the approved ipify primary")
	}
	return nil
}
