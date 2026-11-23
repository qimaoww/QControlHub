package serverconfig

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

func TestWireGuardClientFieldsMatchMihomoExport(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		for _, tc := range []struct {
			name, addresses, routes string
			infer, withoutPSK       bool
			keepalive               int
		}{
			{name: "defaults", addresses: "10.66.66.2/32", routes: "0.0.0.0/0", keepalive: 25},
			{name: "optional-psk-and-keepalive", addresses: "10.66.66.2/32", routes: "192.0.2.0/24", withoutPSK: true},
			{name: "inferred-ipv4", addresses: "10.66.66.2/32", routes: "0.0.0.0/0", infer: true},
			{name: "inferred-ipv6", addresses: "fd00:66::2/128", routes: "::/0", infer: true},
			{name: "inferred-dual-stack", addresses: "10.66.66.2/32, fd00:66::2/128", routes: "0.0.0.0/0, ::/0", infer: true, keepalive: 25},
		} {
			t.Run(string(engine)+"/"+tc.name, func(t *testing.T) {
				protocol, _ := FindProtocol(engine, ProtocolWireGuard)
				input, err := NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.WireGuardClientAddress = " " + tc.addresses + " "
				input.WireGuardAllowedIPs = tc.routes
				input.WireGuardKeepalive = tc.keepalive
				if tc.infer {
					input.WireGuardServerPublicKey, input.WireGuardAllowedIPs = "", ""
				}
				if tc.withoutPSK {
					input.WireGuardPresharedKey = ""
				}
				profile, err := BuildClientProfileNamed(input, "vpn.example.test", "", "WG: [test]")
				if err != nil {
					t.Fatal(err)
				}
				if profile.Format != "Mihomo WireGuard YAML" || !profile.SubscriptionCompatible ||
					profile.URI != profile.Mihomo || profile.MihomoError != "" || strings.ContainsAny(profile.URI, "\r\n") {
					t.Fatal("expected the same single-line Mihomo node for export and subscription")
				}
				serverKey := wireguardServerPublic(input.WireGuardServerPrivateKey)
				peer := map[string]any{"server": "vpn.example.test", "port": input.Port, "public-key": serverKey}
				var routes []any
				for _, route := range strings.Split(tc.routes, ", ") {
					routes = append(routes, route)
				}
				peer["allowed-ips"] = routes
				if !tc.withoutPSK {
					peer["pre-shared-key"] = input.WireGuardPresharedKey
				}
				want := map[string]any{
					"name": "WG: [test]", "type": "wireguard", "server": "vpn.example.test", "port": input.Port,
					"private-key": input.WireGuardClientPrivateKey, "udp": true, "mtu": input.WireGuardMTU, "peers": []any{peer},
				}
				if strings.Contains(tc.addresses, "10.66.66.2/32") {
					want["ip"] = "10.66.66.2"
				}
				if strings.Contains(tc.addresses, "fd00:66::2/128") {
					want["ipv6"] = "fd00:66::2"
				}
				if tc.keepalive > 0 {
					want["persistent-keepalive"] = tc.keepalive
				}
				var proxy map[string]any
				if err := yaml.Unmarshal([]byte(profile.Mihomo), &proxy); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(proxy, want) || strings.Contains(profile.URI, input.WireGuardServerPrivateKey) {
					t.Fatal("incorrect WireGuard proxy fields or credentials")
				}
				fields := map[string]ClientField{}
				for _, field := range profile.Fields {
					fields[field.Label] = field
					if field.Value == input.WireGuardServerPrivateKey {
						t.Fatal("client parameters leaked the server private key")
					}
				}
				for label, value := range map[string]string{
					"客户端私钥": input.WireGuardClientPrivateKey, "服务端公钥": serverKey, "客户端公钥": input.WireGuardClientPublicKey,
					"客户端地址": tc.addresses, "AllowedIPs": tc.routes, "MTU": strconv.Itoa(input.WireGuardMTU),
					"PersistentKeepalive": strconv.Itoa(tc.keepalive),
				} {
					if fields[label].Value != value {
						t.Errorf("incorrect client parameter %s", label)
					}
				}
				psk, hasPSK := fields["预共享密钥（PSK）"]
				if hasPSK == tc.withoutPSK || (hasPSK && (!psk.Secret || psk.Value != input.WireGuardPresharedKey)) {
					t.Fatal("incorrect manual PSK field")
				}
				if !fields["客户端私钥"].Secret || fields["客户端公钥"].Secret || fields["服务端公钥"].Secret {
					t.Fatal("client key secrecy flags changed")
				}
			})
		}
	}
}
