//go:build linux

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/agent"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

var version = "dev"

func main() {
	hostname, _ := os.Hostname()
	specs := agent.DefaultSpecs()
	specs[core.EngineMihomo] = overrideSpec(specs[core.EngineMihomo], "MIHOMO")
	specs[core.EngineXray] = overrideSpec(specs[core.EngineXray], "XRAY")
	specs[core.EngineSingBox] = overrideSpec(specs[core.EngineSingBox], "SING_BOX")
	specs[core.EngineShadowsocksRust] = overrideSpec(specs[core.EngineShadowsocksRust], "SS_RUST")
	capabilities := parseEngines(env("QCH_AGENT_ENGINES", "mihomo,xray,sing-box,ss-rust"))
	enabledSpecs := make(map[core.Engine]agent.EngineSpec, len(capabilities))
	for _, engine := range capabilities {
		enabledSpecs[engine] = specs[engine]
	}

	executor := &agent.Executor{Specs: enabledSpecs}
	client, err := agent.NewClient(agent.ClientConfig{
		ServerURL:         env("QCH_SERVER_URL", "http://localhost:8080"),
		EnrollmentToken:   os.Getenv("QCH_ENROLLMENT_TOKEN"),
		StatePath:         env("QCH_AGENT_STATE", "./data/agent-state.json"),
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
		if errors.Is(err, agent.ErrIdentityRejected) {
			slog.Error("agent identity is no longer valid; remove the state file and enroll again", "error", err)
			return
		}
		slog.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}

func overrideSpec(spec agent.EngineSpec, prefix string) agent.EngineSpec {
	spec.Binary = env("QCH_"+prefix+"_BINARY", spec.Binary)
	spec.ConfigPath = env("QCH_"+prefix+"_CONFIG", spec.ConfigPath)
	spec.Service = env("QCH_"+prefix+"_SERVICE", spec.Service)
	return spec
}

func parseEngines(value string) []core.Engine {
	result := make([]core.Engine, 0, 4)
	seen := make(map[core.Engine]struct{})
	for _, item := range strings.Split(value, ",") {
		engine, err := core.ParseEngine(item)
		if err != nil {
			slog.Error("invalid QCH_AGENT_ENGINES", "value", item, "error", err)
			os.Exit(1)
		}
		if _, exists := seen[engine]; !exists {
			result = append(result, engine)
			seen[engine] = struct{}{}
		}
	}
	return result
}

func parseLabels(value string) map[string]string {
	result := make(map[string]string)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, label, found := strings.Cut(item, "=")
		if !found {
			result[key] = "true"
		} else {
			result[strings.TrimSpace(key)] = strings.TrimSpace(label)
		}
	}
	return result
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		slog.Error("invalid boolean environment variable", "key", key, "value", value)
		os.Exit(1)
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		slog.Error("invalid duration environment variable", "key", key, "value", value)
		os.Exit(1)
	}
	return parsed
}

// envList parses a comma-separated environment variable into a list, dropping
// blank entries. An empty variable yields nil so no default probe endpoint is
// ever applied on the agent's behalf.
func envList(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
