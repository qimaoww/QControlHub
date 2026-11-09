package serverconfig

import (
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestXHTTPSplitMLDSARotationLive(t *testing.T) {
	directory := os.Getenv("QCH_TEST_MANAGED_CORE_DIR")
	if directory == "" {
		t.Skip("requires pinned native core fixture")
	}
	_, certPath, keyPath := singBoxTestCertificate(t)
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	// Match ML-DSA target prerequisites without a public dependency: a large
	// certificate chain and hybrid key exchange. Verification stays enabled.
	certificate.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 55555, 1}, Value: make([]byte, 4000)}}
	signer := pair.PrivateKey.(crypto.Signer)
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, signer.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	pair.Certificate = [][]byte{der}
	cover := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("cover")) }))
	cover.EnableHTTP2 = true
	cover.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13, CurvePreferences: []tls.CurveID{tls.X25519MLKEM768}, NextProtos: []string{"h2"}}
	cover.StartTLS()
	t.Cleanup(cover.Close)
	protocol, _ := FindProtocol(core.EngineXray, ProtocolVLESSXHTTP)
	input := newXHTTPTestPlan(t, protocol.Key)
	input.Listen, input.ClientAddress = "127.0.0.1", " [127.0.0.1] "
	input.RealityServerName = "tls.example.test"
	input.BlockMainlandDestination, input.XHTTPMode = false, "stream-up"
	input, err = RegeneratePlan(protocol, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Port = singBoxFreePort(t)
	download := xhttpEndpoint(input, false)
	download["address"], download["port"] = " [127.0.0.1] ", input.Port
	delete(mapValue(download["realitySettings"]), "mldsa65Verify")
	encoded, _ := json.Marshal(map[string]any{"downloadSettings": download})
	input.XHTTPExtra = string(encoded)
	previousVerify := input.RealityMLDSA65Verify
	for _, phase := range []string{"initial", "rotated"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "rotated" {
				input, err = RegeneratePlan(protocol, input)
				if err != nil {
					t.Fatal(err)
				}
			}
			source, err := Generate(core.EngineXray, input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(core.EngineXray, source)
			if !ok {
				t.Fatal("generated server not recognized")
			}
			metadata, err := MarshalClientMetadata(input)
			if err != nil {
				t.Fatal(err)
			}
			if err := ApplyClientMetadata(&parsed, metadata); err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			_ = json.Unmarshal([]byte(source), &root)
			// Only the external cover target is replaced by the isolated fixture.
			mapValue(mapValue(firstMap(root["inbounds"])["streamSettings"])["realitySettings"])["target"] = cover.Listener.Addr().String()
			encoded, _ := json.Marshal(root)
			binary := filepath.Join(directory, "xray")
			serverLog := nativeWSOriginRun(t, core.EngineXray, binary, string(encoded), input.Port, certPath)
			for _, network := range []string{"tcp", "udp"} {
				t.Run(network, func(t *testing.T) {
					profile, err := BuildClientProfile(parsed, "unused.example.test", "")
					if err != nil {
						t.Fatal(err)
					}
					var client map[string]any
					if err := json.Unmarshal([]byte(profile.Xray), &client); err != nil {
						t.Fatal(err)
					}
					stream := mapValue(firstMap(client["outbounds"])["streamSettings"])
					r := mapValue(mapValue(mapValue(mapValue(stream["xhttpSettings"])["extra"])["downloadSettings"])["realitySettings"])
					if r["mldsa65Verify"] != parsed.RealityMLDSA65Verify {
						t.Fatal("export did not preserve the active download verification key")
					}
					port, echo := singBoxFreePort(t), singBoxEcho(t, network)
					client["inbounds"] = []any{map[string]any{"tag": "payload", "protocol": "tunnel", "listen": "127.0.0.1", "port": port, "settings": map[string]any{"allowedNetwork": "tcp,udp", "rewriteAddress": "127.0.0.1", "rewritePort": echo}}}
					encoded, _ := json.Marshal(client)
					clientLog := nativeWSOriginRun(t, core.EngineXray, binary, string(encoded), port, certPath)
					if err := catalogEchoRoundTrip(network, port); err != nil {
						t.Fatalf("%v\nserver: %s\nclient: %s", err, serverLog(), clientLog())
					}
					if phase == "rotated" && network == "tcp" {
						r["mldsa65Verify"] = previousVerify
						port = singBoxFreePort(t)
						firstMap(client["inbounds"])["port"] = port
						encoded, _ := json.Marshal(client)
						nativeWSOriginRun(t, core.EngineXray, binary, string(encoded), port, certPath)
						if err := singBoxEchoRoundTripTimeout(network, port, time.Second); err == nil {
							t.Fatal("download accepted the previous ML-DSA verification key")
						}
					}
				})
			}
		})
	}
}
