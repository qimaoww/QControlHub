package agent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCoreStateDirectoryPreparationScope(t *testing.T) {
	for _, service := range []string{"mihomo.service", "qagent-mihomo@../../etc.service", "qagent-mihomo@invalid.service"} {
		if err := PrepareCoreStateDirectory(context.Background(), service); err == nil {
			t.Fatalf("unmanaged state preparation accepted %q", service)
		}
	}
	e := &Executor{Services: defaultSystemdServiceManager()}
	spec := DefaultSpecs()[core.EngineMihomo]
	spec.ConfigPath = "/etc/other/config.yaml"
	if err := e.prepareCoreUpdateStateDirectory(context.Background(), core.EngineMihomo, spec); err == nil {
		t.Fatal("state helper accepted a different configuration mapping")
	}
	args := coreStateDirectoryHelperArguments("/usr/local/lib/qagent/qagent", "qagent-core-state-test.service", "qagent-mihomo.service")
	for _, want := range []string{
		"--property=NoNewPrivileges=yes", "--property=ProtectSystem=strict", "--property=ProtectHome=yes",
		"--property=CapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH", "--property=RuntimeMaxSec=35s",
		"--property=RestrictAddressFamilies=AF_UNIX", "--property=ReadWritePaths=/var/lib",
	} {
		if !slices.Contains(args, want) {
			t.Fatalf("missing fixed helper restriction %s", want)
		}
	}
	if !strings.HasSuffix(strings.Join(args, " "), "-- /usr/local/lib/qagent/qagent prepare-core-state qagent-mihomo.service") {
		t.Fatalf("helper command changed: %q", args)
	}
}
