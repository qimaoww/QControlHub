package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"go.yaml.in/yaml/v4"
)

// This test creates real managed services on an empty disposable systemd node.
func TestTLSMigrationLiveGroup(t *testing.T) {
	if os.Getenv("QCH_TEST_TLS_MIGRATION_NODE") != "1" {
		t.Skip("requires isolated pre-provisioned node")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires disposable container")
	}
	if _, err := os.Lstat(tlsFrontendRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fixture requires empty TLS state")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	manager, err := NewServiceManager(ServiceManagerSystemd)
	if err != nil {
		t.Fatal(err)
	}
	m := tlsFrontendManager{root: tlsFrontendRoot, state: tlsFrontendState, services: manager}
	if err = m.prepareDirectories(); err != nil {
		t.Fatal(err)
	}
	id, err := managedCoreServiceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	cert, key := xhttpTestCertificate(t, t.TempDir())
	certData, _ := os.ReadFile(cert)
	keyData, _ := os.ReadFile(key)
	op := core.TLSMigration{ID: "tsk_0000000000000001", Mode: "caddy", Port: 443}
	before := tlsDeployments{}
	var originalWS serverconfig.Input
	for i, engine := range []core.Engine{core.EngineXray, core.EngineMihomo, core.EngineSingBox} {
		spec := DefaultSpecs()[engine]
		protocol := serverconfig.ProtocolXHTTPTLS
		if engine == core.EngineSingBox {
			protocol = "vless-ws-tls"
		}
		preset, _ := serverconfig.FindProtocol(engine, protocol)
		p, err := serverconfig.NewPlan(preset)
		if err != nil {
			t.Fatal(err)
		}
		p.Port = 32001 + i
		p.Tag = fmt.Sprintf("backend-%d", i)
		p.TransportPath = fmt.Sprintf("/route%d", i)
		p.PanelCertificateID = "crt_0123456789abcdef"
		p.TLSPublicPort = 443
		p.TLSServerName = "tls.example.test"
		p.TLSMode = "nginx"
		p.BlockMainlandDestination = false
		dir := filepath.Dir(spec.ConfigPath)
		certDir := filepath.Join(dir, "panel-certificates", p.PanelCertificateID, "v1")
		if err = os.MkdirAll(certDir, 0755); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{"cert.pem": certData, "key.pem": keyData} {
			path := filepath.Join(certDir, name)
			if err = os.WriteFile(path, data, 0640); err != nil {
				t.Fatal(err)
			}
			if err = os.Chown(path, 0, int(id.gid)); err != nil {
				t.Fatal(err)
			}
		}
		if engine == core.EngineSingBox {
			originalWS = p
		}
		content, err := serverconfig.Generate(engine, p)
		if err != nil {
			t.Fatal(err)
		}
		metadata, err := serverconfig.MarshalClientMetadataWithPrevious(p, "")
		if err != nil {
			t.Fatal(err)
		}
		fronts, err := serverconfig.ManagedTLSFrontends(engine, content, map[string]string{p.Tag: metadata})
		if err != nil || len(fronts) != 1 {
			t.Fatal("invalid fixture frontend")
		}
		f := fronts[0]
		f.CertificatePath = filepath.Join(certDir, "cert.pem")
		f.PrivateKeyPath = filepath.Join(certDir, "key.pem")
		before[spec.Service] = tlsDeployment{Frontends: []core.TLSFrontend{f}}
		op.Routes = append(op.Routes, core.TLSMigrationRoute{Engine: engine, Tag: f.Tag, Transport: f.Transport, Path: f.Path, Domain: f.Domain, From: f.Mode, BackendPort: f.BackendPort, CertificateID: p.PanelCertificateID, CertificateVersion: 1})
		var doc map[string]any
		if err = yaml.Unmarshal([]byte(content), &doc); err != nil {
			t.Fatal(err)
		}
		args := "run -config " + spec.ConfigPath
		switch engine {
		case core.EngineMihomo:
			args = "-d " + dir + " -f " + spec.ConfigPath
			doc["rules"] = []string{"MATCH,DIRECT"}
		case core.EngineXray:
			for _, raw := range doc["outbounds"].([]any) {
				out := raw.(map[string]any)
				if out["protocol"] == "freedom" {
					out["settings"] = map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.0/8"}}}}
				}
			}
		case core.EngineSingBox:
			args = "run -c " + spec.ConfigPath
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err = m.writeConfig(spec.ConfigPath, string(raw), false); err != nil {
			t.Fatal(err)
		}
		unit := "[Service]\nUser=qcontrolhub-core\nEnvironment=HOME=/tmp\nExecStart=" + spec.Binary + " " + args + "\n"
		if err = os.WriteFile("/etc/systemd/system/"+spec.Service, []byte(unit), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range [][]string{{"daemon-reload"}, {"start", "qagent-xray", "qagent-mihomo", "qagent-sing-box"}} {
		if err = exec.Command("systemctl", cmd...).Run(); err != nil {
			t.Fatal("backend service startup failed", err)
		}
	}
	_, unit, definition := m.serviceDefinition("nginx", false)
	if err = os.WriteFile(unit, []byte(definition), 0644); err != nil {
		t.Fatal(err)
	}
	if err = exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		t.Fatal(err)
	}
	if err = m.sync(ctx, before); err != nil {
		t.Fatal(err)
	}
	if err = m.migrationFile("deployments.json", before); err != nil {
		t.Fatal(err)
	}
	executor := Executor{Services: manager, tlsFrontendHelperPath: "/usr/local/bin/qagent"}
	run := func(want string) *core.TLSMigrationResult {
		t.Helper()
		result, runErr := executor.runTLSMigration(ctx, core.Task{ID: op.ID, Action: core.ActionMigrateTLS, TLSMigration: &op})
		if result == nil {
			t.Fatal("no receipt", runErr)
		}
		if result.State != want || (runErr == nil) != (want == "succeeded") {
			t.Fatalf("state=%s step=%s paths=%v", result.State, result.Step, result.Paths)
		}
		t.Logf("state=%s step=%s paths=%v", result.State, result.Step, result.Paths)
		return result
	}
	// Corrupt just one route after the real Caddy binary validated the staged file.
	// The service and all three clients run normally inside this disposable node.
	binary := m.proxyBinary("caddy")
	if err = os.Rename(binary, binary+".real"); err != nil {
		t.Fatal(err)
	}
	wrapper := `#!/bin/sh
if [ "$1" = reload ] && [ -f /run/qch-fixture-fail-reload ]; then exit 1; fi
if [ "$1" = run ]; then
  case $(cat /run/qch-fixture-fault 2>/dev/null) in
    route|restore)
      sed 's/127.0.0.1:32002/127.0.0.1:32001/g' "$3" > "$HOME/fixture.json"
      set -- run --config "$HOME/fixture.json";;
    port)
      /usr/bin/python3 -c 'import socket,time;s=socket.socket();s.bind(("127.0.0.1",443));s.listen();open("/var/lib/qcontrolhub-tls/fixture.bound","w").close();time.sleep(20)' &
      for i in $(seq 1 100); do [ -f "$HOME/fixture.bound" ] && break; sleep .01; done;;
  esac
fi
exec ` + binary + `.real "$@"
`
	if err = os.WriteFile(binary, []byte(wrapper), 0755); err != nil {
		t.Fatal(err)
	}
	for i, fault := range []string{"route", "port", "restore", "crash", "reload", "none"} {
		if err = os.WriteFile("/run/qch-fixture-fault", []byte(fault), 0644); err != nil {
			t.Fatal(err)
		}
		op.ID = fmt.Sprintf("tsk_%016x", i+1)
		if fault == "reload" {
			spec := DefaultSpecs()[core.EngineSingBox]
			dep := before[spec.Service]
			second := dep.Frontends[0]
			second.Mode = "caddy"
			second.PublicPort = 34443
			second.Tag = "isolated-reload"
			dep.Frontends = append(dep.Frontends, second)
			before[spec.Service] = dep
			if err = m.sync(ctx, before); err != nil {
				t.Fatal(err)
			}
			if err = m.migrationFile("deployments.json", before); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile("/run/qch-fixture-fail-reload", nil, 0644); err != nil {
				t.Fatal(err)
			}
		}
		if fault == "crash" {
			// Run the same privileged executable in a separate process and kill
			// it after listener handoff, before a durable result exists.
			request := tlsFrontendRequest{Action: core.ActionMigrateTLS, Migration: &op, Service: DefaultSpecs()[core.EngineSingBox].Service, Kind: ServiceManagerSystemd, Deadline: tlsOperationDeadline(ctx)}
			name, err := writeTLSFrontendRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(filepath.Join(tlsFrontendRequests, name))
			helper := exec.CommandContext(ctx, "/usr/local/bin/qagent", "manage-tls", name)
			if err = helper.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(12 * time.Second)
			phasePath := filepath.Join(m.root, op.ID+".phase")
			for time.Now().Before(deadline) {
				if phase, _ := os.ReadFile(phasePath); string(phase) == `"verifying"` {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			phase, _ := os.ReadFile(phasePath)
			killErr := helper.Process.Kill()
			_ = helper.Wait()
			if string(phase) != `"verifying"` || killErr != nil {
				t.Fatal("failed to interrupt the live verification phase")
			}
			if _, err := os.Stat(filepath.Join(m.root, op.ID+".result")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unexpected receipt after helper termination")
			}
		}
		if fault == "none" {
			watch := watchMigrationConnections(t, certData, originalWS)
			run("succeeded")
			watch()
			run("succeeded")
			break
		}
		if fault == "restore" {
			// The existing Nginx process remains healthy. Only its next start
			// fails, after Caddy has taken over and failed route verification.
			path := m.nginxBinary()
			if err = os.Rename(path, path+".real"); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			result := run("recovery_incomplete")
			if result.Code != "rollback_failed" {
				t.Fatal("missing recovery failure code")
			}
			if err = os.Rename(path+".real", path); err != nil {
				t.Fatal(err)
			}
			if err = exec.Command("systemctl", "reset-failed", "qagent-tls-nginx").Run(); err != nil {
				t.Fatal(err)
			}
		}
		result := run("rolled_back")
		if fault == "reload" {
			if err = os.Remove("/run/qch-fixture-fail-reload"); err != nil {
				t.Fatal(err)
			}
		}
		for i, path := range result.Paths {
			if path.Recovery != "succeeded" {
				t.Fatal("original client failed after rollback")
			}
			if fault == "route" && (path.Candidate == "succeeded") != (i != 1) {
				t.Fatal("wrong backend went undetected", i)
			}
		}
		loaded, err := m.load()
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range loaded {
			for _, f := range d.Frontends {
				if f.PublicPort == 443 && !strings.EqualFold(f.Mode, "nginx") {
					t.Fatal("original frontend lost")
				}
			}
		}
	}
}

// Measure an authenticated WebSocket stream established before the handoff
// and fresh upgrade attempts during it, using the original certificate.
func watchMigrationConnections(t *testing.T, cert []byte, p serverconfig.Input) func() {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("bad fixture certificate")
	}
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{ServerName: p.TLSServerName, RootCAs: roots, MinVersion: tls.VersionTLS12}, HandshakeTimeout: 300 * time.Millisecond}
	dest := "wss://127.0.0.1:443" + p.TransportPath
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		c, err := echo.Accept()
		if err == nil {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}
	}()
	conn, _, err := dialer.Dial(dest, nil)
	if err != nil {
		echo.Close()
		t.Fatal("original WebSocket unavailable")
	}
	cred, err := hex.DecodeString(strings.ReplaceAll(p.Credential, "-", ""))
	if err != nil || len(cred) != 16 {
		t.Fatal("invalid fixture credential")
	}
	port := echo.Addr().(*net.TCPAddr).Port
	first := append([]byte{0}, cred...)
	first = append(first, 0, 1, byte(port>>8), byte(port), 1, 127, 0, 0, 1)
	first = append(first, 42)
	if err = conn.WriteMessage(websocket.BinaryMessage, first); err != nil {
		t.Fatal(err)
	}
	// VLESS version/addon response and the first echo can arrive separately.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var received []byte
	for len(received) < 3 {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatal("old connection data failed")
		}
		received = append(received, msg...)
	}
	if received[2] != 42 {
		t.Fatal("unexpected echo")
	}
	stop := make(chan struct{})
	type stats struct {
		upgrades, failures int
		longest            time.Duration
	}
	done := make(chan stats, 1)
	go func() {
		var counts stats
		var firstFailure time.Time
		for {
			c, _, e := dialer.Dial(dest, nil)
			now := time.Now()
			if e == nil {
				c.Close()
				counts.upgrades++
				if !firstFailure.IsZero() {
					counts.longest = max(counts.longest, now.Sub(firstFailure))
					firstFailure = time.Time{}
				}
			} else {
				counts.failures++
				if firstFailure.IsZero() {
					firstFailure = now
				}
			}
			select {
			case <-stop:
				done <- counts
				return
			case <-time.After(15 * time.Millisecond):
			}
		}
	}()
	return func() {
		close(stop)
		counts := <-done
		defer conn.Close()
		defer echo.Close()
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		e := conn.WriteMessage(websocket.BinaryMessage, []byte{43})
		continuous := false
		if e == nil {
			_, msg, e := conn.ReadMessage()
			continuous = e == nil && len(msg) == 1 && msg[0] == 43
		}
		t.Logf("existing authenticated WS survived=%t; fresh upgrades=%d failed=%d longest sampled reconnect gap=%s", continuous, counts.upgrades, counts.failures, counts.longest.Round(time.Millisecond))
	}
}
