package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestProductionAgentUnitAllowsOnlyRequiredCapabilities(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("../../deploy/systemd/qagent.service")
	if err != nil {
		t.Fatal(err)
	}
	unit := string(contents)
	if !strings.Contains(unit, "CapabilityBoundingSet=CAP_CHOWN CAP_NET_ADMIN") || !strings.Contains(unit, "AmbientCapabilities=CAP_CHOWN CAP_NET_ADMIN") {
		t.Fatal("production Agent unit does not retain the metadata and traffic-accounting capabilities")
	}
	for _, forbidden := range []string{"CAP_SYS_ADMIN", "CAP_SYS_PTRACE", "CAP_DAC_OVERRIDE", "CAP_NET_RAW"} {
		if strings.Contains(unit, forbidden) {
			t.Fatalf("production Agent unit grants unnecessary capability %s", forbidden)
		}
	}
	if !strings.Contains(unit, "ReadWritePaths=-/usr/local/lib/qagent/cores") {
		t.Fatal("production Agent unit does not allow updates in the private core directory")
	}
	if strings.Contains(unit, "ReadWritePaths=-/usr/local/bin\n") {
		t.Fatal("production Agent unit can modify administrator-managed /usr/local/bin programs")
	}
	if !strings.Contains(unit, "ProtectProc=invisible") {
		t.Fatal("production Agent unit must hide process details")
	}
	if strings.Contains(unit, "ProcSubset=pid") {
		t.Fatal("production Agent unit hides /proc/stat, /proc/meminfo, and /proc/net metrics")
	}
	if !strings.Contains(unit, "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK") {
		t.Fatal("production Agent unit does not allow the nftables netlink transport")
	}
}

func TestDefaultSpecsUsePrivateQAgentNamespace(t *testing.T) {
	t.Parallel()
	want := map[core.Engine]EngineSpec{
		core.EngineMihomo:          {Binary: "/usr/local/lib/qagent/cores/mihomo", ConfigPath: "/etc/qagent/mihomo/config.yaml", Service: "qagent-mihomo.service"},
		core.EngineXray:            {Binary: "/usr/local/lib/qagent/cores/xray", ConfigPath: "/etc/qagent/xray/config.json", Service: "qagent-xray.service"},
		core.EngineSingBox:         {Binary: "/usr/local/lib/qagent/cores/sing-box", ConfigPath: "/etc/qagent/sing-box/config.json", Service: "qagent-sing-box.service"},
		core.EngineShadowsocksRust: {Binary: "/usr/local/lib/qagent/cores/ssserver", ConfigPath: "/etc/qagent/shadowsocks-rust/config.json", Service: "qagent-shadowsocks-rust.service"},
	}
	for engine, expected := range want {
		actual := DefaultSpecs()[engine]
		if actual != expected {
			t.Errorf("DefaultSpecs()[%s] = %+v, want %+v", engine, actual, expected)
		}
		unitPath := filepath.Join("../../deploy/systemd", expected.Service)
		contents, err := os.ReadFile(unitPath)
		if err != nil {
			t.Errorf("read %s: %v", unitPath, err)
			continue
		}
		unit := string(contents)
		if err := validateManagedUnitFragment(contents, engine, expected); err != nil {
			t.Errorf("%s does not match the runtime-owned unit contract: %v", expected.Service, err)
		}
		for _, required := range []string{
			"ConditionFileIsExecutable=" + expected.Binary,
			"ConditionPathExists=" + expected.ConfigPath,
			"ExecStart=" + expected.Binary,
			"CapabilityBoundingSet=CAP_NET_BIND_SERVICE",
			"AmbientCapabilities=CAP_NET_BIND_SERVICE",
			"LogNamespace=qagent-cores",
			"StandardOutput=journal",
			"StandardError=journal",
		} {
			if !strings.Contains(unit, required) {
				t.Errorf("%s is missing %q", expected.Service, required)
			}
		}
		for _, forbidden := range []string{"CAP_NET_ADMIN", "CAP_NET_RAW", "CAP_SYS_ADMIN", "CAP_DAC_OVERRIDE"} {
			if strings.Contains(unit, forbidden) {
				t.Errorf("%s grants unnecessary capability %s", expected.Service, forbidden)
			}
		}
	}
	journalConfig, err := os.ReadFile("../../deploy/systemd/qagent-core-journal.conf")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Storage=volatile", "RuntimeMaxUse=16M", "MaxRetentionSec=15min"} {
		if !strings.Contains(string(journalConfig), required) {
			t.Errorf("core journal configuration is missing %q", required)
		}
	}
}

