package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Exercise real generated configurations rather than duplicating the redactor's
// list of field names. Protocol additions must keep diagnostic reads safe.
func TestDiagnosticRedactionGeneratedProtocols(t *testing.T) {
	for _, engine := range core.AllEngines() {
		for _, protocol := range Protocols(engine) {
			if protocol.UsesEndpoint || protocol.PortForward {
				continue
			}
			t.Run(string(engine)+"/"+protocol.Key, func(t *testing.T) {
				input, err := nativeTLSFixturePlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				content, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				redacted, err := json.Marshal(core.RedactDiagnosticValue(map[string]any{"content": content}))
				if err != nil {
					t.Fatal(err)
				}
				for name, secret := range map[string]string{
					"credential": input.Credential, "secondary credential": input.SecondaryCredential,
					"Reality private key": input.RealityPrivateKey, "VLESS decryption": input.VLESSDecryption,
					"WireGuard private key": input.WireGuardServerPrivateKey, "WireGuard preshared key": input.WireGuardPresharedKey,
					"ShadowTLS password": input.SnellShadowTLSPassword,
				} {
					if secret != "" && secret != "none" && strings.Contains(string(redacted), secret) {
						t.Errorf("%s remains in diagnostic response", name)
					}
				}
			})
		}
	}
}
