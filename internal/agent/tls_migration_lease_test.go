package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// The control-plane fixture streams the real lease directly to its isolated
// node. Native binaries run inside the fixture's network namespace.
func TestTLSMigrationIsolatedLease(t *testing.T) {
	if os.Getenv("QCH_TEST_TLS_MIGRATION_NODE") != "1" {
		t.Skip("requires disposable systemd node")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires isolated container")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	var task core.Task
	if err := json.NewDecoder(bufio.NewReader(os.Stdin)).Decode(&task); err != nil {
		t.Fatal("invalid test lease")
	}
	service, err := NewServiceManager(ServiceManagerSystemd)
	if err != nil {
		t.Fatal(err)
	}
	m := tlsFrontendManager{root: tlsFrontendRoot, state: tlsFrontendState, services: service}
	if task.Action == core.ActionMigrateTLS {
		e := Executor{Services: service, tlsFrontendHelperPath: "/usr/local/bin/qagent"}
		result, _ := e.runTLSMigration(ctx, task)
		if result == nil {
			t.Fatal("no runtime receipt")
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("QCH_RECEIPT %s\n", data)
		return
	}
	if task.Action != core.ActionDeploy {
		t.Fatal("unexpected setup action")
	}
	if err = m.prepareDirectories(); err != nil {
		t.Fatal(err)
	}
	spec := DefaultSpecs()[task.Engine]
	dir := filepath.Dir(spec.ConfigPath)
	if err = os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err = preparePanelCertificates(spec, &task); err != nil {
		t.Fatal(err)
	}
	// Permit the nonce echo inside this networkless fixture. These are test
	// egress rules on the backend; the VLESS credential and transport come
	// unchanged from the panel lease.
	var doc map[string]any
	if err = yaml.Unmarshal([]byte(task.ConfigContent), &doc); err != nil {
		t.Fatal("invalid lease configuration")
	}
	args := "run -config " + spec.ConfigPath
	switch task.Engine {
	case core.EngineMihomo:
		args = "-d " + dir + " -f " + spec.ConfigPath
		doc["rules"] = []string{"MATCH,DIRECT"}
	case core.EngineSingBox:
		args = "run -c " + spec.ConfigPath
	case core.EngineXray:
		for _, v := range doc["outbounds"].([]any) {
			out := v.(map[string]any)
			if out["protocol"] == "freedom" {
				out["settings"] = map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.0/8"}}}}
			}
		}
	default:
		t.Fatal("unexpected fixture backend")
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
	_, unitPath, definition := m.serviceDefinition("nginx", false)
	if err = os.WriteFile(unitPath, []byte(definition), 0644); err != nil {
		t.Fatal(err)
	}
	if err = exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		t.Fatal(err)
	}
	if err = exec.Command("systemctl", "restart", spec.Service).Run(); err != nil {
		t.Fatal(err)
	}
	all, err := m.load()
	if err != nil {
		t.Fatal(err)
	}
	all[spec.Service] = tlsDeployment{Frontends: task.TLSFrontends}
	if err = m.sync(ctx, all); err != nil {
		t.Fatal("fixture frontend startup failed", err)
	}
	if err = m.migrationFile("deployments.json", all); err != nil {
		t.Fatal(err)
	}
}