func TestOpenRCSpecsAndServicesUsePrivateQAgentNamespace(t *testing.T) {
	t.Parallel()
	for engine, spec := range DefaultSpecsForServiceManager(ServiceManagerOpenRC) {
		if strings.HasSuffix(spec.Service, ".service") {
			t.Errorf("OpenRC service for %s retains systemd suffix: %s", engine, spec.Service)
		}
		contents, err := os.ReadFile(filepath.Join("../../deploy/openrc", spec.Service))
		if err != nil {
			t.Errorf("read OpenRC service %s: %v", spec.Service, err)
			continue
		}
		script := string(contents)
		for _, required := range []string{"#!/sbin/openrc-run", "# QControlHub managed OpenRC service:", "supervisor=\"supervise-daemon\"", "capabilities=\"^cap_net_bind_service\"", spec.Binary} {
			if !strings.Contains(script, required) {
				t.Errorf("OpenRC service %s is missing %q", spec.Service, required)
			}
		}
	}
	agentService, err := os.ReadFile("../../deploy/openrc/qagent")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"command=\"/usr/local/lib/qagent/qagent\"", "command_user=\"root:root\"", "respawn_delay=5", "capabilities=\"^cap_chown,^cap_net_admin\"", "no_new_privs=true"} {
		if !strings.Contains(string(agentService), required) {
			t.Errorf("OpenRC Agent service is missing %q", required)
		}
	}
}

func TestCoreBootstrapDoesNotTouchLegacyInstallations(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("../../deploy/bootstrap-core-services.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	for _, required := range []string{"/usr/local/lib/qagent/cores", "/etc/systemd/system/$managed_service", "install_managed_unit", "preserved non-QAgent unit", "install_managed_openrc_service", "rc-update add"} {
		if !strings.Contains(script, required) {
			t.Errorf("core bootstrap is missing private namespace %q", required)
		}
	}
	for _, forbidden := range []string{"/usr/local/bin/mihomo", "/usr/local/bin/xray", "managed by QControlHub", "legacy_service"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("core bootstrap unexpectedly touches legacy installation %q", forbidden)
		}
	}
}

func TestOneClickInstallerMapsOnlyValidatedExistingCorePaths(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("../../deploy/remote/install-agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := os.ReadFile("../../deploy/existing-core-mapping.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents) + string(mapping)
	for _, required := range []string{
		"discover_existing_xray", "discover_existing_singbox", "systemctl is-active --quiet",
		"existing-core-mapping.sh", "QCH_EXISTING_XRAY_CONFIG", "QCH_EXISTING_SING_BOX_CONFIG",
		"rc-service \"$service\" status", "openrc_supervised_child_pid",
		"QCH_SERVICE_MANAGER", "apk add --no-cache", "deploy/openrc",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("one-click installer is missing inherited-core guard %q", required)
		}
	}
	for _, forbidden := range []string{"systemctl stop xray.service", "systemctl stop sing-box.service", "QCH_INHERIT_CONFIGS", "validate-inherited", "/proc/[0-9]*"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("one-click installer stops an existing service via %q", forbidden)
		}
	}
}

