package serverconfig

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Xray accepts unknown settings.users without rejecting its configuration,
// leaving VMess/Trojan listeners without working credentials. Verify actual
// traffic, because a successful `xray run -test` does not detect that mistake.
func TestXrayGeneratedAuthenticationRoundTrip(t *testing.T) {
	binary := os.Getenv("QCH_TEST_XRAY_BIN")
	if binary == "" {
		t.Skip("requires QCH_TEST_XRAY_BIN")
	}
	_, certificate, key := singBoxTestCertificate(t)
	t.Setenv("SSL_CERT_FILE", certificate)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "authenticated-through-generated-xray-inbound")
	}))
	defer target.Close()
	for _, protocolKey := range []string{ProtocolVMess, ProtocolTrojan} {
		t.Run(protocolKey, func(t *testing.T) {
			protocol, _ := FindProtocol(core.EngineXray, protocolKey)
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Port, input.Listen, input.Transport = xrayAuthenticationTestPort(t), "127.0.0.1", "raw"
			input.TLSEnabled, input.RealityEnabled = protocol.RequiresTLS, false
			input.CertificatePath, input.PrivateKeyPath, input.TLSServerName = certificate, key, "tls.example.test"
			content, err := Generate(core.EngineXray, input)
			if err != nil {
				t.Fatal(err)
			}
			startWireGuardTestCore(t, core.EngineXray, binary, content)
			outbound, err := BuildClientOutbound(core.EngineXray, input, "127.0.0.1", input.TLSServerName)
			if err != nil {
				t.Fatal(err)
			}
			port := xrayAuthenticationTestPort(t)
			client, _ := json.Marshal(map[string]any{
				"log":       map[string]any{"loglevel": "warning"},
				"inbounds":  []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks"}},
				"outbounds": []json.RawMessage{outbound},
			})
			startWireGuardTestCore(t, core.EngineXray, binary, string(client))
			proxy, _ := url.Parse("socks5://127.0.0.1:" + strconv.Itoa(port))
			transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
			defer transport.CloseIdleConnections()
			httpClient := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			deadline := time.Now().Add(6 * time.Second)
			for {
				response, err := httpClient.Get(target.URL)
				if err == nil {
					body, readErr := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if readErr == nil && response.StatusCode == http.StatusOK && string(body) == "authenticated-through-generated-xray-inbound" {
						return
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("generated %s credentials did not authenticate real traffic: %v", protocolKey, err)
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}

func xrayAuthenticationTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestLegacyXrayUsersPresetCanBeRepairedWithoutLosingOtherInbounds(t *testing.T) {
	for _, protocolKey := range []string{ProtocolVMess, ProtocolTrojan} {
		protocol, _ := FindProtocol(core.EngineXray, protocolKey)
		input, err := nativeTLSFixturePlan(protocol)
		if err != nil {
			t.Fatal(err)
		}
		generated, err := Generate(core.EngineXray, input)
		if err != nil {
			t.Fatal(err)
		}
		legacy := strings.Replace(generated, `"clients"`, `"users"`, 1)
		var legacyRoot map[string]any
		if err := json.Unmarshal([]byte(legacy), &legacyRoot); err != nil {
			t.Fatal(err)
		}
		legacyRoot["futureRoot"] = "preserved"
		legacyRoot["inbounds"] = append(legacyRoot["inbounds"].([]any), map[string]any{"tag": "keep-other", "protocol": "socks", "port": 10807, "settings": map[string]any{"auth": "noauth"}})
		raw, _ := json.Marshal(legacyRoot)
		legacy = string(raw)
		parsed, ok := Parse(core.EngineXray, legacy)
		if !ok || parsed.Credential != input.Credential {
			t.Fatal("legacy credentials could not be edited")
		}
		fixed, err := Generate(core.EngineXray, parsed)
		if err == nil {
			fixed, err = MutateGenerated(core.EngineXray, legacy, fixed, input.Tag, "modify")
		}
		var root map[string]any
		if err != nil || json.Unmarshal([]byte(fixed), &root) != nil {
			t.Fatalf("legacy preset repair: %v", err)
		}
		settings := mapValue(firstMap(root["inbounds"])["settings"])
		if firstMap(settings["clients"]) == nil || settings["users"] != nil {
			t.Fatal("legacy repair left inactive credentials")
		}
		inbounds := root["inbounds"].([]any)
		if len(inbounds) != 2 || mapValue(inbounds[1])["tag"] != "keep-other" || root["futureRoot"] != "preserved" {
			t.Fatal("repair lost another inbound or custom root option")
		}
	}
}
