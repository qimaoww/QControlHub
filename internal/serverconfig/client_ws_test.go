package serverconfig

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strconv"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

func TestWebSocketClientHostDefaultsAndLifecycle(t *testing.T) {
	for _, key := range []string{"vless-ws-tls", "vmess-ws-tls", "trojan-ws-tls"} {
		for _, host := range []string{"", "custom.example.test"} {
			t.Run(key+"/"+host, func(t *testing.T) {
				protocol, _ := FindProtocol(core.EngineSingBox, key)
				input, err := NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.TLSServerName, input.TransportHost = "sni.example.test", host
				input.TransportPath = "/custom/path"
				// Start managed, then switch to native TLS using the same routing values.
				for _, mode := range []string{"caddy", "native"} {
					input.TLSMode = mode
					input.Listen, input.Port, input.TLSPublicPort = "127.0.0.1", 21001, 443
					if mode == "native" {
						input.Listen, input.Port, input.TLSPublicPort = "0.0.0.0", 443, 0
						input.CertificatePath, input.PrivateKeyPath = "/etc/certs/cert.pem", "/etc/certs/key.pem"
					}
					source, err := Generate(core.EngineSingBox, input)
					if err != nil {
						t.Fatal(err)
					}
					metadata, err := MarshalClientMetadataWithPrevious(input, "")
					if err != nil {
						t.Fatal(err)
					}
					saved, ok := Parse(core.EngineSingBox, source)
					if !ok {
						t.Fatal("parse source")
					}
					if err := ApplyClientMetadata(&saved, metadata); err != nil {
						t.Fatal(err)
					}
					if saved.TransportHost != host || saved.TLSServerName != input.TLSServerName || saved.TransportPath != input.TransportPath || saved.Port != input.Port || saved.Listen != input.Listen || saved.CertificatePath != input.CertificatePath {
						t.Fatalf("saved routing differs in %s: %+v", mode, saved)
					}
					wantHost := host
					if wantHost == "" {
						wantHost = input.TLSServerName
					}
					for _, address := range []string{"203.0.113.10", "2001:db8::10", "preferred.example.test", input.TLSServerName} {
						profile, err := BuildClientProfile(saved, address, "unrelated.example.test")
						if err != nil {
							t.Fatal(err)
						}
						uri, err := url.Parse(profile.URI)
						if err != nil {
							t.Fatal(err)
						}
						q := uri.Query()
						gotAddress, gotPort := uri.Hostname(), uri.Port()
						if key == "vmess-ws-tls" {
							payload, err := vmessURLPayload(profile.URI)
							if err != nil {
								t.Fatal(err)
							}
							field := func(key string) string { var value string; _ = json.Unmarshal(payload[key], &value); return value }
							gotAddress, gotPort = field("add"), field("port")
							q = url.Values{"host": {field("host")}, "sni": {field("sni")}, "path": {field("path")}, "type": {field("net")}}
						}
						if gotAddress != address || gotPort != "443" || q.Get("host") != wantHost || q.Get("sni") != input.TLSServerName || q.Get("path") != input.TransportPath || q.Get("type") != "ws" {
							t.Fatalf("wrong %s client URL: %s", mode, profile.URI)
						}
						var mihomo map[string]any
						if err := yaml.Unmarshal([]byte(profile.Mihomo), &mihomo); err != nil {
							t.Fatal(err)
						}
						opts := mapValue(mihomo["ws-opts"])
						if mihomo["server"] != address || mapValue(opts["headers"])["Host"] != wantHost || opts["path"] != input.TransportPath {
							t.Fatalf("wrong Mihomo profile: %s", profile.Mihomo)
						}
						raw, err := BuildClientOutbound(core.EngineSingBox, saved, address, "")
						if err != nil {
							t.Fatal(err)
						}
						var out map[string]any
						if err := json.Unmarshal(raw, &out); err != nil {
							t.Fatal(err)
						}
						ws, tls := mapValue(out["transport"]), mapValue(out["tls"])
						if out["server"] != address || out["server_port"] != float64(443) || mapValue(ws["headers"])["Host"] != wantHost || tls["server_name"] != input.TLSServerName || ws["path"] != input.TransportPath {
							t.Fatalf("wrong outbound: %s", raw)
						}
						// Custom trust forces full JSON; it must carry the same host routing.
						trusted := saved
						trusted.TLSClientCertificate = "fixture public certificate"
						native, err := BuildClientProfile(trusted, address, "")
						if err != nil {
							t.Fatal(err)
						}
						var root map[string]any
						if err := json.Unmarshal([]byte(native.URI), &root); err != nil {
							t.Fatal(err)
						}
						exported := firstMap(root["outbounds"])
						if mapValue(mapValue(exported["transport"])["headers"])["Host"] != wantHost || exported["server"] != address {
							t.Fatal("native JSON lost routing")
						}
					}
					// Client defaulting must not change the source, metadata, or caller input.
					again, err := Generate(core.EngineSingBox, saved)
					if err != nil {
						t.Fatal(err)
					}
					if again != source {
						t.Fatal("client export changed server-side headers")
					}
				}
			})
		}
	}
}

