//go:build linux

package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"golang.org/x/net/proxy"
)

// Uses the verified binaries from the existing permission fixture. No public
// listener, production service, host configuration or Internet is involved.
func TestImportedDependenciesRunWithRealCores(t *testing.T) {
	bin := os.Getenv("QCH_PERMISSION_BIN")
	if bin == "" {
		t.Skip("requires verified official core binaries")
	}
	requireAgentRoot(t)
	identity, err := lookupCommandIdentity("nobody", "nogroup")
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		t.Run(string(engine), func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{filepath.Dir(root), root} {
				if err := os.Chmod(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			source := filepath.Join(root, "原服务 证书")
			target := filepath.Join(root, "managed")
			state := filepath.Join(root, "state")
			for _, dir := range []string{source, target, state} {
				if err := os.Mkdir(dir, 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(dir, 0, int(identity.gid)); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Chown(state, int(identity.uid), int(identity.gid)); err != nil {
				t.Fatal(err)
			}
			pair, err := certificates.SelfSigned([]string{"import.example.test"}, "ecdsa256", 7)
			if err != nil {
				t.Fatal(err)
			}
			for name, data := range map[string]string{"chain.pem": pair.Certificate, "secret.key": pair.PrivateKey, "policy.json": `{"version":3,"rules":[{"ip_cidr":["192.0.2.0/24"]}]}`, "policy.yaml": "payload: ['IP-CIDR,192.0.2.0/24']\n", "custom.acl": "[reject_list]\n192.0.2.0/24\n"} {
				if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			port := importLivePort(t)
			binaryName := string(engine)
			if engine == core.EngineShadowsocksRust {
				binaryName = "ssserver"
			}
			binaryPath := filepath.Join(bin, binaryName)
			for _, name := range []string{binaryName, "sslocal"} {
				data, err := os.ReadFile(filepath.Join(bin, name))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, name), data, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			binaryPath = filepath.Join(root, binaryName)
			existing := EngineSpec{Binary: binaryPath, ConfigPath: filepath.Join(source, "source.json"), WorkingDirectory: source}
			managed := EngineSpec{Binary: binaryPath, ConfigPath: filepath.Join(target, "config.json"), WorkingDirectory: state}
			cert, key := filepath.Join(source, "chain.pem"), filepath.Join(source, "secret.key")
			var content string
			switch engine {
			case core.EngineMihomo:
				content = fmt.Sprintf("log-level: silent\nlisteners:\n  - name: tls\n    type: trojan\n    port: %d\n    listen: 127.0.0.1\n    users: [{username: fixture, password: fixture-password}]\n    certificate: %q\n    private-key: %q\nrule-providers:\n  local:\n    type: file\n    behavior: classical\n    path: %q\nrules: ['RULE-SET,local,REJECT', 'MATCH,DIRECT']\n", port, cert, key, filepath.Join(source, "policy.yaml"))
			case core.EngineXray:
				content = fmt.Sprintf(`{"log":{"loglevel":"debug"},"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"trojan","settings":{"clients":[{"password":"fixture-password"}]},"streamSettings":{"security":"tls","tlsSettings":{"certificates":[{"certificateFile":%q,"keyFile":%q}]}}}],"outbounds":[{"protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.1/32"]}]}}]}`, port, cert, key)
			case core.EngineSingBox:
				content = fmt.Sprintf(`{"log":{"disabled":true},"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":%d,"users":[{"password":"fixture-password"}],"tls":{"enabled":true,"certificate_path":%q,"key_path":%q}}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"rule_set":[{"type":"local","tag":"fixture","format":"source","path":%q}],"rules":[{"rule_set":"fixture","action":"reject"}]}}`, port, cert, key, filepath.Join(source, "policy.json"))
			case core.EngineShadowsocksRust:
				content = fmt.Sprintf(`{"server":"127.0.0.1","server_port":%d,"method":"aes-256-gcm","password":"fixture-password","acl":%q}`, port, filepath.Join(source, "custom.acl"))
			}
			var plan coreImportPlan
			switch engine {
			case core.EngineMihomo:
				plan, err = prepareMihomoDependencyImport(existing, managed, content)
			case core.EngineShadowsocksRust:
				plan, err = prepareSSRustImport(existing, content, managed)
			default:
				plan, err = prepareJSONDependencyImport(engine, existing, managed, content)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.resources) == 0 || len(plan.dependencies) == 0 {
				t.Fatal("native fixture has no dependencies")
			}
			if err := (&Executor{}).validateImportPlanSnapshot(context.Background(), engine, managed, plan); err != nil {
				t.Fatal(err)
			}
			if err := plan.stage(identity); err != nil {
				t.Fatal(err)
			}
			defer plan.cleanup()
			if err := os.WriteFile(managed.ConfigPath, []byte(plan.managedContent), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(managed.ConfigPath, 0, int(identity.gid)); err != nil {
				t.Fatal(err)
			}
			args := []string{"run", "-c", managed.ConfigPath}
			if engine == core.EngineMihomo {
				args = []string{"-d", state, "-f", managed.ConfigPath}
			}
			if engine == core.EngineShadowsocksRust {
				args = []string{"-c", managed.ConfigPath}
			}
			for restart := 0; restart < 2; restart++ {
				ctx, cancel := context.WithCancel(context.Background())
				command := exec.CommandContext(ctx, binaryPath, args...)
				command.Dir = state
				command.Env = append(os.Environ(), "SAFE_PATHS="+target)
				configureCommand(command)
				configureCommandIdentity(command, &identity)
				var output bytes.Buffer
				command.Stdout, command.Stderr = &output, &output
				if err := command.Start(); err != nil {
					cancel()
					t.Fatal(err)
				}
				func() {
					defer func() { cancel(); _ = command.Wait() }()
					if !importLiveWait(port, 5*time.Second) {
						t.Fatalf("native start: %s", output.String())
					}
					backend, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					defer backend.Close()
					go func() {
						c, err := backend.Accept()
						if err == nil {
							defer c.Close()
							_ = c.SetDeadline(time.Now().Add(5 * time.Second))
							_, _ = io.Copy(c, c)
						}
					}()
					var connection net.Conn
					if engine == core.EngineShadowsocksRust {
						localPort := importLivePort(t)
						client := exec.CommandContext(ctx, filepath.Join(root, "sslocal"), "-s", "127.0.0.1:"+strconv.Itoa(port), "-b", "127.0.0.1:"+strconv.Itoa(localPort), "-m", "aes-256-gcm", "-k", "fixture-password")
						configureCommand(client)
						configureCommandIdentity(client, &identity)
						if err := client.Start(); err != nil {
							t.Fatal(err)
						}
						defer func() { _ = client.Cancel(); _ = client.Wait() }()
						if !importLiveWait(localPort, 5*time.Second) {
							t.Fatal("SS local startup")
						}
						dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), nil, &net.Dialer{Timeout: 5 * time.Second})
						if err != nil {
							t.Fatal(err)
						}
						connection, err = dialer.Dial("tcp", backend.Addr().String())
						if err != nil {
							t.Fatal(err)
						}
					} else {
						roots := x509.NewCertPool()
						roots.AppendCertsFromPEM([]byte(pair.Certificate))
						connection, err = tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", fmt.Sprintf("127.0.0.1:%d", port), &tls.Config{ServerName: "import.example.test", RootCAs: roots, MinVersion: tls.VersionTLS12})
						if err != nil {
							t.Fatal(err)
						}
						sum := sha256.Sum224([]byte("fixture-password"))
						header := []byte(hex.EncodeToString(sum[:]) + "\r\n")
						header = append(header, 1, 1, 127, 0, 0, 1)
						address := backend.Addr().(*net.TCPAddr)
						header = binary.BigEndian.AppendUint16(header, uint16(address.Port))
						header = append(header, '\r', '\n')
						if _, err := connection.Write(header); err != nil {
							t.Fatal(err)
						}
					}
					defer connection.Close()
					_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
					payload := []byte("import dependency round trip")
					if _, err := connection.Write(payload); err != nil {
						t.Fatal(err)
					}
					got := make([]byte, len(payload))
					if _, err := io.ReadFull(connection, got); err != nil || !bytes.Equal(got, payload) {
						t.Fatalf("forwarding after import: %v\n%s", err, output.String())
					}
				}()
			}
		})
	}
}

func importLivePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
func importLiveWait(port int, duration time.Duration) bool {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			c.Close()
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
