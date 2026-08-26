//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// The exact ExecStart commands the supported one-click installers write. Each
// literal is transcribed from the installer that generates it, so a drift in
// these strings is a drift in real-world import coverage.
const (
	boySingBoxBinary   = "/etc/sing-box/bin/sing-box"
	boyXrayBinary      = "/etc/xray/bin/xray"
	agentXrayBinary    = "/etc/v2ray-agent/xray/xray"
	agentSingBoxBinary = "/etc/v2ray-agent/sing-box/sing-box"
	boySingBoxArgs     = "run -c /etc/sing-box/config.json -C /etc/sing-box/conf"
	boyXrayArgs        = "run -config /etc/xray/config.json -confdir /etc/xray/conf"
	agentXrayArgs      = "run -confdir /etc/v2ray-agent/xray/conf"
	agentSingBoxArgs   = "run -c /etc/v2ray-agent/sing-box/conf/config.json"
	agentSingBoxConfig = "/etc/v2ray-agent/sing-box/conf/config.json"
	agentXrayConfDir   = "/etc/v2ray-agent/xray/conf"
	boyXrayConfDir     = "/etc/xray/conf"
	boySingBoxConfDir  = "/etc/sing-box/conf"
	boySingBoxConfig   = "/etc/sing-box/config.json"
	boyXrayConfig      = "/etc/xray/config.json"
)

// TestProductionCandidatesCoverInstallerLayouts pins the shipped candidate
// table to the binaries and configurations the supported installers create.
// Discovery only ever considers whitelisted paths, so an omission here silently
// removes an installer from import support.
func TestProductionCandidatesCoverInstallerLayouts(t *testing.T) {
	for _, expectation := range []struct {
		engine core.Engine
		binary string
		direct bool
		config string
	}{
		{engine: core.EngineSingBox, binary: boySingBoxBinary, direct: true, config: boySingBoxConfig},
		{engine: core.EngineSingBox, binary: agentSingBoxBinary, direct: true, config: agentSingBoxConfig},
		{engine: core.EngineXray, binary: boyXrayBinary, direct: true, config: boyXrayConfig},
		{engine: core.EngineXray, binary: agentXrayBinary, direct: true},
	} {
		candidates := existingDiscoveryCandidates[expectation.engine]
		if !stringInSlice(expectation.binary, candidates.executables) {
			t.Errorf("%s executables omit %s: %+v", expectation.engine, expectation.binary, candidates.executables)
		}
		if expectation.direct && !stringInSlice(expectation.binary, candidates.directExecutables) {
			t.Errorf("%s does not require a direct executable at %s: %+v", expectation.engine, expectation.binary, candidates.directExecutables)
		}
		if expectation.config != "" && !stringInSlice(expectation.config, candidates.configs) {
			t.Errorf("%s configs omit %s: %+v", expectation.engine, expectation.config, candidates.configs)
		}
	}
}

