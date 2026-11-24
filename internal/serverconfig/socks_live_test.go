package serverconfig

import (
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"golang.org/x/net/proxy"
)

func TestSocksNativeTraffic(t *testing.T) {
	clientBinary := nativeWSTestBinary(t, core.EngineSingBox)
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
		t.Run(string(engine), func(t *testing.T) {
			binary := nativeWSTestBinary(t, engine)
			protocol, _ := FindProtocol(engine, ProtocolSocks)
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Port = singBoxFreePort(t)
			source, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			if engine == core.EngineSingBox {
				singBoxRun(t, binary, "SOCKS5 server", source)
			} else {
				nativeWSOriginRun(t, engine, binary, source, input.Port, "")
			}
			parsed, ok := Parse(engine, source)
			if !ok {
				t.Fatal("native configuration not recognized")
			}
			for _, network := range []string{"tcp", "udp"} {
				t.Run(network, func(t *testing.T) {
					outbound, err := BuildClientOutbound(core.EngineSingBox, parsed, "127.0.0.1", "")
					if err != nil {
						t.Fatal(err)
					}
					var out map[string]any
					if err := json.Unmarshal(outbound, &out); err != nil {
						t.Fatal(err)
					}
					echo, port := singBoxEcho(t, network), singBoxFreePort(t)
					config, _ := json.Marshal(map[string]any{
						"inbounds":  []any{map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": port, "network": network, "override_address": "127.0.0.1", "override_port": echo}},
						"outbounds": []any{out},
					})
					log := singBoxRun(t, clientBinary, "SOCKS5 client", string(config))
					if err := singBoxEchoRoundTrip(network, port); err != nil {
						t.Fatalf("%v\n%s", err, log())
					}
				})
			}
			t.Run("xray-export", func(t *testing.T) {
				outbound, err := BuildClientOutbound(core.EngineXray, parsed, "127.0.0.1", "")
				if err != nil {
					t.Fatal(err)
				}
				echo, port := singBoxEcho(t, "tcp"), singBoxFreePort(t)
				config, _ := json.Marshal(map[string]any{
					"inbounds":  []any{map[string]any{"protocol": "dokodemo-door", "listen": "127.0.0.1", "port": port, "settings": map[string]any{"address": "127.0.0.1", "port": echo, "network": "tcp"}}},
					"outbounds": []json.RawMessage{outbound},
				})
				log := nativeWSOriginRun(t, core.EngineXray, nativeWSTestBinary(t, core.EngineXray), string(config), port, "")
				if err := singBoxEchoRoundTrip("tcp", port); err != nil {
					t.Fatalf("%v\n%s", err, log())
				}
			})
			address := net.JoinHostPort("127.0.0.1", strconv.Itoa(input.Port))
			for _, auth := range []*proxy.Auth{nil, {User: input.Username, Password: "incorrect-password"}} {
				dialer, err := proxy.SOCKS5("tcp", address, auth, &net.Dialer{Timeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				conn, err := dialer.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(singBoxEcho(t, "tcp"))))
				if err == nil {
					conn.Close()
					t.Fatal("SOCKS5 accepted missing or incorrect authentication")
				}
			}
		})
	}
}
