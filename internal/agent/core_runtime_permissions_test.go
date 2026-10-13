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

func TestCoreRuntimePermissionsDetectActualPrivilegeLossAndEscalation(t *testing.T) {
	identity := commandIdentity{uid: 123, gid: 456, groups: []uint32{456}}
	valid := "Uid: 123 123 123 123\nGid: 456 456 456 456\nGroups: 456\nCapInh: 1400\nCapEff: 1400\nCapPrm: 1400\nCapAmb: 1400\nCapBnd: 1400\nNoNewPrivs: 1\n"
	if err := validateManagedProcessStatus(valid, identity); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct{ from, to, reason string }{
		{"Uid: 123 123 123 123", "Uid: 123 0 0 0", "Uid"},
		{"Gid: 456 456 456 456", "Gid: 456 0 0 0", "Gid"},
		{"Groups: 456", "Groups: 456 0", "supplementary"},
		{"CapEff: 1400", "CapEff: 0400", "SO_MARK"},
		{"CapAmb: 1400", "CapAmb: 0", "CapAmb"},
		{"CapInh: 1400", "CapInh: 0", "CapInh"},
		{"CapPrm: 1400", "CapPrm: 21400", "CapPrm"},
		{"CapBnd: 1400", "CapBnd: ffff", "CapBnd"},
		{"NoNewPrivs: 1", "NoNewPrivs: 0", "NoNewPrivs"},
	} {
		t.Run(bad.reason, func(t *testing.T) {
			err := validateManagedProcessStatus(strings.Replace(valid, bad.from, bad.to, 1), identity)
			if err == nil || !strings.Contains(err.Error(), bad.reason) {
				t.Fatalf("actual process mismatch was not diagnosed: %v", err)
			}
		})
	}
}

func TestCertificatePermissionPathsPreserveInlineMaterial(t *testing.T) {
	for _, test := range []struct {
		engine  core.Engine
		content string
		want    string
	}{
		{core.EngineMihomo, "listeners:\n- certificate: /etc/qagent/mihomo/cert.pem\n  private-key: |\n    -----BEGIN PRIVATE KEY-----\n    inline\n", "/etc/qagent/mihomo/cert.pem"},
		{core.EngineXray, `{"inbounds":[{"tlsSettings":{"certificates":[{"certificateFile":"/etc/tls/cert.pem","key":["inline"]}]}}]}`, "/etc/tls/cert.pem"},
		{core.EngineSingBox, `{"inbounds":[{"tls":{"certificate_path":"/etc/tls/cert.pem","client_certificate_path":["/etc/tls/ca.pem"]}}]}`, "/etc/tls/cert.pem"},
	} {
		paths, err := coreCertificatePaths(test.engine, test.content)
		if err != nil || !strings.Contains(strings.Join(paths, "\n"), test.want) || strings.Contains(strings.Join(paths, "\n"), "inline") {
			t.Fatalf("%s certificate references: %v %v", test.engine, paths, err)
		}
	}
}

func TestOpenRCPermissionMigrationRecognizesHistoricalScriptCombinations(t *testing.T) {
	for engine, base := range DefaultSpecsForServiceManager(ServiceManagerOpenRC) {
		shared, err := sharedInstanceSpec(engine, base, "shr_0000000000000001", ServiceManagerOpenRC)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range []EngineSpec{base, shared} {
			var want []byte
			if spec == base {
				want, err = fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/openrc/"+base.Service)
			} else {
				var value string
				value, err = openRCSharedInstanceContent(engine, spec)
				want = []byte(value)
			}
			if err != nil {
				t.Fatal(err)
			}
			variants := managedOpenRCScriptVariants(want)
			legacy := strings.ReplaceAll(string(want), "^cap_net_bind_service,^cap_net_admin", "^cap_net_bind_service")
			start := strings.Index(legacy, "\n# QCH core bounding policy begin\n")
			end := strings.Index(legacy, "# QCH core bounding policy end\n")
			legacy = legacy[:start] + legacy[end+len("# QCH core bounding policy end\n"):]
			legacy = strings.ReplaceAll(legacy, managedSSRustLogFilter, "info")
			if !matchesAnyManagedDropIn([]byte(legacy), variants) {
				t.Fatalf("%s historical policy was not recognized", spec.Service)
			}
			if matchesAnyManagedDropIn([]byte(legacy+"command_user=root:root\n"), variants) {
				t.Fatalf("%s accepted a customized root script", spec.Service)
			}
		}
	}
}

func TestOpenRCPermissionSyncReportsConfDOverride(t *testing.T) {
	root := t.TempDir()
	previous := openRCInitRoot
	openRCInitRoot = filepath.Join(root, "init.d")
	t.Cleanup(func() { openRCInitRoot = previous })
	conf := filepath.Join(root, "conf.d")
	if err := os.Mkdir(conf, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(conf, "qagent-xray.default")
	if err := os.WriteFile(path, []byte("# administrator override\ncapabilities=\"\"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenRCCoreOverrides("qagent-xray"); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("OpenRC override was ignored: %v", err)
	}
}

func TestSingBoxAndSharedMihomoRequireNetlinkEvenWithCapabilities(t *testing.T) {
	for _, service := range []string{"qagent-sing-box.service", "qagent-mihomo@shr_0000000000000001.service", "qagent-sing-box@shr_0000000000000001.service"} {
		helper := filepath.Join(t.TempDir(), "systemctl")
		writeExecutable(t, helper, "#!/bin/sh\nprintf '%s\\n' 'AmbientCapabilities=cap_net_bind_service cap_net_admin' 'CapabilityBoundingSet=cap_net_bind_service cap_net_admin' 'RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6'\n")
		ready, err := (coreUnitCapabilitySyncer{systemctlPath: helper, outboundMarks: true}).configured(context.Background(), service)
		if err != nil || ready {
			t.Fatalf("%s lost family filter was ignored: ready=%v err=%v", service, ready, err)
		}
	}
}

func TestManagedRuntimeServiceIdentificationRequiresProtectedFragment(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "systemctl")
	writeExecutable(t, helper, "#!/bin/sh\necho /etc/systemd/system/administrator.service\n")
	manager := &ServiceManager{kind: ServiceManagerSystemd, executable: helper}
	if _, _, managed := runtimePermissionManagedService(context.Background(), manager, "qagent-xray.service"); managed {
		t.Fatal("a managed-looking custom name was treated as project-owned")
	}
	if err := verifyManagedServiceProcessPermissions(context.Background(), manager, "qagent-xray.service"); err != nil {
		t.Fatal(err)
	}
}

func TestManagedServiceIdentityCoversValidSharedIDsOnly(t *testing.T) {
	for _, kind := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		for engine, base := range DefaultSpecsForServiceManager(kind) {
			spec, _ := sharedInstanceSpec(engine, base, "shr_0000000000000001", kind)
			if got, _, ok := managedSpecForService(spec.Service, kind); !ok || got != engine {
				t.Fatalf("%s/%s shared process verification is missing", kind, engine)
			}
		}
	}
	for _, service := range []string{"qagent-mihomo@.service", "qagent-xray@../bad.service", "qagent-xray@shr_0000000000000001", "ssh.service"} {
		if _, _, managed := managedSpecForService(service, ServiceManagerSystemd); managed {
			t.Fatalf("invalid managed identity %q", service)
		}
	}
}
