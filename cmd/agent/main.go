//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/agent"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

var version = "dev"

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "system-bbr" {
		if (len(os.Args) != 3 && len(os.Args) != 4) || !core.Action(os.Args[2]).SystemBBR() {
			fmt.Fprintln(os.Stderr, "usage: qagent system-bbr enable-bbr|disable-bbr|configure-tcp [settings-json]")
			os.Exit(1)
		}
		var settings core.TCPSettings
		if len(os.Args) == 4 {
			if len(os.Args[3]) > 4096 || json.Unmarshal([]byte(os.Args[3]), &settings) != nil {
				fmt.Fprintln(os.Stderr, "invalid TCP settings JSON")
				os.Exit(1)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		output, err := agent.RunSystemBBR(ctx, core.Action(os.Args[2]), settings)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(output)
		return
	}
	// Handle the side-effect-limited candidate check before loading credentials,
	// discovering cores, or starting the Agent's normal runtime.
	if len(os.Args) == 4 && os.Args[1] == "upgrade-preflight" {
		report, err := agent.PreflightAgentUpgrade(version, os.Args[2], os.Args[3])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			os.Exit(1)
		}
		return
	}
	hostname, _ := os.Hostname()
	serviceManagerName := env("QCH_SERVICE_MANAGER", agent.ServiceManagerSystemd)
	serviceManager, err := agent.NewServiceManager(serviceManagerName)
	if err != nil {
		slog.Error("invalid service manager", "error", err)
		os.Exit(1)
	}
	specs := agent.DefaultSpecsForServiceManager(serviceManager.Kind())
	specs[core.EngineMihomo] = overrideSpec(specs[core.EngineMihomo], "MIHOMO")
	specs[core.EngineXray] = overrideSpec(specs[core.EngineXray], "XRAY")
	specs[core.EngineSingBox] = overrideSpec(specs[core.EngineSingBox], "SING_BOX")
	specs[core.EngineShadowsocksRust] = overrideSpec(specs[core.EngineShadowsocksRust], "SS_RUST")
	if len(os.Args) > 1 {
		if err := runUtilityCommand(specs, os.Args[1:], serviceManager); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	capabilities := parseEngines(env("QCH_AGENT_ENGINES", "mihomo,xray,sing-box,ss-rust"))
	enabledSpecs := make(map[core.Engine]agent.EngineSpec, len(capabilities))
	for _, engine := range capabilities {
		enabledSpecs[engine] = specs[engine]
	}
	statePath := env("QCH_AGENT_STATE", "./data/agent-state.json")
	manualExistingSpecs := make(map[core.Engine]agent.EngineSpec)
	if spec, ok := existingSpec("XRAY"); ok {
		manualExistingSpecs[core.EngineXray] = spec
	}
	if spec, ok := existingSpec("SING_BOX"); ok {
		manualExistingSpecs[core.EngineSingBox] = spec
	}
	if spec, ok := existingSpec("SS_RUST"); ok {
		manualExistingSpecs[core.EngineShadowsocksRust] = spec
	}
	discoveryContext, discoveryCancel := context.WithTimeout(context.Background(), 30*time.Second)
	existingSpecs, discoveryIssues, err := agent.RefreshExistingCoreDiscovery(
		discoveryContext,
		statePath+".existing-cores",
		statePath+".core-migration",
		enabledSpecs,
		manualExistingSpecs,
		serviceManager,
	)
	discoveryCancel()
	if err != nil {
		slog.Error("existing core discovery failed", "error", err)
		os.Exit(1)
	}

	executor := &agent.Executor{
		Specs: enabledSpecs, ExistingSpecs: existingSpecs,
		ExistingDiscoveryIssues: discoveryIssues,
		MigrationMarkerPrefix:   statePath + ".core-migration",
		Services:                serviceManager,
	}
	client, err := agent.NewClient(agent.ClientConfig{
		ServerURL:         env("QCH_SERVER_URL", "http://localhost:8080"),
		EnrollmentToken:   os.Getenv("QCH_ENROLLMENT_TOKEN"),
		StatePath:         statePath,
		Name:              env("QCH_AGENT_NAME", hostname),
		Version:           version,
		Labels:            parseLabels(os.Getenv("QCH_AGENT_LABELS")),
		Capabilities:      capabilities,
		HeartbeatEvery:    envDuration("QCH_HEARTBEAT_INTERVAL", 15*time.Second),
		MetricsEvery:      envDuration("QCH_METRICS_INTERVAL", time.Second),
		AllowHTTP:         envBool("QCH_ALLOW_HTTP", false),
		AllowInsecureLive: envBool("QCH_ALLOW_INSECURE_LIVE", false),
		TLSCAFile:         strings.TrimSpace(os.Getenv("QCH_TLS_CA_FILE")),
		// Dual-stack egress probing is opt-in because it queries operator
		// supplied echo endpoints and adds an outbound dependency. Keep it off
		// unless a control-plane-owned echo service is configured.
		PublicIPProbe:              envBool("QCH_PUBLIC_IP_PROBE", false),
		PublicIPProbeEvery:         envDuration("QCH_PUBLIC_IP_PROBE_INTERVAL", 5*time.Minute),
		PublicIPProbeIPv4Endpoints: envList("QCH_PUBLIC_IP_PROBE_IPV4_ENDPOINTS"),
		PublicIPProbeIPv6Endpoints: envList("QCH_PUBLIC_IP_PROBE_IPV6_ENDPOINTS"),
	}, executor)
	if err != nil {
		slog.Error("invalid agent configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("QControlHub agent starting", "version", version)
	if err := client.Run(ctx); err != nil {
		// Run retries control-plane identity rejection internally instead of
		// returning it, so any error here is fatal. Exit non-zero so the service
		// manager applies Restart=on-failure rather than leaving the node idle.
		slog.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}
