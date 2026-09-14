//go:build linux

package main

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
