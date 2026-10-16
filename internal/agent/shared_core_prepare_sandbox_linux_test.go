//go:build linux

package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Exercise the real Executor's missing-core path at the managed paths. The
// container provides a read-only rootfs; systemd and core validation are fixture
// processes, while preparation, download verification and filesystem writes run
// unchanged. Never run this against the workstation's services.
func TestSharedCoreFirstUseReadOnlySandbox(t *testing.T) {
	if os.Getenv("QCH_TEST_UPGRADE_SANDBOX") != "1" {
		t.Skip("requires isolated read-only container")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("sandbox test requires Docker")
	}
	if err := os.Mkdir("/var/lib/qch-core-prepare-probe", 0700); err == nil {
		os.Remove("/var/lib/qch-core-prepare-probe")
		t.Fatal("container root filesystem must be read-only")
	}
	root := t.TempDir()
	helper := filepath.Join(root, "systemctl")
	marker := filepath.Join(root, "service-mutation")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
  daemon-reload) exit 0 ;;
  show)
    case "$3" in
      --property=FragmentPath) printf '/etc/systemd/system/%%s@.service\n' "${2%%%%@*}" ;;
      --property=User|--property=Group) echo qcontrolhub-core ;;
      --property=DropInPaths) echo '' ;;
      *) exit 99 ;;
    esac ;;
  *) printf '%%s\n' "$*" >> '%s'; exit 99 ;;
esac
`, marker)
	if err := os.WriteFile(helper, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	previous := systemctlPath
	systemctlPath = helper
	t.Cleanup(func() { systemctlPath = previous })
	launcher := filepath.Join(root, "systemd-run")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\necho 'fixture private configuration validated'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		t.Run(string(engine), func(t *testing.T) {
			input, err := serverconfig.NewPlan(serverconfig.Protocols(engine)[0])
			if err != nil {
				t.Fatal(err)
			}
			content, err := serverconfig.Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			spec := DefaultSpecs()[engine]
			// The earlier lifecycle sandbox test may have left a managed file.
			if err := os.RemoveAll(filepath.Dir(spec.ConfigPath)); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(spec.Binary); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			archiveName, repository, asset := sharedCoreReleaseFixture(t, engine)
			digest := sha256.Sum256(asset)
			metadata, _ := json.Marshal(githubRelease{TagName: "v1.19.30", Assets: []githubReleaseAsset{{Name: archiveName, Size: int64(len(asset)), Digest: "sha256:" + hex.EncodeToString(digest[:]), BrowserDownloadURL: "https://github.com/" + repository + "/releases/download/v1.19.30/" + archiveName}}})
			requests := 0
			updater := NewCoreUpdater()
			// The executable fixture is a portable script; use one deterministic
			// release asset layout on both amd64 and arm64 test hosts.
			updater.goarch, updater.libc = "amd64", "gnu"
			updater.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				body := asset
				if request.URL.Host == "api.github.com" {
					body = metadata
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
			})}
			executor := &Executor{Specs: DefaultSpecs(), Updater: updater, Services: &ServiceManager{kind: ServiceManagerSystemd, executable: helper}, validationSystemdRunPath: launcher}
			task := core.Task{Action: core.ActionValidate, Engine: engine, SharedInstance: true, SharedTrafficID: "shr_0123456789abcdef", InstallIfMissing: true, ConfigID: "cfg_private", ConfigVersion: 1, ConfigContent: content}
			for attempt := 0; attempt < 2; attempt++ {
				output, err := executor.Execute(context.Background(), task)
				if err != nil {
					t.Fatalf("private validation attempt %d: %s %v", attempt, output, err)
				}
				if attempt == 1 && !strings.Contains(output, "keeping the current version") {
					t.Fatal("existing binary was not reused")
				}
			}
			if requests != 2 {
				t.Fatalf("expected one stable preparation, got %d download requests", requests)
			}
			if _, err := os.Stat(spec.ConfigPath); !os.IsNotExist(err) {
				t.Fatal("private validation wrote the owner's configuration")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("private validation mutated a service")
			}
			private, err := sharedInstanceSpec(engine, spec, task.SharedTrafficID, ServiceManagerSystemd)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(private.ConfigPath); !os.IsNotExist(err) {
				t.Fatal("validation deployed a configuration")
			}
		})
	}
}
