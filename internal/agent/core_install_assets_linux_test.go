//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestBundledCoreInstallAssetsPublishAndReuse(t *testing.T) {
	requireAgentRoot(t)
	directory := filepath.Join(t.TempDir(), "core-install")
	script, err := stageBundledCoreInstallAssets(directory)
	if err != nil {
		t.Fatal(err)
	}
	assets, digest, err := bundledCoreInstallAssets()
	if err != nil || script != filepath.Join(directory, digest, coreInstallBootstrapName) {
		t.Fatalf("unexpected bundle path %q: %v", script, err)
	}
	for _, asset := range assets {
		path := filepath.Join(directory, digest, asset.name)
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != string(asset.content) {
			t.Fatalf("staged asset %s: %v", asset.name, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != asset.mode {
			t.Fatalf("staged permissions %s: %v", asset.name, err)
		}
	}
	before, _ := os.Stat(script)
	reused, err := stageBundledCoreInstallAssets(directory)
	after, _ := os.Stat(script)
	if err != nil || reused != script || !os.SameFile(before, after) {
		t.Fatalf("bundle was rewritten on retry: %v", err)
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 1 || entries[0].Name() != digest {
		t.Fatalf("staging debris or unexpected bundle entries: %v", entries)
	}
}

func TestBundledCoreInstallAssetsFailClosedOnTampering(t *testing.T) {
	requireAgentRoot(t)
	for _, mutation := range []string{"content", "mode", "missing", "symlink", "directory-symlink", "owner", "extra"} {
		t.Run(mutation, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "core-install")
			script, err := stageBundledCoreInstallAssets(directory)
			if err != nil {
				t.Fatal(err)
			}
			bundle := filepath.Dir(filepath.Dir(script))
			mapping := filepath.Join(bundle, "deploy/existing-core-mapping.sh")
			switch mutation {
			case "content":
				err = os.WriteFile(mapping, []byte("custom operator script\n"), 0o644)
			case "mode":
				err = os.Chmod(mapping, 0o666)
			case "missing":
				err = os.Remove(mapping)
			case "symlink":
				err = os.Remove(mapping)
				if err == nil {
					err = os.Symlink(script, mapping)
				}
			case "directory-symlink":
				err = os.Rename(filepath.Join(bundle, "deploy"), filepath.Join(bundle, "deploy-original"))
				if err == nil {
					err = os.Symlink("deploy-original", filepath.Join(bundle, "deploy"))
				}
			case "owner":
				err = os.Chown(mapping, 12345, 12345)
			case "extra":
				err = os.WriteFile(filepath.Join(bundle, "unexpected.sh"), []byte("exit 0\n"), 0o755)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stageBundledCoreInstallAssets(directory); err == nil {
				t.Fatal("unsafe bundle was accepted or silently replaced")
			}
			if mutation == "content" {
				content, _ := os.ReadFile(mapping)
				if string(content) != "custom operator script\n" {
					t.Fatal("modified resource was overwritten")
				}
			}
		})
	}
}

func TestBundledCoreInstallAssetsRejectUnsafeRoot(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	directory := filepath.Join(root, "core-install")
	if err := os.Symlink(root, directory); err != nil {
		t.Fatal(err)
	}
	if _, err := stageBundledCoreInstallAssets(directory); err == nil {
		t.Fatal("symlinked bundle root accepted")
	}
	if _, err := stageBundledCoreInstallAssets("relative/assets"); err == nil {
		t.Fatal("relative bundle root accepted")
	}
	unsafe := filepath.Join(root, "writable")
	if err := os.Mkdir(unsafe, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := stageBundledCoreInstallAssets(filepath.Join(unsafe, "assets")); err == nil {
		t.Fatal("writable parent accepted")
	}
}

func TestBundledCoreInstallAssetsIgnoreInterruptedAndOldBundles(t *testing.T) {
	requireAgentRoot(t)
	directory := filepath.Join(t.TempDir(), "core-install")
	for _, name := range []string{"interrupted.tmp", strings.Repeat("0", 64)} {
		if err := os.MkdirAll(filepath.Join(directory, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name, "keep"), []byte("old resource"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := stageBundledCoreInstallAssets(directory); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	for _, name := range []string{"interrupted.tmp", strings.Repeat("0", 64)} {
		if content, err := os.ReadFile(filepath.Join(directory, name, "keep")); err != nil || string(content) != "old resource" {
			t.Fatalf("old/interrupted bundle was altered: %v", err)
		}
	}
}

func TestBundledCoreInstallAssetsBootstrapAfterBinaryOnlyUpgrade(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy-bootstrap.sh")
	legacyContent := "#!/bin/sh\necho stale installer must not execute >&2\nexit 99\n"
	if err := os.WriteFile(legacy, []byte(legacyContent), 0o755); err != nil {
		t.Fatal(err)
	}
	argumentsPath := filepath.Join(root, "arguments")
	launcher := filepath.Join(root, "systemd-run")
	// A read-only stand-in for systemd-run: verify the effective script's
	// bundled sibling resources, but never install or change real services.
	launcherContent := fmt.Sprintf("#!/bin/sh\nset -eu\nprintf '%%s\\n' \"$@\" > %s\nwhile [ \"$1\" != -- ]; do shift; done\nshift\nsh -n \"$1\"\ngrep -q qagent_ssrust_binary \"$(dirname \"$1\")/existing-core-mapping.sh\"\n", shellQuote(argumentsPath))
	if err := os.WriteFile(launcher, []byte(launcherContent), 0o755); err != nil {
		t.Fatal(err)
	}
	bootstrapper := defaultCoreServiceBootstrapper()
	if bootstrapper.assetRoot != coreInstallAssetRoot || bootstrapper.scriptPath != "" {
		t.Fatal("default bootstrap still depends on installer-cached assets")
	}
	bootstrapper.assetRoot = filepath.Join(root, "core-install")
	bootstrapper.scriptPath = legacy
	bootstrapper.systemdRunPath = launcher
	bootstrapper.preserveUnit = true
	if err := bootstrapper.install(context.Background(), core.EngineShadowsocksRust, true, defaultSystemdServiceManager()); err != nil {
		t.Fatalf("binary-only upgrade bootstrap: %v", err)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil || strings.Contains(string(arguments), legacy) || !strings.Contains(string(arguments), "--setenv=QCH_SKIP_CORE_SERVICES=shadowsocks-rust") || !strings.Contains(string(arguments), bootstrapper.assetRoot) {
		t.Fatalf("unexpected bootstrap invocation: %v\n%s", err, arguments)
	}
	if content, err := os.ReadFile(legacy); err != nil || string(content) != legacyContent {
		t.Fatal("legacy installation resource was overwritten")
	}
	if !strings.Contains(string(arguments), "--setenv=QCH_PRESERVE_CORE_UNIT=1") {
		t.Fatal("prerequisite repair did not preserve the pre-transaction unit")
	}
}

func TestPerServiceManagerBundledCoreInstallAssets(t *testing.T) {
	requireAgentRoot(t)
	script, err := stageBundledCoreInstallAssets(filepath.Join(t.TempDir(), "core-install"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"qagent-mihomo", "qagent-xray", "qagent-sing-box", "qagent-shadowsocks-rust"} {
		path := filepath.Join(filepath.Dir(script), "openrc", name)
		if err := validatePrivilegedExecutable(path); err != nil {
			t.Fatalf("missing executable OpenRC resource: %v", err)
		}
	}
}

func TestBundledCoreInstallAssetsWithExistingAgentPermissions(t *testing.T) {
	requireAgentRoot(t)
	if _, err := exec.LookPath("setpriv"); err != nil {
		t.Skip("setpriv is unavailable")
	}
	// Match the existing service's restrictive umask and capability bound.
	// No CAP_FOWNER, CAP_DAC_OVERRIDE, changed unit, or privileged updater is
	// needed for an already-installed Agent to stage its new bundle.
	command := exec.Command("sh", "-c", `umask 077; exec "$@"`, "sh",
		"setpriv", "--no-new-privs", "--bounding-set=-all,+chown", "--",
		os.Args[0], "-test.run=^TestBundledCoreInstallAssetsPublishAndReuse$", "-test.count=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("existing Agent permissions cannot stage assets: %v\n%s", err, output)
	}
	unit, err := os.ReadFile("../../deploy/systemd/qagent.service")
	if err != nil || !strings.Contains(string(unit), "ReadWritePaths=-"+filepath.Dir(coreInstallAssetRoot)+"\n") {
		t.Fatalf("asset namespace is outside the existing service write allowance: %v", err)
	}
}