func TestOneClickInstallerDefersManagedCoreServicesUntilPanelInstall(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("../../deploy/remote/install-agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	for _, required := range []string{
		`bootstrap-core-services.sh" --prepare-agent`,
		`core_asset_root=/usr/local/share/qcontrolhub/core-install`,
		`stage_core_asset "$repository_dir/deploy/$service_manager/$service_asset"`,
		`/usr/local/lib/qagent/cores`,
		`existing $label service was left unchanged because it could not be mapped safely`,
		`only this core's remote tasks will remain disabled`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("one-click installer is missing deferred-core contract %q", required)
		}
	}
	if strings.Contains(script, `QCH_SKIP_CORE_SERVICES="$mapped_engines" sh "$repository_dir/deploy/bootstrap-core-services.sh"`) {
		t.Fatal("one-click installer still installs all managed core services during Agent deployment")
	}
	if strings.Contains(script, `unsafe $label service state; installation stopped`) {
		t.Fatal("one-click installer still blocks Agent enrollment for an untouched, unmappable existing core")
	}

	bootstrap, err := os.ReadFile("../../deploy/bootstrap-core-services.sh")
	if err != nil {
		t.Fatal(err)
	}
	bootstrapScript := string(bootstrap)
	for _, required := range []string{
		`--prepare-agent) selected_engines=""`,
		`mihomo|xray|sing-box|shadowsocks-rust) selected_engines=$requested_engine`,
		`for engine in $selected_engines`,
	} {
		if !strings.Contains(bootstrapScript, required) {
			t.Errorf("core bootstrap is missing selective-install contract %q", required)
		}
	}
}

func TestPersistentCoreLogOutputsAreRejected(t *testing.T) {
	t.Parallel()
	fixtures := []struct {
		engine  core.Engine
		content string
	}{
		{core.EngineXray, `{"log":{"loglevel":"info","access":"/var/log/xray.log"}}`},
		{core.EngineSingBox, `{"log":{"level":"info","output":"/var/log/sing-box.log"}}`},
		{core.EngineSingBox, `{"log":{"level":"info","output":"none"}}`},
	}
	for _, fixture := range fixtures {
		if err := validateNoPersistentCoreLogs(fixture.engine, fixture.content); err == nil {
			t.Errorf("%s persistent log output was accepted", fixture.engine)
		}
	}
	if err := validateNoPersistentCoreLogs(core.EngineXray, `{"log":{"loglevel":"info"}}`); err != nil {
		t.Fatalf("stdout Xray logging was rejected: %v", err)
	}
	if err := validateNoPersistentCoreLogs(core.EngineXray, `{"log":{"loglevel":"info","access":"none"}}`); err != nil {
		t.Fatalf("disabled Xray file logging was rejected: %v", err)
	}
	if err := validateNoPersistentCoreLogs(core.EngineXray, `{"log":{"access":" NONE "}}`); err == nil {
		t.Fatal("Xray file destination disguised as a none variant was accepted")
	}
}

func TestNormalizeImportedXrayLogDestinations(t *testing.T) {
	t.Parallel()
	content := `{"log":{"access":"/var/log/xray/access.log","error":" NONE ","loglevel":"info","dnsLog":true,"maskAddress":"half"},"inbounds":[],"outbounds":[]}`
	normalized, err := normalizeImportedXrayLogDestinations(content)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(normalized), &root); err != nil {
		t.Fatal(err)
	}
	logging := root["log"].(map[string]any)
	if logging["access"] != "" || logging["error"] != "" || logging["loglevel"] != "info" ||
		logging["dnsLog"] != true || logging["maskAddress"] != "half" {
		t.Fatalf("normalized Xray log policy = %+v", logging)
	}
	unchanged := `{"log":{"access":"none","error":"","loglevel":"warning"},"inbounds":[]}`
	if got, err := normalizeImportedXrayLogDestinations(unchanged); err != nil || got != unchanged {
		t.Fatalf("non-persistent Xray log policy changed: %q, %v", got, err)
	}
	if _, err := normalizeImportedXrayLogDestinations(`{"log":{"access":42}}`); err == nil {
		t.Fatal("non-string Xray log destination was accepted")
	}
}