// TestParseExistingArgvMapsInstallerLayouts covers every argv shape the
// supported installers produce, plus the shapes that must keep failing closed.
func TestParseExistingArgvMapsInstallerLayouts(t *testing.T) {
	for name, testCase := range map[string]struct {
		engine          core.Engine
		binary          string
		args            string
		configPath      string
		configDirectory string
		workDirectory   string
		ok              bool
	}{
		"233boy sing-box file and confdir": {
			engine: core.EngineSingBox, binary: boySingBoxBinary, args: boySingBoxArgs,
			configPath: boySingBoxConfig, configDirectory: boySingBoxConfDir, ok: true,
		},
		"233boy xray file and confdir": {
			engine: core.EngineXray, binary: boyXrayBinary, args: boyXrayArgs,
			configPath: boyXrayConfig, configDirectory: boyXrayConfDir, ok: true,
		},
		"v2ray-agent xray confdir only": {
			engine: core.EngineXray, binary: agentXrayBinary, args: agentXrayArgs,
			configPath: "", configDirectory: agentXrayConfDir, ok: true,
		},
		"v2ray-agent sing-box single file": {
			engine: core.EngineSingBox, binary: agentSingBoxBinary, args: agentSingBoxArgs,
			configPath: agentSingBoxConfig, ok: true,
		},
		"xray short config flag with confdir": {
			engine: core.EngineXray, binary: boyXrayBinary,
			args:       "run -c " + boyXrayConfig + " -confdir " + boyXrayConfDir,
			configPath: boyXrayConfig, configDirectory: boyXrayConfDir, ok: true,
		},
		"xray plain single file": {
			engine: core.EngineXray, binary: "/usr/local/bin/xray",
			args:       "run -config /usr/local/etc/xray/config.json",
			configPath: "/usr/local/etc/xray/config.json", ok: true,
		},
		"xray relative confdir": {
			engine: core.EngineXray, binary: agentXrayBinary, args: "run -confdir conf",
		},
		"xray unknown trailing flag": {
			engine: core.EngineXray, binary: boyXrayBinary,
			args: boyXrayArgs + " -format json",
		},
		"xray repeated confdir": {
			engine: core.EngineXray, binary: boyXrayBinary,
			args: "run -confdir " + boyXrayConfDir + " -confdir " + boyXrayConfDir,
		},
		"xray sing-box directory flag is not xray syntax": {
			engine: core.EngineXray, binary: boyXrayBinary,
			args: "run -config " + boyXrayConfig + " -C " + boyXrayConfDir,
		},
		"xray missing run subcommand": {
			engine: core.EngineXray, binary: boyXrayBinary,
			args: "-confdir " + boyXrayConfDir,
		},
		"sing-box xray directory flag is not sing-box syntax": {
			engine: core.EngineSingBox, binary: boySingBoxBinary,
			args: "run -c " + boySingBoxConfig + " -confdir " + boySingBoxConfDir,
		},
		"sing-box official working directory form": {
			engine: core.EngineSingBox, binary: boySingBoxBinary,
			args:            "-D /var/lib/sing-box -C " + boySingBoxConfDir + " run",
			configPath:      filepath.Join(boySingBoxConfDir, "config.json"),
			configDirectory: boySingBoxConfDir, workDirectory: "/var/lib/sing-box", ok: true,
		},
	} {
		fields := append([]string{testCase.binary}, strings.Fields(testCase.args)...)
		configPath, configDirectory, workDirectory, ok := parseExistingArgv(testCase.engine, testCase.binary, fields)
		if ok != testCase.ok {
			t.Errorf("%s: parseExistingArgv ok = %v, want %v", name, ok, testCase.ok)
			continue
		}
		if !ok {
			continue
		}
		if configPath != testCase.configPath || configDirectory != testCase.configDirectory || workDirectory != testCase.workDirectory {
			t.Errorf("%s: parseExistingArgv = (%q, %q, %q), want (%q, %q, %q)",
				name, configPath, configDirectory, workDirectory,
				testCase.configPath, testCase.configDirectory, testCase.workDirectory)
		}
	}
}