func TestWebSocketClientHostScope(t *testing.T) {
	for _, tc := range []struct {
		transport, host, sni, want string
		tls                        bool
	}{
		{"websocket", "", "", "", false},
		{"websocket", "explicit.example.test", "", "explicit.example.test", false},
		{"websocket", "", "sni.example.test", "sni.example.test", true},
		{"grpc", "", "sni.example.test", "", true},
		{"httpupgrade", "", "sni.example.test", "", true},
	} {
		input := Input{Protocol: ProtocolVLESS, Port: 443, Credential: "dd912e84-0755-4e53-b345-3bf8423af6cb", Transport: tc.transport, TransportPath: "/ws", TransportHost: tc.host, TLSEnabled: tc.tls, TLSServerName: tc.sni}
		original := input
		profile, err := BuildClientProfile(input, "203.0.113.10", "")
		if err != nil {
			t.Fatal(err)
		}
		fields := map[string]string{}
		for _, f := range profile.Fields {
			fields[f.Label] = f.Value
		}
		if fields["HTTP Host"] != tc.want || !reflect.DeepEqual(input, original) {
			t.Fatalf("unexpected defaults: %+v", fields)
		}
		if tc.transport == "websocket" && tc.host == "" {
			raw, err := BuildClientOutbound(core.EngineXray, input, "203.0.113.10", "")
			if err != nil {
				t.Fatal(err)
			}
			var out map[string]any
			_ = json.Unmarshal(raw, &out)
			ws := mapValue(mapValue(out["streamSettings"])["wsSettings"])
			got := stringValue(ws["host"])
			if got != tc.want {
				t.Fatalf("Xray Host=%s want=%s at port %s", got, tc.want, strconv.Itoa(input.Port))
			}
		}
	}
}

func TestNativeWebSocketTLSCatalogAndDetachedMetadata(t *testing.T) {
	want := map[core.Engine]int{core.EngineSingBox: 6, core.EngineXray: 2, core.EngineMihomo: 2, core.EngineShadowsocksRust: 0}
	for engine, count := range want {
		protocols := nativeWebSocketTLSProtocols(engine)
		if len(protocols) != count {
			t.Fatalf("%s native TLS WS catalog changed: %d want %d", engine, len(protocols), count)
		}
		for _, p := range protocols {
			t.Run(string(engine)+"/"+p.Key, func(t *testing.T) {
				input, err := nativeTLSFixturePlan(p)
				if err != nil {
					t.Fatal(err)
				}
				input.Transport, input.TransportPath = "websocket", "/independent"
				input.TLSServerName, input.TransportHost = "sni.example.test", "host.example.test"
				input.CertificatePath, input.PrivateKeyPath = "/etc/certs/site.pem", "/etc/certs/site.key"
				source, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := MarshalClientMetadata(input)
				if err != nil {
					t.Fatal(err)
				}
				parsed, ok := Parse(engine, source)
				if !ok {
					t.Fatal("source parse failed")
				}
				if err := ApplyClientMetadata(&parsed, metadata); err != nil {
					t.Fatal(err)
				}
				if parsed.TransportHost != input.TransportHost || parsed.TLSServerName != input.TLSServerName {
					t.Fatal("detached client routing was lost")
				}
				regenerated, err := RegeneratePlan(p, parsed)
				if err != nil {
					t.Fatal(err)
				}
				if regenerated.TransportHost != input.TransportHost || regenerated.TLSServerName != input.TLSServerName || regenerated.CertificatePath != input.CertificatePath {
					t.Fatal("regeneration overwrote custom routing")
				}
				// Path/credentials are intentionally regenerated; bind a fresh snapshot.
				next, err := Generate(engine, regenerated)
				if err != nil {
					t.Fatal(err)
				}
				fresh, err := MarshalClientMetadata(regenerated)
				if err != nil {
					t.Fatal(err)
				}
				reread, ok := Parse(engine, next)
				if !ok {
					t.Fatal("regenerated parse failed")
				}
				if err := ApplyClientMetadata(&reread, fresh); err != nil {
					t.Fatal(err)
				}
				if reread.TransportHost != input.TransportHost || reread.TransportPath != regenerated.TransportPath {
					t.Fatal("regenerated save lost WS client values")
				}
				if engine == core.EngineSingBox {
					return
				} // Its explicit Host is already source data.
				for name, change := range map[string]func(*Input){
					"peer":        func(in *Input) { in.Credential = "changed-peer" },
					"tag":         func(in *Input) { in.Tag = "other" },
					"path":        func(in *Input) { in.TransportPath = "/other" },
					"port":        func(in *Input) { in.Port++ },
					"listen":      func(in *Input) { in.Listen = "127.0.0.2" },
					"certificate": func(in *Input) { in.CertificatePath = "/etc/certs/other.pem" },
					"key":         func(in *Input) { in.PrivateKeyPath = "/etc/certs/other.key" },
					"tls":         func(in *Input) { in.TLSEnabled = false },
				} {
					t.Run("stale-"+name, func(t *testing.T) {
						stale, _ := Parse(engine, source)
						change(&stale)
						if err := ApplyClientMetadata(&stale, metadata); err != nil {
							t.Fatal(err)
						}
						if stale.TransportHost != "" {
							t.Fatal("stale WS client metadata applied to another endpoint")
						}
					})
				}
			})
		}
	}
}
