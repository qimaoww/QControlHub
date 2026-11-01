package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestNodeCertificateProtectedFilesAndSharedProvenance(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires protected root-owned fixture directories")
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		for _, shared := range []bool{false, true} {
			name := string(engine)
			if shared {
				name += "/shared"
			}
			t.Run(name, func(t *testing.T) {
				root, err := os.MkdirTemp("/var/lib", "qch-node-tls-test-")
				if err != nil {
					t.Fatal(err)
				}
				baseRoot := root
				t.Cleanup(func() { os.RemoveAll(baseRoot) })
				source := core.ManagedCertificateSource{ID: "mcs_fixture", Source: "node", Engine: engine, Domain: "edge.example.test", ConfigID: "cfg_fixture", TaskID: "tsk_fixture"}
				if shared {
					suffix, err := randomSuffix(8)
					if err != nil {
						t.Fatal(err)
					}
					source.ShareID = "shr_" + suffix
					if !core.ValidAgentShareID(source.ShareID) {
						t.Fatal("invalid generated share identity")
					}
					root = filepath.Join("/etc/qagent", string(engine), "shares", source.ShareID)
					if err = os.MkdirAll(root, 0700); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { os.RemoveAll(root) })
				}
				write := func(path, value string) {
					t.Helper()
					if err := os.WriteFile(path, []byte(value), 0600); err != nil {
						t.Fatal(err)
					}
				}
				certpath, keypath := filepath.Join(root, "cert.pem"), filepath.Join(root, "key.pem")
				input := serverconfig.Input{Protocol: serverconfig.ProtocolTrojan, Tag: "tls-fixture", Username: "fixture", Listen: "0.0.0.0", Port: 24443, Credential: "fixture-node-tls-password", TLSEnabled: true, TLSServerName: source.Domain, CertificatePath: certpath, PrivateKeyPath: keypath}
				content, err := serverconfig.Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				refs := serverconfig.NodeTLSCertificateReferences(engine, content, nil)
				if len(refs) != 1 {
					t.Fatal("missing server reference")
				}
				source.Reference = &refs[0]
				spec := EngineSpec{ConfigPath: filepath.Join(root, "config.json")}
				write(spec.ConfigPath, content)
				pair, err := certificates.SelfSigned([]string{source.Domain}, "ecdsa256", 90)
				if err != nil {
					t.Fatal(err)
				}
				write(certpath, pair.Certificate)
				write(keypath, pair.PrivateKey)
				state := sharedDeploymentState{ConfigID: source.ConfigID, ConfigVersion: 1, AppliedTaskID: source.TaskID, Digest: configurationDigest(content)}
				if shared {
					if err = writeSharedDeploymentState(spec, state); err != nil {
						t.Fatal(err)
					}
				}
				check := func(want string) {
					t.Helper()
					report := readNodeTLSCertificate(source, spec)
					if report.Error != want || report.TaskID != source.TaskID {
						t.Fatalf("unexpected sanitized report code %q, want %q", report.Error, want)
					}
					if want != "" && (report.PrivateKey != "" || report.Certificate != "") {
						t.Fatal("failed read returned partial secrets")
					}
					if want == "" && (report.PrivateKey != pair.PrivateKey || report.Certificate != pair.Certificate) {
						t.Fatal("wrong verified certificate pair")
					}
				}
				check("")
				if shared {
					for _, bad := range []sharedDeploymentState{{ConfigID: "cfg_foreign", ConfigVersion: 1, AppliedTaskID: source.TaskID, Digest: state.Digest}, {ConfigID: source.ConfigID, ConfigVersion: 1, AppliedTaskID: "tsk_old", Digest: state.Digest}, {ConfigID: source.ConfigID, ConfigVersion: 1, AppliedTaskID: source.TaskID, Digest: configurationDigest(content + " ")}} {
						if err = writeSharedDeploymentState(spec, bad); err != nil {
							t.Fatal(err)
						}
						check("node-source-unverified")
					}
					if err = writeSharedDeploymentState(spec, state); err != nil {
						t.Fatal(err)
					}
				}
				oldDomain := source.Domain
				source.Domain = "foreign.example.test"
				check("node-domain-mismatch")
				source.Domain = oldDomain
				other, _ := certificates.SelfSigned([]string{source.Domain}, "ecdsa256", 91)
				write(keypath, other.PrivateKey)
				check("node-pair-invalid")
				write(keypath, pair.PrivateKey)
				if err = os.Chmod(keypath, 0666); err != nil {
					t.Fatal(err)
				}
				check("node-files-unavailable")
				os.Chmod(keypath, 0600)
				if err = os.Rename(keypath, keypath+".original"); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(keypath+".original", keypath); err != nil {
					t.Fatal(err)
				}
				check("node-files-unavailable")
				os.Remove(keypath)
				os.Rename(keypath+".original", keypath)
				if err = os.Link(keypath, keypath+".hardlink"); err != nil {
					t.Fatal(err)
				}
				check("node-files-unavailable")
				os.Remove(keypath + ".hardlink")
				original := source.Reference
				bad := *original
				bad.PrivateKeyPath = "/etc/private/foreign.key"
				source.Reference = &bad
				check("node-source-unverified")
				source.Reference = original
				os.Remove(certpath)
				check("node-files-unavailable")
			})
		}
	}
}
