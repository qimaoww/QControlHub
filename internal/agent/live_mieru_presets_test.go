package agent

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"gopkg.in/yaml.v3"
)

func TestMieruPresetsTransferRealTraffic(t *testing.T) {
	if os.Getenv("QCH_LIVE_CORE_VALIDATION_TEST") != "1" {
		t.Skip("QCH_LIVE_CORE_VALIDATION_TEST is not enabled")
	}
	binary := filepath.Join(os.Getenv("QCH_LIVE_CORE_ROOT"), "bin", "mihomo")
	const response = "qcontrolhub-live-mieru-ok"
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, response)
	}))
	defer backend.Close()
	for _, transport := range []string{"TCP", "UDP"} {
		t.Run(transport, func(t *testing.T) {
			protocol, ok := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolMieru)
			if !ok {
				t.Fatal("Mieru preset is unavailable")
			}
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Listen, input.Port = "127.0.0.1", availableTCPPort(t)
			input.MieruTransport = transport
			if transport == "UDP" {
				listener, err := net.ListenPacket("udp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				input.Port = listener.LocalAddr().(*net.UDPAddr).Port
				_ = listener.Close()
			}
			content, err := serverconfig.Generate(core.EngineMihomo, input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := serverconfig.Parse(core.EngineMihomo, content)
			if !ok {
				t.Fatal("generated Mieru listener did not parse")
			}
			profile, err := serverconfig.BuildClientProfileNamed(parsed, "127.0.0.1", "", "LIVE")
			if err != nil || profile.MihomoError != "" {
				t.Fatalf("client export: %v %s", err, profile.MihomoError)
			}
			var proxy map[string]any
			if err := yaml.Unmarshal([]byte(profile.Mihomo), &proxy); err != nil {
				t.Fatal(err)
			}
			clientPort := availableTCPPort(t)
			clientYAML, err := yaml.Marshal(map[string]any{
				"mixed-port": clientPort, "mode": "rule", "log-level": "info",
				"proxies": []any{proxy}, "rules": []string{"MATCH,LIVE"},
			})
			if err != nil {
				t.Fatal(err)
			}
			// The loopback controller supplies a readiness probe for UDP-only
			// servers without adding another proxy listener to the preset.
			readyPort := availableTCPPort(t)
			content += fmt.Sprintf("external-controller: 127.0.0.1:%d\n", readyPort)
			directory := t.TempDir()
			server := startMihomoForTrafficTest(t, binary, filepath.Join(directory, "server"), "config.yaml", content, readyPort)
			defer server.stop(t)
			client := startMihomoForTrafficTest(t, binary, filepath.Join(directory, "client"), "config.yaml", string(clientYAML), clientPort)
			defer client.stop(t)
			assertProxyTraffic(t, clientPort, backend.URL, response, func() string { return server.log(t) }, func() string { return client.log(t) })
		})
	}
}
