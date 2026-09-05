package qcontrolhub

import (
	"bytes"
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestCoreInstallAssetsMatchRepositoryAllowlist(t *testing.T) {
	want := map[string]bool{
		"deploy/bootstrap-core-services.sh": true, "deploy/existing-core-mapping.sh": true,
		"deploy/systemd/qagent-core-journal.conf": true,
		"deploy/systemd/qagent-mihomo.service":    true, "deploy/systemd/qagent-xray.service": true,
		"deploy/systemd/qagent-sing-box.service": true, "deploy/systemd/qagent-shadowsocks-rust.service": true,
		"deploy/openrc/qagent-mihomo": true, "deploy/openrc/qagent-xray": true,
		"deploy/openrc/qagent-sing-box": true, "deploy/openrc/qagent-shadowsocks-rust": true,
		"examples/configs/mihomo-minimal.yaml": true, "examples/configs/xray-minimal.json": true,
		"examples/configs/sing-box-minimal.json": true, "examples/configs/shadowsocks-rust-minimal.json": true,
	}
	err := fs.WalkDir(CoreInstallAssets(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !want[name] {
			t.Errorf("unexpected embedded install asset %s", name)
		}
		delete(want, name)
		embedded, err := fs.ReadFile(CoreInstallAssets(), name)
		if err != nil {
			return err
		}
		repository, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if len(embedded) == 0 || !bytes.Equal(embedded, repository) {
			t.Errorf("embedded asset differs from repository: %s", name)
		}
		return nil
	})
	if err != nil || len(want) != 0 {
		t.Fatalf("incomplete install bundle: %v, missing=%v", err, want)
	}
	mapping, _ := fs.ReadFile(CoreInstallAssets(), "deploy/existing-core-mapping.sh")
	if !strings.Contains(string(mapping), "qagent_ssrust_binary") {
		t.Fatal("binary bundle lacks the SS Rust import fix")
	}
}
