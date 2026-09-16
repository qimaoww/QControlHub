package serverconfig

import (
	"strconv"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestWireGuardClientFieldsMatchNativeConfig(t *testing.T) {
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
				profile, err := BuildClientProfile(input, "vpn.example.test", "")
				if err != nil {
					t.Fatal(err)
				}
				native := map[string]string{}
				for _, line := range strings.Split(profile.URI, "\n") {
					if key, value, ok := strings.Cut(line, " = "); ok {
						native[key] = value
					}
				}
				fields := map[string]ClientField{}
				for _, field := range profile.Fields {
					fields[field.Label] = field
					if field.Value == input.WireGuardServerPrivateKey {
						t.Fatal("client parameters leaked the server private key")
					}
				}
				for label, key := range map[string]string{
					"客户端私钥": "PrivateKey", "服务端公钥": "PublicKey",
					"客户端地址": "Address", "AllowedIPs": "AllowedIPs", "MTU": "MTU",
				} {
					if field, ok := fields[label]; !ok || field.Value == "" || field.Value != native[key] {
						t.Errorf("client parameter %s does not match native %s", label, key)
					}
				}
				if native["Address"] != tc.addresses || native["AllowedIPs"] != tc.routes ||
					native["PublicKey"] != wireguardServerPublic(input.WireGuardServerPrivateKey) {
					t.Fatal("native client defaults changed")
				}
				psk, hasPSK := fields["预共享密钥（PSK）"]
				if tc.withoutPSK {
					if hasPSK || native["PresharedKey"] != "" {
						t.Fatal("absent PSK must not be advertised")
					}
				} else if !hasPSK || !psk.Secret || psk.Value != input.WireGuardPresharedKey || psk.Value != native["PresharedKey"] {
					t.Fatal("PSK is missing, unmasked, or inconsistent with the native export")
				}
				if !fields["客户端私钥"].Secret || fields["客户端公钥"].Secret || fields["服务端公钥"].Secret {
					t.Fatal("client key secrecy flags changed")
				}
				if fields["PersistentKeepalive"].Value != strconv.Itoa(tc.keepalive) {
					t.Fatal("client parameters lost the keepalive value")
				}
				if tc.keepalive == 0 && native["PersistentKeepalive"] != "" ||
					tc.keepalive > 0 && native["PersistentKeepalive"] != strconv.Itoa(tc.keepalive) {
					t.Fatal("native keepalive behavior changed")
				}
				if strings.Contains(profile.URI, input.WireGuardServerPrivateKey) || profile.SubscriptionCompatible {
					t.Fatal("WireGuard export leaked server material or became URI-subscription compatible")
				}
			})
		}
	}
}
