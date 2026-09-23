package agent

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedCoreInstanceServicesCoverEveryEngineAndManager(t *testing.T) {
	shareID := "shr_0000000000000001"
	for _, manager := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		for _, engine := range core.AllEngines() {
			t.Run(manager+"/"+string(engine), func(t *testing.T) {
				assetName := managedCoreAssetName(engine)
				base := DefaultSpecsForServiceManager(manager)[engine]
				spec, err := sharedInstanceSpec(engine, base, shareID, manager)
				if err != nil || !isSharedInstanceSpec(engine, spec, manager) {
					t.Fatalf("instance spec: %+v %v", spec, err)
				}
				if !strings.Contains(spec.ConfigPath, assetName+"/shares/"+shareID) {
					t.Fatalf("config path is not private: %s", spec.ConfigPath)
				}
				statePath := filepath.Join("/var/lib", "qcontrolhub-"+assetName+"-"+shareID)
				if manager == ServiceManagerOpenRC {
					content, err := openRCSharedInstanceContent(engine, spec)
					if err != nil {
						t.Fatal(err)
					}
					for _, value := range []string{spec.Service, spec.ConfigPath, statePath,
						"/var/log/qagent/" + spec.Service + ".log", "^cap_net_bind_service,^cap_net_admin"} {
						if !strings.Contains(content, value) {
							t.Errorf("OpenRC script lacks %q", value)
						}
					}
					if engine == core.EngineShadowsocksRust && !strings.Contains(content, spec.ACLPath) {
						t.Error("OpenRC script lacks private ACL path")
					}
					return
				}
				unit, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/systemd/qagent-"+assetName+"@.service")
				if err != nil {
					t.Fatal(err)
				}
				for _, value := range []string{"/etc/qagent/" + assetName + "/shares/%i/",
					"/var/lib/qcontrolhub-" + assetName + "-%i", "StateDirectory=qcontrolhub-" + assetName + "-%i", "CAP_NET_ADMIN"} {
					if !strings.Contains(string(unit), value) {
						t.Errorf("systemd unit lacks %q", value)
					}
				}
			})
		}
	}
}

func TestSharedFailureDoesNotStopUnrelatedBaseCore(t *testing.T) {
	configs := map[core.Engine]string{
		core.EngineMihomo:          "listeners: [{name: owner, type: http, port: 21001}]\n",
		core.EngineXray:            `{"inbounds":[{"tag":"owner","protocol":"http","port":21001}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`,
		core.EngineSingBox:         `{"inbounds":[{"tag":"owner","type":"http","listen_port":21001}],"outbounds":[{"tag":"direct","type":"direct"}]}`,
		core.EngineShadowsocksRust: `{"server":"0.0.0.0","server_port":21001,"method":"aes-256-gcm","password":"test-only"}`,
	}
	for _, managerKind := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		for _, engine := range core.AllEngines() {
			t.Run(managerKind+"/"+string(engine), func(t *testing.T) {
				root := t.TempDir()
				configPath := filepath.Join(root, "config")
				if err := os.WriteFile(configPath, []byte(configs[engine]), 0o600); err != nil {
					t.Fatal(err)
				}
				calls := filepath.Join(root, "calls")
				helper := filepath.Join(root, "systemctl")
				writeExecutable(t, helper, "#!/bin/sh\nif [ \"$1\" = is-active ] || [ \"$2\" = status ]; then echo active; exit 0; fi\nprintf '%s %s\\n' \"$1\" \"$2\" >> '"+calls+"'\n")
				manager := &ServiceManager{kind: managerKind, executable: helper}
				base := DefaultSpecsForServiceManager(managerKind)[engine]
				base.ConfigPath = configPath
				if err := stopLegacySharedBase(context.Background(), manager, engine, base, []int{21002}); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(calls); !os.IsNotExist(err) {
					t.Fatalf("shared port 21002 stopped the owner's port 21001: %v", err)
				}
				if err := stopLegacySharedBase(context.Background(), manager, engine, base, []int{21001}); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(calls)
				want := "stop " + base.Service + "\n"
				if managerKind == ServiceManagerOpenRC {
					want = base.Service + " stop\n"
				}
				if err != nil || string(data) != want {
					t.Fatalf("legacy shared base did not stop when its own port lost accounting: %q %v", data, err)
				}
			})
		}
	}
}
