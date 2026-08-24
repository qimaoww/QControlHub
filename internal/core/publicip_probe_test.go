package core

import "testing"

func TestPublicIPProbeConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  PublicIPProbeConfig
		wantErr bool
	}{
		{name: "disabled"},
		{name: "dual stack", config: PublicIPProbeConfig{IPv4Endpoint: "https://probe.example.test/v4", IPv6Endpoint: "https://probe.example.test/v6", IntervalSeconds: 300}},
		{name: "cleartext", config: PublicIPProbeConfig{IPv4Endpoint: "http://probe.example.test/v4"}, wantErr: true},
		{name: "credentials", config: PublicIPProbeConfig{IPv4Endpoint: "https://user:secret@probe.example.test/v4"}, wantErr: true},
		{name: "query", config: PublicIPProbeConfig{IPv4Endpoint: "https://probe.example.test/v4?token=secret"}, wantErr: true},
		{name: "too frequent", config: PublicIPProbeConfig{IPv4Endpoint: "https://probe.example.test/v4", IntervalSeconds: 59}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.config.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr=%v", err, test.wantErr)
			}
		})
	}
}

func TestDefaultPublicIPProbeEndpointsAreFamilyBoundAndValid(t *testing.T) {
	config := PublicIPProbeConfig{
		IPv4Endpoint:    DefaultPublicIPProbeIPv4Endpoint,
		IPv6Endpoint:    DefaultPublicIPProbeIPv6Endpoint,
		IntervalSeconds: 300,
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("default ident.me probe config is invalid: %v", err)
	}
	if config.IPv4Endpoint != "https://4.ident.me" || config.IPv6Endpoint != "https://6.ident.me" {
		t.Fatalf("default probe endpoints = %+v, want family-specific ident.me endpoints", config)
	}
}
