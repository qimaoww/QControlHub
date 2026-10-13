//go:build linux

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestManagedSourcePermissionsSurviveAgentUmaskAndLegacyReuse(t *testing.T) {
	if os.Getenv("QCH_TEST_SOURCE_UMASK_CHILD") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestManagedSourcePermissionsSurviveAgentUmaskAndLegacyReuse$")
		command.Env = append(os.Environ(), "QCH_TEST_SOURCE_UMASK_CHILD=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("umask regression: %v\n%s", err, output)
		}
		return
	}
	syscall.Umask(0o077)
	directory := t.TempDir()
	content := `{"inbounds":[{"tag":"a","port":1080}],"outbounds":[{"tag":"direct"}]}`
	metadata := fileMetadata{mode: 0o640, uid: os.Getuid(), gid: os.Getgid(), ownershipKnown: true}
	destination := filepath.Join(directory, "config.json")
	if err := writeManagedConfigFiles(core.EngineXray, destination, content, metadata); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	bundle := filepath.Join(directory, "sources-v3-"+hex.EncodeToString(sum[:]))
	fragment := filepath.Join(bundle, "inbounds/a.json")
	info, err := os.Stat(fragment)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("0077 umask removed service group access: %v %v", info, err)
	}
	if err := os.Chmod(fragment, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(fragment), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigFiles(core.EngineXray, destination, content, metadata); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(fragment)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("legacy bundle was not repaired: %v %v", info, err)
	}
	value, err := os.ReadFile(fragment)
	if err != nil || !strings.Contains(string(value), `"tag": "a"`) {
		t.Fatalf("metadata migration changed source contents: %s %v", value, err)
	}
}

