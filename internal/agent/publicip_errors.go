package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
)

// publicIPProbeErrorCategory maps an endpoint failure to a bounded, log-safe
// label. A raw *url.Error formats the full endpoint URL and any underlying
// network address, so it must never be logged directly; only the category is
// retained alongside the family and provider index.
func publicIPProbeErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	var classified *publicIPProbeError
	if errors.As(err, &classified) {
		return classified.category
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var certificateErr *tls.CertificateVerificationError
	if errors.As(err, &certificateErr) {
		return "tls"
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		if networkErr.Timeout() {
			return "timeout"
		}
		return "network"
	}
	return "http"
}

// publicIPProbeSafeError collapses a transport or parse failure into a bounded
// error that never carries the configured endpoint URL, an addressed host or
// path, or a response address. The returned error stays a restricted type the
// log classifier can re-identify, so its category survives the boundary exactly
// once instead of being re-derived into a generic http outcome.
func publicIPProbeSafeError(err error) error {
	if err == nil {
		return nil
	}
	return &publicIPProbeError{category: publicIPProbeErrorCategory(err)}
}

// publicIPProbeError is a restricted, point-in-time classification of an
// endpoint failure. It carries only a bounded category and never the endpoint
// URL, an addressed host or path, or a response address.
type publicIPProbeError struct {
	category string
}

func (err *publicIPProbeError) Error() string {
	return err.category
}