func TestImportedSingBoxPreservesManagedFileLogOutput(t *testing.T) {
	root := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = root
	t.Cleanup(func() { importedSingBoxLogRoot = previous })
	executor := &Executor{}
	spec := EngineSpec{Binary: "/usr/bin/true", ConfigPath: filepath.Join(t.TempDir(), "config.json")}
	content := `{"log":{"output":"runtime.log"},"inbounds":[],"outbounds":[]}`
	if _, err := executor.validateImportedSnapshot(context.Background(), core.EngineSingBox, spec, content); err != nil {
		t.Fatalf("safe imported log.output was rejected: %v", err)
	}
	unsafe := `{"log":{"output":"/etc/shadow"},"inbounds":[],"outbounds":[]}`
	if _, err := executor.validateImportedSnapshot(context.Background(), core.EngineSingBox, spec, unsafe); err == nil {
		t.Fatal("imported log.output outside the managed boundary was accepted")
	}
}

func TestUnsupportedExistingServiceBlocksEveryCoreAction(t *testing.T) {
	t.Parallel()
	reason := "multiple active sing-box services were detected"
	executor := &Executor{
		Specs: map[core.Engine]EngineSpec{
			core.EngineSingBox: {Binary: "/unused/sing-box", ConfigPath: "/unused/config.json", Service: "qagent-sing-box.service"},
		},
		ExistingDiscoveryIssues: map[core.Engine]string{core.EngineSingBox: reason},
	}
	for _, action := range []core.Action{
		core.ActionValidate, core.ActionDeploy, core.ActionStart, core.ActionStop,
		core.ActionRestart, core.ActionStatus, core.ActionInstall, core.ActionReadConfig,
		core.ActionImportExisting,
	} {
		t.Run(string(action), func(t *testing.T) {
			_, err := executor.Execute(context.Background(), core.Task{
				Action: action, Engine: core.EngineSingBox, ConfigContent: `{"inbounds":[]}`,
			})
			if err == nil || !strings.Contains(err.Error(), "core tasks are disabled") || !strings.Contains(err.Error(), reason) {
				t.Fatalf("Execute(%s) error = %v", action, err)
			}
		})
	}
}

func TestServiceVerificationRejectsTransientActiveState(t *testing.T) {
	t.Parallel()
	statuses := []string{"active", "active", "failed"}
	index := 0
	status, err := waitForServiceState(context.Background(), "active", 20*time.Millisecond, time.Millisecond, func(context.Context) (string, error) {
		if index < len(statuses)-1 {
			value := statuses[index]
			index++
			return value, nil
		}
		return statuses[len(statuses)-1], nil
	})
	if err != nil || status != "failed" {
		t.Fatalf("transient active verification = %q, %v; want failed", status, err)
	}
}

func TestServiceVerificationRequiresStableActiveState(t *testing.T) {
	t.Parallel()
	status, err := waitForServiceState(context.Background(), "active", 5*time.Millisecond, time.Millisecond, func(context.Context) (string, error) {
		return "active", nil
	})
	if err != nil || status != "active" {
		t.Fatalf("stable active verification = %q, %v", status, err)
	}
}

func TestExecutorRejectsUnsafeTasksAndPaths(t *testing.T) {
	t.Parallel()
	executor := &Executor{
		Specs: map[core.Engine]EngineSpec{
			core.EngineMihomo: {
				Binary:     "relative-binary",
				ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
				Service:    "qagent-mihomo.service",
			},
		},
	}
	rejected := []core.Task{
		{Action: core.Action("restart; rm -rf /"), Engine: core.EngineMihomo},
		{Action: core.ActionStatus, Engine: core.Engine("mihomo;evil")},
		{Action: core.ActionStatus, Engine: core.EngineXray},
		{Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: "https://evil.example/core"},
		{Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionStable, CoreSource: string(core.CoreSourceMirror)},
		{Action: core.ActionInstall, Engine: core.EngineXray, CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror)},
		{Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionDevelopment, CoreSource: "private"},
	}
	for _, task := range rejected {
		if _, err := executor.Execute(context.Background(), task); err == nil {
			t.Fatalf("Execute() accepted non-whitelisted task: action=%q engine=%q", task.Action, task.Engine)
		}
	}
}

