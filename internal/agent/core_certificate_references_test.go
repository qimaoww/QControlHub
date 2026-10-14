package agent

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCertificateReferencesFollowEngineFieldSemantics(t *testing.T) {
	for _, test := range []struct {
		name    string
		engine  core.Engine
		content string
		want    []string
	}{
		{"reality keys can look like paths", core.EngineMihomo, `listeners:
- type: vless
  reality-config:
    dest: www.getty.edu:443
    private-key: certs/looks-like-a-file.pem
    short-id: [0123456789abcdef]
- type: vless
  reality-config:
    private-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
proxies:
- type: wireguard
  private-key: wireguard-inline-secret
  peers:
  - pre-shared-key: peer-inline-secret
- type: masque
  private-key: masque-inline-secret
- type: ssh
  private-key: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    ssh-inline-secret
    -----END OPENSSH PRIVATE KEY-----
`, nil},
		{"TLS and Reality coexist", core.EngineMihomo, `listeners:
- type: vless
  reality-config: {private-key: inline-reality-secret}
- type: trojan
  certificate: /etc/qagent/mihomo/fullchain.pem
  private-key: certs/key.pem
- type: anytls
  certificate: |
    -----BEGIN CERTIFICATE-----
    inline-certificate
    -----END CERTIFICATE-----
  private-key: |
    -----BEGIN PRIVATE KEY-----
    inline-secret
    -----END PRIVATE KEY-----
`, []string{"/etc/qagent/mihomo/fullchain.pem", "certs/key.pem"}},
		{"TLS filename may look like a Reality key", core.EngineMihomo, "listeners:\n- type: trojan\n  private-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n", []string{"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}},
		{"TLS controller outbound and nested realm", core.EngineMihomo, `tls: {certificate: controller.pem, private-key: controller.key}
tuic-server: {certificate: tuic.pem, private-key: tuic.key}
listeners:
- type: hysteria2
  certificate: hy.pem
  private-key: hy.key
  realm-opts: {certificate: realm.pem, private-key: realm.key}
proxies:
- type: vless
  certificate: client.pem
  private-key: client.key
  xhttp-opts:
    download-settings: {certificate: download.pem, private-key: download.key}
- type: ssh
  private-key: ssh-key.pem
proxy-providers:
  inline:
    type: inline
    payload:
    - type: wireguard
      private-key: inline-wireguard
    - type: trojan
      certificate: provider.pem
      private-key: provider.key
`, []string{"controller.pem", "controller.key", "tuic.pem", "tuic.key", "hy.pem", "hy.key", "realm.pem", "realm.key", "client.pem", "client.key", "download.pem", "download.key", "ssh-key.pem", "provider.pem", "provider.key"}},
		{"Xray file and inline certificate arrays", core.EngineXray, `{"inbounds":[{"streamSettings":{"security":"reality","realitySettings":{"privateKey":"inline-reality"}}},{"streamSettings":{"tlsSettings":{"certificates":[{"certificateFile":"cert.pem","keyFile":"key.pem"},{"certificate":["inline-cert"],"key":["inline-key"]}]}}}],"outbounds":[{"streamSettings":{"tlsSettings":{"certificates":[{"certificateFile":"client.pem"}]}}}]}`, []string{"cert.pem", "key.pem", "client.pem"}},
		{"sing-box TLS arrays and protocol keys", core.EngineSingBox, `{"inbounds":[{"tls":{"reality":{"private_key":"inline-reality"}}},{"tls":{"certificate_path":"cert.pem","key_path":"key.pem","client_certificate_path":["ca1.pem","ca2.pem"]}},{"tls":{"certificate":["inline-cert"],"key":["inline-key"]}}],"endpoints":[{"type":"wireguard","private_key":"inline-wireguard"}],"outbounds":[{"tls":{"client_certificate_path":"client.pem","client_key_path":"client.key"}}],"certificate":{"certificate_path":["global.pem"]}}`, []string{"cert.pem", "key.pem", "ca1.pem", "ca2.pem", "client.pem", "client.key", "global.pem"}},
		{"file-only fields do not accept PEM", core.EngineSingBox, `{"inbounds":[{"tls":{"key_path":"-----BEGIN PRIVATE KEY-----"}}]}`, []string{"-----BEGIN PRIVATE KEY-----"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, refs, err := coreCertificateReferences(test.engine, test.content)
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, ref := range refs {
				paths = append(paths, ref.path)
				if ref.field == "" {
					t.Fatal("missing configuration field location")
				}
			}
			sort.Strings(paths)
			sort.Strings(test.want)
			if !reflect.DeepEqual(paths, test.want) {
				t.Fatalf("paths = %v, want %v", paths, test.want)
			}
		})
	}
}

func TestRealityDoesNotProbeOrRepairInlineKeys(t *testing.T) {
	content := "listeners:\n- type: vless\n  reality-config:\n    private-key: ../../../inline-secret\n"
	executor := &Executor{}
	spec := DefaultSpecs()[core.EngineMihomo]
	if err := executor.verifyCoreCertificateAccess(context.Background(), core.EngineMihomo, spec, content, commandIdentity{uid: 65534, gid: 65534}); err != nil {
		t.Fatalf("Reality invoked file access or repair: %v", err)
	}
}

func TestMihomoValidationResolvesFilesAgainstOrdinaryAndSharedState(t *testing.T) {
	base := DefaultSpecs()[core.EngineMihomo]
	shared, err := sharedInstanceSpec(core.EngineMihomo, base, "shr_0000000000000001", ServiceManagerSystemd)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []EngineSpec{base, shared} {
		state := coreCertificateWorkingDirectory(core.EngineMihomo, spec, ServiceManagerSystemd)
		content := "listeners:\n- type: vless\n  reality-config: {private-key: inline-reality}\n- type: trojan\n  certificate: certs/cert.pem\n  private-key: /etc/qcontrolhub/tls/key.pem\n"
		got, err := mihomoValidationCertificatePaths(content, state)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, filepath.Join(state, "certs/cert.pem")) || !strings.Contains(got, "private-key: inline-reality") || !strings.Contains(got, "/etc/qcontrolhub/tls/key.pem") {
			t.Fatalf("validation differs from deployed service: %s", got)
		}
	}
}
