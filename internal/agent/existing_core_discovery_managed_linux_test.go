//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestManagedCoreUnitPolicyMatchesProjectUnits(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate discovery test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../.."))
	for engine, fileName := range map[core.Engine]string{
		core.EngineMihomo:          "qagent-mihomo.service",
		core.EngineXray:            "qagent-xray.service",
		core.EngineSingBox:         "qagent-sing-box.service",
		core.EngineShadowsocksRust: "qagent-shadowsocks-rust.service",
	} {
		spec := DefaultSpecs()[engine]
		contents, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "systemd", fileName))
		if err != nil {
			t.Fatal(err)
		}
		actual := make([]string, 0)
		for _, line := range strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n") {
			if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, ";") {
				actual = append(actual, line)
			}
		}
		expected := managedCoreUnitLines(engine, spec)
		if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
			t.Fatalf("%s project unit does not match the supported execution policy", engine)
		}
	}
}

func TestManagedCoreUnitPolicyAcceptsOnlyExactHistoricalTemplate(t *testing.T) {
	managed := DefaultSpecs()[core.EngineSingBox]
	omissions := map[string]struct{}{
		"LogNamespace=qagent-cores":                                {},
		"StandardOutput=journal":                                   {},
		"StandardError=journal":                                    {},
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE":               {},
		"AmbientCapabilities=CAP_NET_BIND_SERVICE":                 {},
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_ADMIN": {},
		"AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN":   {},
	}
	legacy := make([]string, 0)
	for _, line := range managedCoreUnitLines(core.EngineSingBox, managed) {
		if _, omitted := omissions[line]; !omitted {
			legacy = append(legacy, line)
		}
	}
	contents := []byte(strings.Join(legacy, "\n") + "\n")
	if err := validateManagedUnitFragment(contents, core.EngineSingBox, managed); err != nil {
		t.Fatalf("exact historical QControlHub unit was rejected: %v", err)
	}
	contents = append(contents, []byte("EnvironmentFile=/etc/unknown\n")...)
	if err := validateManagedUnitFragment(contents, core.EngineSingBox, managed); err == nil {
		t.Fatal("historical unit with an unknown directive was accepted")
	}
}
