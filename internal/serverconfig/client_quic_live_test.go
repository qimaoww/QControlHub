package serverconfig

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMihomoQUICCustomALPNLive(t *testing.T) {
	serverBinary := nativeWSTestBinary(t, core.EngineMihomo)
	clientBinary := nativeWSTestBinary(t, core.EngineSingBox)
	certificate, certPath, keyPath := singBoxTestCertificate(t)
	for _, key := range []string{ProtocolTUIC, ProtocolHy2} {
		t.Run(key, func(t *testing.T) {
			protocol, _ := FindProtocol(core.EngineMihomo, key)
			input, _ := nativeTLSFixturePlan(protocol)
			input.Listen, input.Port = "127.0.0.1", singBoxFreePort(t)
			input.TLSServerName, input.CertificatePath, input.PrivateKeyPath = "tls.example.test", certPath, keyPath
			input.TLSALPN, input.TLSClientALPN, input.TLSClientCertificate = "qch-tuic", "qch-tuic", certificate
			if key == ProtocolHy2 {
				input.TLSALPN, input.TLSClientALPN = "h2,h3", "h3"
			}
			input.ClientAddress, input.ClientPort = "127.0.0.1", input.Port
			source, err := Generate(core.EngineMihomo, input)
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := MarshalClientMetadata(input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(core.EngineMihomo, source)
			if !ok {
				t.Fatal("cannot read saved QUIC listener")
			}
			if err := ApplyClientMetadata(&parsed, metadata); err != nil {
				t.Fatal(err)
			}
			if parsed.TLSALPN != input.TLSALPN || parsed.TLSClientALPN != input.TLSClientALPN {
				t.Fatal("ALPN lost during readback")
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "server.yaml")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			log, err := os.Create(filepath.Join(dir, "server.log"))
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(serverBinary, "-d", dir, "-f", path)
			command.Env = append(os.Environ(), "SAFE_PATHS="+filepath.Dir(certPath))
			command.Stdout, command.Stderr = log, log
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait(); _ = log.Close() })
			deadline := time.Now().Add(5 * time.Second)
			for {
				socket, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(input.Port)))
				if err != nil {
					break
				}
				_ = socket.Close()
				if time.Now().After(deadline) {
					output, _ := os.ReadFile(log.Name())
					t.Fatalf("QUIC listener failed: %s", output)
				}
				time.Sleep(20 * time.Millisecond)
			}
			for _, network := range []string{"tcp", "udp"} {
				t.Run(network, func(t *testing.T) {
					outbound, err := BuildClientOutbound(core.EngineSingBox, parsed, "unused.example.test", "")
					if err != nil {
						t.Fatal(err)
					}
					var out map[string]any
					_ = json.Unmarshal(outbound, &out)
					echo, port := singBoxEcho(t, network), singBoxFreePort(t)
					client, _ := json.Marshal(map[string]any{"inbounds": []any{map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": port, "network": network, "override_address": "127.0.0.1", "override_port": echo}}, "outbounds": []any{out}})
					clientLog := singBoxRun(t, clientBinary, "quic-custom-client", string(client))
					if err := singBoxEchoRoundTrip(network, port); err != nil {
						output, _ := os.ReadFile(log.Name())
						t.Fatalf("%v\nserver: %s\nclient: %s", err, output, clientLog())
					}
				})
			}
		})
	}
}