// TestInstallerLayoutsSurviveMigrationRevalidation asserts that a mapping which
// discovery accepts is still accepted by the two checks the migration task
// re-runs immediately before it stops the existing service. A mismatch there
// would let an importable node fail at the last, most dangerous moment.
func TestInstallerLayoutsSurviveMigrationRevalidation(t *testing.T) {
	for name, layout := range map[string]struct {
		engine core.Engine
		binary string
		args   string
	}{
		"233boy sing-box":     {engine: core.EngineSingBox, binary: boySingBoxBinary, args: boySingBoxArgs},
		"233boy xray":         {engine: core.EngineXray, binary: boyXrayBinary, args: boyXrayArgs},
		"v2ray-agent xray":    {engine: core.EngineXray, binary: agentXrayBinary, args: agentXrayArgs},
		"v2ray-agent singbox": {engine: core.EngineSingBox, binary: agentSingBoxBinary, args: agentSingBoxArgs},
	} {
		argv := layout.binary + " " + layout.args
		fields := strings.Fields(argv)
		configPath, configDirectory, workDirectory, ok := parseExistingArgv(layout.engine, layout.binary, fields)
		if !ok {
			t.Fatalf("%s: discovery rejected its own installer layout", name)
		}
		existing := EngineSpec{
			Binary: layout.binary, ConfigPath: configPath, ConfigDirectory: configDirectory,
			WorkingDirectory: workDirectory, Service: string(layout.engine) + ".service",
		}
		if !supportedExistingExecStart(layout.engine, existing, argv) {
			t.Errorf("%s: supportedExistingExecStart rejected the discovered mapping", name)
		}
		// OpenRC re-derives the mapping from the supervised process argv rather
		// than from a unit file, so it has to agree with the systemd path.
		if workDirectory == "" && !openRCProcessArgvMatches(layout.engine, existing, fields) {
			t.Errorf("%s: openRCProcessArgvMatches rejected the discovered mapping", name)
		}
	}
}

