package agent

import (
	"io/fs"
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
