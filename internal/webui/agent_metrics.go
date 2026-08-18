package webui

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type agentMetricsResponse struct {
	Agents []agentMetricsSnapshot `json:"agents"`
}

type agentMetricsSnapshot struct {
	ID               string                            `json:"id"`
	Status           string                            `json:"status"`
	LastSeenLabel    string                            `json:"last_seen_label"`
	Version          string                            `json:"version"`
	Runtime          map[core.Engine]core.RuntimeState `json:"runtime"`
	Available        bool                              `json:"available"`
	CollectedAgo     string                            `json:"collected_ago"`
	CPUAvailable     bool                              `json:"cpu_available"`
	CPUPercent       float64                           `json:"cpu_percent"`
	CPUText          string                            `json:"cpu_text"`
	MemoryAvailable  bool                              `json:"memory_available"`
	MemoryPercent    float64                           `json:"memory_percent"`
	MemoryText       string                            `json:"memory_text"`
	DiskAvailable    bool                              `json:"disk_available"`
	DiskPercent      float64                           `json:"disk_percent"`
	DiskText         string                            `json:"disk_text"`
	NetworkAvailable bool                              `json:"network_available"`
	DownloadRate     string                            `json:"download_rate"`
	UploadRate       string                            `json:"upload_rate"`
	DownloadTotal    string                            `json:"download_total"`
	UploadTotal      string                            `json:"upload_total"`
}

func (s *Server) agentMetrics(w http.ResponseWriter, request *http.Request) {
	agents, err := s.store.ListAgents(request.Context())
	if err != nil {
		s.renderDatabaseError(w, err)
		return
	}
	response := agentMetricsResponse{Agents: make([]agentMetricsSnapshot, 0, len(agents))}
	for _, agent := range agents {
		response.Agents = append(response.Agents, metricsSnapshot(agent))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		return
	}
}

func metricsSnapshot(agent core.Agent) agentMetricsSnapshot {
	metrics := agent.Metrics
	result := agentMetricsSnapshot{
		ID: agent.ID, Status: agent.Status, LastSeenLabel: heartbeatLabel(agent.LastSeen), Version: agent.Version, Runtime: shortenedRuntimeVersions(agent.Runtime),
		Available: hasMetrics(metrics), CPUAvailable: metrics.CPUAvailable,
		MemoryAvailable: metrics.MemoryAvailable, DiskAvailable: metrics.DiskAvailable,
		NetworkAvailable: metrics.NetworkAvailable,
	}
	if result.Available {
		result.CollectedAgo = timeAgo(metrics.CollectedAt)
	}
	if metrics.CPUAvailable {
		result.CPUPercent = clampPercent(metrics.CPUPercent)
		result.CPUText = formatPercent(metrics.CPUPercent)
	}
	if metrics.MemoryAvailable {
		result.MemoryPercent = usagePercent(metrics.MemoryUsedBytes, metrics.MemoryTotalBytes)
		result.MemoryText = formatDataSize(metrics.MemoryUsedBytes) + " / " + formatDataSize(metrics.MemoryTotalBytes)
	}
	if metrics.DiskAvailable {
		result.DiskPercent = usagePercent(metrics.DiskUsedBytes, metrics.DiskTotalBytes)
		result.DiskText = formatDataSize(metrics.DiskUsedBytes) + " / " + formatDataSize(metrics.DiskTotalBytes)
	}
	if metrics.NetworkAvailable {
		result.DownloadRate = formatDataRate(metrics.NetworkRXBPS)
		result.UploadRate = formatDataRate(metrics.NetworkTXBPS)
		result.DownloadTotal = formatDataSize(metrics.NetworkRXBytes)
		result.UploadTotal = formatDataSize(metrics.NetworkTXBytes)
	}
	return result
}

func hasMetrics(metrics core.HostMetrics) bool {
	return !metrics.CollectedAt.IsZero() && (metrics.CPUAvailable || metrics.MemoryAvailable || metrics.DiskAvailable || metrics.NetworkAvailable)
}

// shortenedRuntimeVersions rewrites each installed engine's version into the same
// concise "Engine 内核 version" label the SSR templates use, so the realtime
// polling JS overwrites the DOM with the short text (never the raw banner).
func shortenedRuntimeVersions(original map[core.Engine]core.RuntimeState) map[core.Engine]core.RuntimeState {
	out := make(map[core.Engine]core.RuntimeState, len(original))
	for engine, state := range original {
		if state.Installed {
			state.Version = displayEngineVersion(engine, state.Version)
		}
		out[engine] = state
	}
	return out
}

func usagePercent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return clampPercent(float64(used) * 100 / float64(total))
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func formatPercent(value float64) string {
	return fmt.Sprintf("%.1f%%", clampPercent(value))
}

func formatDataRate(value uint64) string {
	return formatDataSize(value) + "/s"
}

func formatDataSize(value uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	number := float64(value)
	unit := 0
	for number >= 1024 && unit < len(units)-1 {
		number /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", number, units[unit])
}

func (s *Server) metricsScript(w http.ResponseWriter, r *http.Request) {
	s.serveAsset(w, r, "text/javascript; charset=utf-8", s.metricsJSAsset)
}