// TestReadExistingConfigurationSourcesIsDirectoryAuthoritative covers the
// v2ray-agent Xray shape: no main configuration file at all, so the confdir is
// the only source of truth and every fragment must land in the snapshot.
func TestReadExistingConfigurationSourcesIsDirectoryAuthoritative(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"01_inbounds.json":  `{"inbounds":[{"tag":"in","port":443}]}`,
		"02_outbounds.json": `{"outbounds":[{"tag":"direct"}]}`,
		"notes.txt":         "ignored by the core and by this reader",
	} {
		if err := os.WriteFile(filepath.Join(configDirectory, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	spec := EngineSpec{Binary: "/unused", ConfigDirectory: configDirectory}
	content, digest, err := readExistingConfigurationSources(spec)
	if err != nil {
		t.Fatalf("read directory-authoritative sources: %v", err)
	}
	for _, fragment := range []string{`"inbounds"`, `"outbounds"`, `"direct"`} {
		if !strings.Contains(content, fragment) {
			t.Errorf("merged snapshot omitted %s: %s", fragment, content)
		}
	}
	if strings.Contains(content, "ignored by the core") {
		t.Errorf("merged snapshot absorbed a non-JSON directory entry: %s", content)
	}
	if digest == "" {
		t.Error("directory-authoritative read returned an empty source digest")
	}

	// The digest must track the directory, so a fragment change is detected
	// between the administrator's saved snapshot and the import that applies it.
	if err := os.WriteFile(filepath.Join(configDirectory, "02_outbounds.json"), []byte(`{"outbounds":[{"tag":"block"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, changedDigest, err := readExistingConfigurationSources(spec)
	if err != nil {
		t.Fatalf("re-read directory-authoritative sources: %v", err)
	}
	if changedDigest == digest {
		t.Error("source digest did not change after a confdir fragment was rewritten")
	}
}

// TestReadExistingConfigurationSourcesRejectsEmptyMapping keeps the reader fail
// closed when neither source is mapped, instead of reading a bare "" path.
func TestReadExistingConfigurationSourcesRejectsEmptyMapping(t *testing.T) {
	if _, _, err := readExistingConfigurationSources(EngineSpec{Binary: "/unused"}); err == nil {
		t.Fatal("a mapping with neither a config file nor a config directory was accepted")
	}
}

// TestOpenRCStateDirectoryChainAcceptsOnlyRootGroupWrite covers the shape stock
// OpenRC creates: /run/openrc is mode 0775 owned by root:root. That is readable
// under the narrow OpenRC policy while the general protected-path rule stays
// strict and world-writable or foreign-group directories keep failing closed.
func TestOpenRCStateDirectoryChainAcceptsOnlyRootGroupWrite(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("directory ownership assertions require root")
	}
	root := t.TempDir()
	stateRoot := filepath.Join(root, "openrc")
	options := filepath.Join(stateRoot, "options", "xray")
	if err := os.MkdirAll(options, 0o755); err != nil {
		t.Fatal(err)
	}

	// Stock OpenRC: group-writable, root:root, not world-writable.
	if err := os.Chmod(stateRoot, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := validateProtectedDirectoryChain(options); err == nil {
		t.Error("the general protected-path rule accepted a group-writable directory")
	}
	if err := validateOpenRCStateDirectoryChain(options, stateRoot); err != nil {
		t.Errorf("OpenRC state chain rejected the stock 0775 root:root layout: %v", err)
	}

	// World-writable is never tolerated, even for OpenRC state.
	if err := os.Chmod(stateRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenRCStateDirectoryChain(options, stateRoot); err == nil {
		t.Error("OpenRC state chain accepted a world-writable directory")
	}

	// Group-writable by a group other than root is a real privilege boundary.
	if err := os.Chmod(stateRoot, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(stateRoot, 0, 1); err != nil {
		t.Skipf("cannot restrict the directory group in this environment: %v", err)
	}
	if err := validateOpenRCStateDirectoryChain(options, stateRoot); err == nil {
		t.Error("OpenRC state chain accepted a directory writable by a non-root group")
	}

	// Even an otherwise acceptable root:root 0775 directory cannot opt into the
	// relaxed policy unless it is below the declared OpenRC state root.
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outside, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(outside, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenRCStateDirectoryChain(outside, stateRoot); err == nil {
		t.Error("OpenRC state policy accepted a directory outside its state root")
	}

	// The exception ends at stateRoot. A root:root group-writable ancestor is
	// outside OpenRC-owned state and must still fail the general path rule.
	if err := os.Chown(stateRoot, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateRoot, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenRCStateDirectoryChain(options, stateRoot); err == nil {
		t.Error("OpenRC state policy accepted a group-writable ancestor outside its state root")
	}
}

// TestSupervisorPIDFileNameAcceptsInstallerPidfiles pins which supervise-daemon
// pidfile paths are mappable. The name itself is not a trust anchor — the
// supervisor identity proof is — but the path must stay a direct child of the
// run directory so service metadata can never redirect the reader elsewhere.
func TestSupervisorPIDFileNameAcceptsInstallerPidfiles(t *testing.T) {
	for name, testCase := range map[string]struct {
		pidfile string
		want    string
	}{
		"233boy run-svcname form":  {pidfile: "/run/xray.pid", want: "xray.pid"},
		"233boy sing-box form":     {pidfile: "/run/sing-box.pid", want: "sing-box.pid"},
		"openrc default supervise": {pidfile: "/run/supervise-xray.pid", want: "supervise-xray.pid"},
		"var run variant":          {pidfile: "/var/run/supervise-xray.pid", want: "supervise-xray.pid"},
		"nested directory":         {pidfile: "/run/openrc/xray.pid"},
		"outside run directory":    {pidfile: "/tmp/xray.pid"},
		"traversal":                {pidfile: "/run/../etc/shadow.pid"},
		"relative":                 {pidfile: "run/xray.pid"},
		"not a pid file":           {pidfile: "/run/xray.sock"},
		"bare suffix":              {pidfile: "/run/.pid"},
		"dotfile":                  {pidfile: "/run/.hidden.pid"},
		"whitespace":               {pidfile: "/run/xray service.pid"},
		"newline":                  {pidfile: "/run/xray\n.pid"},
		"trailing slash":           {pidfile: "/run/xray.pid/"},
		"shell metacharacter":      {pidfile: "/run/xray$(id).pid"},
	} {
		got, err := supervisorPIDFileName(testCase.pidfile)
		if testCase.want == "" {
			if err == nil {
				t.Errorf("%s: supervisorPIDFileName(%q) = %q, want an error", name, testCase.pidfile, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: supervisorPIDFileName(%q) error = %v", name, testCase.pidfile, err)
			continue
		}
		if got != testCase.want {
			t.Errorf("%s: supervisorPIDFileName(%q) = %q, want %q", name, testCase.pidfile, got, testCase.want)
		}
	}
}

// TestOpenRCBoundServiceProcessAcceptsInstallerPidfileName covers the OpenRC
// layout 233boy installs on Alpine: the init script sets
// pidfile="/run/${RC_SVCNAME}.pid", so the supervisor pidfile is /run/xray.pid
// rather than OpenRC's own supervise-xray.pid default. The supervised process
// pair is otherwise exactly what the migration requires.
func TestOpenRCBoundServiceProcessAcceptsInstallerPidfileName(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	realExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	procRoot, stateRoot, supervisorRoot := t.TempDir(), t.TempDir(), t.TempDir()
	useFakeOpenRCTree(t, procRoot, stateRoot, supervisorRoot, realExecutable)

	const service = "xray"
	const supervisorPID, childPID = 100, 200
	writeOpenRCProcIdentity(t, procRoot, supervisorPID, "supervise-daemon", 1, "4000", realExecutable,
		[]string{"supervise-daemon", service, "--start", "/bin/sleep", "--", "100"})
	writeOpenRCProcIdentity(t, procRoot, childPID, "xray", supervisorPID, "5000", realExecutable,
		[]string{realExecutable, "run", "-config", boyXrayConfig, "-confdir", boyXrayConfDir})

	options := filepath.Join(stateRoot, "options", service)
	if err := os.MkdirAll(options, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"child_pid": "200\n",
		// The installer-chosen name, not supervise-<service>.pid.
		"pidfile": "/run/" + service + ".pid\n",
	} {
		if err := os.WriteFile(filepath.Join(options, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(supervisorRoot, service+".pid"), []byte("100\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		// Stock Alpine/OpenRC creates /run/openrc as root:root 0775. The bound
		// process proof must accept that directory only for the two state files.
		if err := os.Chmod(stateRoot, 0o775); err != nil {
			t.Fatal(err)
		}
	}

	identity, err := boundOpenRCServiceProcess(context.Background(), service)
	if err != nil {
		t.Fatalf("boundOpenRCServiceProcess with installer pidfile name: %v", err)
	}
	if identity.Supervisor.PID != supervisorPID || identity.Child.PID != childPID {
		t.Fatalf("identity = supervisor %d child %d; want %d/%d",
			identity.Supervisor.PID, identity.Child.PID, supervisorPID, childPID)
	}
	if identity.Child.ParentPID != supervisorPID {
		t.Fatalf("child parent = %d; want %d", identity.Child.ParentPID, supervisorPID)
	}
	if os.Geteuid() == 0 {
		// The supervisor PID file is outside /run/openrc and must retain the
		// general strict directory policy.
		if err := os.Chmod(supervisorRoot, 0o775); err != nil {
			t.Fatal(err)
		}
		if _, err := boundOpenRCServiceProcess(context.Background(), service); err == nil {
			t.Fatal("a supervisor PID below a group-writable directory was accepted")
		}
		if err := os.Chmod(supervisorRoot, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	// The relaxed name must not relax the identity proof: a supervisor whose
	// argv names a different service is still rejected. Only the command line
	// is rewritten so the already-created exe symlink stays intact.
	hijacked := strings.Join([]string{"supervise-daemon", "other-service", "--start"}, "\x00") + "\x00"
	cmdlinePath := filepath.Join(procRoot, strconv.Itoa(supervisorPID), "cmdline")
	if err := os.WriteFile(cmdlinePath, []byte(hijacked), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := boundOpenRCServiceProcess(context.Background(), service); err == nil {
		t.Fatal("a supervisor supervising another service was accepted")
	}
}