// Only run in a fresh disposable OpenRC container. Use the actual installed
// paths and production sync function, not a shell imitation of the migration.
func TestInstalledOpenRCCorePermissionMigration(t *testing.T) {
	if os.Getenv("QCH_LIVE_CORE_PERMISSION_MIGRATION") != "1" {
		t.Skip("opt-in disposable OpenRC migration fixture")
	}
	if os.Geteuid() != 0 || os.Getenv("QCH_SERVICE_MANAGER") != ServiceManagerOpenRC {
		t.Fatal("requires root and OpenRC in a disposable container")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("refusing installed-path fixture outside a disposable Docker container")
	}
	manager, err := NewServiceManager(ServiceManagerOpenRC)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	specs := DefaultSpecsForServiceManager(ServiceManagerOpenRC)
	var instances []EngineSpec
	active := map[string]bool{}
	beforePIDs := map[string]int{}
	for _, engine := range core.AllEngines() {
		base := specs[engine]
		for _, path := range []string{base.Binary, base.ConfigPath, filepath.Join(openRCInitRoot, base.Service)} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("refusing to overwrite installed fixture path %s", path)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_ = os.MkdirAll("/run/openrc", 0o755)
	_ = os.WriteFile("/run/openrc/softlevel", []byte("default\n"), 0o644)
	for index, engine := range core.AllEngines() {
		base := specs[engine]
		if err := os.MkdirAll(filepath.Dir(base.Binary), 0o755); err != nil {
			t.Fatal(err)
		}
		value, err := os.ReadFile(filepath.Join(os.Getenv("QCH_PERMISSION_BIN"), filepath.Base(base.Binary)))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base.Binary, value, 0o755); err != nil {
			t.Fatal(err)
		}
		shared, _ := sharedInstanceSpec(engine, base, "shr_0000000000000001", ServiceManagerOpenRC)
		for position, spec := range []EngineSpec{base, shared} {
			instances = append(instances, spec)
			state := "/var/lib/qcontrolhub-" + managedCoreAssetName(engine)
			if position == 1 {
				state += "-shr_0000000000000001"
			}
			t.Cleanup(func() {
				_, _ = manager.command(context.Background(), spec.Service, core.ActionStop)
				_ = os.Remove(filepath.Join(openRCInitRoot, spec.Service))
				_ = os.RemoveAll(state)
				_ = os.RemoveAll(filepath.Dir(spec.ConfigPath))
			})
			if err := os.MkdirAll(filepath.Dir(spec.ConfigPath), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(state, 0o750); err != nil {
				t.Fatal(err)
			}
			_ = os.Chown(state, int(identity.uid), int(identity.gid))
			var script string
			if position == 0 {
				asset, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/openrc/"+base.Service)
				if err != nil {
					t.Fatal(err)
				}
				script = string(asset)
			} else {
				script, err = openRCSharedInstanceContent(engine, spec)
				if err != nil {
					t.Fatal(err)
				}
			}
			start, end := strings.Index(script, "\n# QCH core bounding policy begin\n"), strings.Index(script, "# QCH core bounding policy end\n")
			script = script[:start] + script[end+len("# QCH core bounding policy end\n"):]
			script = strings.ReplaceAll(script, "^cap_net_bind_service,^cap_net_admin", "^cap_net_bind_service")
			if err := os.WriteFile(filepath.Join(openRCInitRoot, spec.Service), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			p := listener.Addr().(*net.TCPAddr).Port
			_ = listener.Close()
			content := fmt.Sprintf(`{"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"socks","settings":{"udp":true}}],"outbounds":[{"protocol":"freedom"}]}`, p)
			switch engine {
			case core.EngineMihomo:
				content = fmt.Sprintf("mixed-port: %d\nallow-lan: false\n", p)
			case core.EngineSingBox:
				content = fmt.Sprintf(`{"inbounds":[{"type":"socks","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct"}]}`, p)
			case core.EngineShadowsocksRust:
				content = fmt.Sprintf(`{"server":"127.0.0.1","server_port":%d,"method":"aes-256-gcm","password":"fixture-only","mode":"tcp_and_udp"}`, p)
				acl := shadowsocksRustACLPath
				if spec.ACLPath != "" {
					acl = spec.ACLPath
				}
				_ = os.WriteFile(acl, []byte("[outbound_block_list]\n"), 0o640)
				_ = os.Chown(acl, 0, int(identity.gid))
			}
			_ = os.WriteFile(spec.ConfigPath, []byte(content), 0o640)
			if _, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, spec.ConfigPath); err != nil {
				t.Fatal(err)
			}
			active[spec.Service] = index%2 == position
			if active[spec.Service] {
				if output, err := manager.command(ctx, spec.Service, core.ActionStart); err != nil {
					t.Fatalf("start historical %s: %v %s", spec.Service, err, output)
				}
				if _, err := waitForServiceState(ctx, "active", 500*time.Millisecond, 100*time.Millisecond, func(ctx context.Context) (string, error) { return manager.status(ctx, spec.Service) }); err != nil {
					t.Fatal(err)
				}
				pid, err := managedServiceProcessPID(ctx, manager, spec.Service)
				if err != nil {
					t.Fatal(err)
				}
				beforePIDs[spec.Service] = pid
				if err := verifyManagedServiceProcessPermissions(ctx, manager, spec.Service); err == nil {
					t.Fatal("historical capability loss was not reproduced")
				}
			}
			// Simulate old atomic writes and stale root-owned cache contents.
			_ = os.Chmod(spec.ConfigPath, 0o600)
			_ = os.Chown(spec.ConfigPath, 0, 0)
			_ = os.WriteFile(filepath.Join(state, "old-cache"), []byte("preserve-me"), 0o600)
		}
		_ = os.Chmod(base.Binary, 0o750)
	}
	executor := &Executor{Specs: specs, Services: manager}
	if err := executor.syncManagedCorePermissions(ctx); err != nil {
		t.Fatal(err)
	}
	newPIDs := map[string]int{}
	for _, spec := range instances {
		status, err := manager.status(ctx, spec.Service)
		if err != nil || (status == "active") != active[spec.Service] {
			t.Fatalf("migration changed running state %s: %s %v", spec.Service, status, err)
		}
		if active[spec.Service] {
			pid, _ := managedServiceProcessPID(ctx, manager, spec.Service)
			newPIDs[spec.Service] = pid
			if pid == beforePIDs[spec.Service] {
				t.Fatal("old process survived changed permissions")
			}
			if err := verifyManagedServiceProcessPermissions(ctx, manager, spec.Service); err != nil {
				t.Fatal(err)
			}
		}
		info, _ := os.Stat(spec.ConfigPath)
		if metadataFromFileInfo(info).gid != int(identity.gid) || info.Mode().Perm() != 0o640 {
			t.Fatal("existing configuration group access not restored")
		}
		engine, _, _ := managedSpecForService(spec.Service, ServiceManagerOpenRC)
		state := "/var/lib/qcontrolhub-" + managedCoreAssetName(engine)
		if isSharedInstanceSpec(engine, spec, ServiceManagerOpenRC) {
			state += "-shr_0000000000000001"
		}
		cache := filepath.Join(state, "old-cache")
		value, err := os.ReadFile(cache)
		info, statErr := os.Stat(cache)
		if err != nil || statErr != nil || string(value) != "preserve-me" || metadataFromFileInfo(info).uid != int(identity.uid) {
			t.Fatalf("cache contents/ownership not preserved: %s %v %v", value, err, statErr)
		}
	}
	if err := executor.syncManagedCorePermissions(ctx); err != nil {
		t.Fatal(err)
	}
	for service, pid := range newPIDs {
		current, _ := managedServiceProcessPID(ctx, manager, service)
		if current != pid {
			t.Fatalf("repeat sync restarted already repaired %s", service)
		}
	}
	t.Log("all eight old services migrated; active PIDs changed once, stopped services stayed stopped, repeat synchronization preserved PIDs and cache contents")
}
