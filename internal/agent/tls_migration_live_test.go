package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	wrapper := "#!/bin/sh\nif [ \"$1\" = run ]; then case $(cat /run/qch-fixture-fault 2>/dev/null) in\nroute) sed 's/127.0.0.1:32002/127.0.0.1:32001/g' \"$3\" > \"$HOME/fixture.json\"; set -- run --config \"$HOME/fixture.json\";;\nport) exec /usr/bin/python3 -c 'import socket,time;s=socket.socket();s.bind((\"127.0.0.1\",443));s.listen();time.sleep(20)' ;;\nesac; fi\nexec " + binary + ".real \"$@\"\n"
	if err = os.WriteFile(binary, []byte(wrapper), 0755); err != nil {
		t.Fatal(err)
	}
	for i, fault := range []string{"route", "port", "none"} {
		if err = os.WriteFile("/run/qch-fixture-fault", []byte(fault), 0644); err != nil {
			t.Fatal(err)
		}
		op.ID = fmt.Sprintf("tsk_%016x", i+1)
		if fault == "none" {
			run("succeeded")
			run("succeeded")
			break
		}
		result := run("rolled_back")
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
				if !strings.EqualFold(f.Mode, "nginx") {
					t.Fatal("original frontend lost")
				}
			}
		}
	}
}