func TestValidateNoRelativeSingBoxResourcesContract(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		contents string
		wantErr  bool
	}{
		"relative clash external ui": {
			contents: `{"experimental":{"clash_api":{"external_ui":"dashboard"}}}`,
			wantErr:  true,
		},
		"absolute clash external ui": {
			contents: `{"experimental":{"clash_api":{"external_ui":"/srv/sing-box/dashboard"}}}`,
			wantErr:  false,
		},
		"relative acme data directory": {
			contents: `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","acme":{"data_directory":"acme-data"}}}]}`,
			wantErr:  true,
		},
		"absolute acme data directory": {
			contents: `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","acme":{"data_directory":"/var/lib/acme"}}}]}`,
			wantErr:  false,
		},
		"relative client certificate path array": {
			contents: `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","client_certificate_path":["client.pem","ca.pem"]}}]}`,
			wantErr:  true,
		},
		"absolute client certificate path array": {
			contents: `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","client_certificate_path":["/etc/client.pem","/etc/ca.pem"]}}]}`,
			wantErr:  false,
		},
		"relative inbound certificate path": {
			contents: `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"cert.pem","key_path":"/etc/key.pem"}}]}`,
			wantErr:  true,
		},
		"relative outbound ech config path": {
			contents: `{"outbounds":[{"type":"vless","server":"example.com","server_port":443,"uuid":"abc","tls":{"enabled":true,"ech":{"config_path":"ech.json"}}}]}`,
			wantErr:  true,
		},
		"relative local rule set path": {
			contents: `{"route":{"rule_set":[{"type":"local","tag":"geo","format":"binary","path":"ruleset.srs"}]}}`,
			wantErr:  true,
		},
		"absolute local rule set path": {
			contents: `{"route":{"rule_set":[{"type":"local","tag":"geo","format":"binary","path":"/etc/sing-box/ruleset.srs"}]}}`,
			wantErr:  false,
		},
		"relative ssh private key": {
			contents: `{"outbounds":[{"type":"ssh","server":"example.com","server_port":22,"user":"root","private_key_path":"id_ed25519"}]}`,
			wantErr:  true,
		},
		"relative tor executable": {
			contents: `{"outbounds":[{"type":"tor","server":"127.0.0.1","server_port":9050,"executable_path":"./tor"}]}`,
			wantErr:  true,
		},
		"relative dialer protect path": {
			contents: `{"outbounds":[{"type":"direct","protect_path":"protect.sock"}]}`,
			wantErr:  true,
		},
		"relative geoip path": {
			contents: `{"route":{"geoip":{"path":"geoip.db"}}}`,
			wantErr:  true,
		},
		"relative cache file path": {
			contents: `{"experimental":{"cache_file":{"enabled":true,"path":"cache.db"}}}`,
			wantErr:  true,
		},
		"log output stdout accepted": {
			contents: `{"log":{"output":"stdout"}}`,
			wantErr:  false,
		},
		"log output stderr accepted": {
			contents: `{"log":{"output":"stderr"}}`,
			wantErr:  false,
		},
		"log output relative deferred to import boundary": {
			contents: `{"log":{"output":"relative.log"}}`,
			wantErr:  false,
		},
		"log output absolute accepted": {
			contents: `{"log":{"output":"/var/log/sing-box.log"}}`,
			wantErr:  false,
		},
		"http transport url path not treated as file": {
			contents: `{"outbounds":[{"type":"http","server":"example.com","server_port":8080,"path":"/proxy"}]}`,
			wantErr:  false,
		},
		"relative certificate directory path": {
			contents: `{"certificate":{"certificate_directory_path":["certs"]}}`,
			wantErr:  true,
		},
		"absolute certificate directory path": {
			contents: `{"certificate":{"certificate_directory_path":["/etc/sing-box/certs"]}}`,
			wantErr:  false,
		},
		"relative certificate path list": {
			contents: `{"certificate":{"certificate_path":["cert.pem"]}}`,
			wantErr:  true,
		},
		"absolute certificate path list": {
			contents: `{"certificate":{"certificate_path":["/etc/sing-box/cert.pem"]}}`,
			wantErr:  false,
		},
		"relative tailscale state directory": {
			contents: `{"endpoints":[{"type":"tailscale","tag":"ts","state_directory":"state"}]}`,
			wantErr:  true,
		},
		"absolute tailscale state directory": {
			contents: `{"endpoints":[{"type":"tailscale","tag":"ts","state_directory":"/var/lib/tailscale"}]}`,
			wantErr:  false,
		},
		"relative ccm credential path": {
			contents: `{"services":[{"type":"ccm","tag":"ccm","credential_path":"creds.json"}]}`,
			wantErr:  true,
		},
		"absolute ccm credential path": {
			contents: `{"services":[{"type":"ccm","tag":"ccm","credential_path":"/etc/sing-box/creds.json"}]}`,
			wantErr:  false,
		},
		"relative ocm usages path": {
			contents: `{"services":[{"type":"ocm","tag":"ocm","usages_path":"usages.json"}]}`,
			wantErr:  true,
		},
		"absolute ocm usages path": {
			contents: `{"services":[{"type":"ocm","tag":"ocm","usages_path":"/etc/sing-box/usages.json"}]}`,
			wantErr:  false,
		},
		"relative derp config path": {
			contents: `{"services":[{"type":"derp","tag":"derp","config_path":"derp.json"}]}`,
			wantErr:  true,
		},
		"absolute derp config path": {
			contents: `{"services":[{"type":"derp","tag":"derp","config_path":"/etc/sing-box/derp.json"}]}`,
			wantErr:  false,
		},
		"relative derp mesh psk file": {
			contents: `{"services":[{"type":"derp","tag":"derp","mesh_psk_file":"psk.txt"}]}`,
			wantErr:  true,
		},
		"absolute derp mesh psk file": {
			contents: `{"services":[{"type":"derp","tag":"derp","mesh_psk_file":"/etc/sing-box/psk.txt"}]}`,
			wantErr:  false,
		},
		"relative ssmapi cache path": {
			contents: `{"services":[{"type":"ssm-api","tag":"ssm","cache_path":"cache.db"}]}`,
			wantErr:  true,
		},
		"absolute ssmapi cache path": {
			contents: `{"services":[{"type":"ssm-api","tag":"ssm","cache_path":"/var/lib/sing-box/cache.db"}]}`,
			wantErr:  false,
		},
		"relative hysteria2 masquerade file directory": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":{"type":"file","directory":"webroot"}}]}`,
			wantErr:  true,
		},
		"absolute hysteria2 masquerade file directory": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":{"type":"file","directory":"/var/www/sing-box"}}]}`,
			wantErr:  false,
		},
		"relative hysteria2 masquerade file url": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":"file:webroot"}]}`,
			wantErr:  true,
		},
		"absolute hysteria2 masquerade file url": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":"file:///var/www/sing-box"}]}`,
			wantErr:  false,
		},
		"http hysteria2 masquerade url accepted": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":"https://example.com/ui"}]}`,
			wantErr:  false,
		},
		"unknown masquerade scheme left to core": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":"ftp://example.com/ui"}]}`,
			wantErr:  false,
		},
		"future resource named path rejected by fallback": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"some_future_path":"data.db"}]}`,
			wantErr:  true,
		},
		"future resource named file rejected by fallback": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"some_future_file":"data.db"}]}`,
			wantErr:  true,
		},
		"rule process path matching not treated as resource": {
			contents: `{"route":{"rules":[{"type":"default","process_path":"sbin/nginx","action":"direct"}]}}`,
			wantErr:  false,
		},
		"http transport relative path not treated as resource": {
			contents: `{"outbounds":[{"type":"http","server":"example.com","server_port":8080,"path":"proxy"}]}`,
			wantErr:  false,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateNoRelativeSingBoxResources(tt.contents)
			if tt.wantErr && err == nil {
				t.Fatalf("validateNoRelativeSingBoxResources() accepted a relative resource:\n%s", tt.contents)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateNoRelativeSingBoxResources() rejected an absolute resource: %v\n%s", err, tt.contents)
			}
		})
	}
}
