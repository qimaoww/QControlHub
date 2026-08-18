package serverconfig

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type staticRealityResolver struct {
	cname     string
	addresses []netip.Addr
	cnameErr  error
	lookupErr error
}

func (resolver staticRealityResolver) LookupCNAME(context.Context, string) (string, error) {
	return resolver.cname, resolver.cnameErr
}

func (resolver staticRealityResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return resolver.addresses, resolver.lookupErr
}

func TestRealityPresetsExcludeMicrosoftAndCloudflare(t *testing.T) {
	t.Parallel()
	presets := RealityServerNamePresets()
	if len(presets) < 3 || DefaultRealityServerName == "" {
		t.Fatalf("Reality presets are unexpectedly empty: %v", presets)
	}
	for _, preset := range presets {
		lower := strings.ToLower(preset)
		if strings.Contains(lower, "microsoft") || isCloudflareName(lower) {
			t.Fatalf("unsafe or retired Reality preset published: %q", preset)
		}
	}
	presets[0] = "changed.example"
	if RealityServerNamePresets()[0] == "changed.example" {
		t.Fatal("RealityServerNamePresets returned mutable package state")
	}
}

func TestNormalizeRealityServerName(t *testing.T) {
	t.Parallel()
	if got, err := normalizeRealityServerName("  WWW.Example.COM. "); err != nil || got != "www.example.com" {
		t.Fatalf("normalizeRealityServerName() = %q, %v", got, err)
	}
	for _, invalid := range []string{"", "localhost", "127.0.0.1", "[::1]", "example.com:443", "https://example.com", "bad_name.example", "-bad.example", "www.microsoft.com", "www.cloudflare.com"} {
		if _, err := normalizeRealityServerName(invalid); err == nil {
			t.Errorf("normalizeRealityServerName(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestProbeRealityTargetRejectsUnsafeDNSBeforeDial(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		cname     string
		addresses []netip.Addr
		want      string
	}{
		{"loopback", "safe.example.", []netip.Addr{netip.MustParseAddr("127.0.0.1")}, "非公网地址"},
		{"private", "safe.example.", []netip.Addr{netip.MustParseAddr("10.0.0.8")}, "非公网地址"},
		{"mixed DNS answer", "safe.example.", []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("192.168.1.10")}, "非公网地址"},
		{"documentation range", "safe.example.", []netip.Addr{netip.MustParseAddr("203.0.113.10")}, "非公网地址"},
		{"Cloudflare IPv4", "safe.example.", []netip.Addr{netip.MustParseAddr("104.16.10.1")}, "Cloudflare 地址"},
		{"Cloudflare IPv6", "safe.example.", []netip.Addr{netip.MustParseAddr("2606:4700::1234")}, "Cloudflare 地址"},
		{"Cloudflare CNAME", "customer.cdn.cloudflare.net.", []netip.Addr{netip.MustParseAddr("93.184.216.34")}, "Cloudflare 域名或 CNAME"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var dialed atomic.Bool
			_, err := probeRealityTarget(context.Background(), "safe.example", realityProbeOptions{
				resolver: staticRealityResolver{cname: test.cname, addresses: test.addresses},
				dialContext: func(context.Context, string, string) (net.Conn, error) {
					dialed.Store(true)
					return nil, context.Canceled
				},
				totalTimeout: time.Second, connectionTimeout: time.Second,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("probeRealityTarget() error = %v, want %q", err, test.want)
			}
			if dialed.Load() {
				t.Fatal("probe dialed a target before DNS safety checks completed")
			}
		})
	}
}

func TestProbeRealityTargetPinsResolvedIPAndVerifiesTLS13(t *testing.T) {
	t.Parallel()
	const serverName = "reality.example"
	certificate, roots := newRealityTestCertificate(t, serverName)
	publicAddress := netip.MustParseAddr("93.184.216.34")

	serverErr := make(chan error, 1)
	result, err := probeRealityTarget(context.Background(), " Reality.Example. ", realityProbeOptions{
		resolver: staticRealityResolver{cname: "origin.reality.example.", addresses: []netip.Addr{publicAddress}},
		dialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != "93.184.216.34:443" {
				t.Fatalf("probe dialed %s %s instead of the pinned DNS address", network, address)
			}
			client, server := net.Pipe()
			go func() {
				tlsServer := tls.Server(server, &tls.Config{
					Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13,
					NextProtos: []string{"h2", "http/1.1"},
				})
				serverErr <- tlsServer.Handshake()
				_ = tlsServer.Close()
			}()
			return client, nil
		},
		rootCAs: roots, totalTimeout: 2 * time.Second, connectionTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("probeRealityTarget() error = %v", err)
	}
	if result.ServerName != serverName || result.Address != publicAddress || result.CNAME != "origin.reality.example" || result.ALPN != "h2" {
		t.Fatalf("probeRealityTarget() = %+v", result)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test TLS server handshake: %v", err)
	}
}

func newRealityTestCertificate(t *testing.T, serverName string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "QControlHub test CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: serverName}, DNSNames: []string{serverName},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCertificate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(caCertificate)
	return tls.Certificate{Certificate: [][]byte{serverDER, caDER}, PrivateKey: serverKey}, roots
}
