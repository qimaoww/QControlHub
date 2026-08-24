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
