package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

const komariAPIKeyMask = "••••••••"

func panelSettingsResponse(settings core.PanelSettings) core.PanelSettings {
	if settings.KomariAPIKey != "" {
		settings.KomariAPIKey = komariAPIKeyMask
	}
	return settings
}

func (s *Server) getSettings(w http.ResponseWriter, request *http.Request) {
	settings, err := s.store.PanelSettings(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, panelSettingsResponse(settings))
}

func (s *Server) putSettings(w http.ResponseWriter, request *http.Request) {
	previous, previousErr := s.store.PanelSettings(request.Context())
	if previousErr != nil {
		writeInternalError(w, previousErr)
		return
	}
	var input struct {
		core.PanelSettings
		KomariURL         *string `json:"komari_url"`
		KomariAPIKey      *string `json:"komari_api_key"`
		ClearKomariAPIKey bool    `json:"clear_komari_api_key"`
	}
	if err := decodeJSON(w, request, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	settings := input.PanelSettings
	// Pointer fields distinguish a legacy client that does not know about
	// Komari from an explicit empty value used to clear the URL.
	if input.KomariURL == nil {
		settings.KomariURL = previous.KomariURL
	} else {
		settings.KomariURL = *input.KomariURL
	}
	preserveKomariAPIKey := input.KomariAPIKey == nil
	if !preserveKomariAPIKey && !input.ClearKomariAPIKey {
		preserveKomariAPIKey = *input.KomariAPIKey == "" || *input.KomariAPIKey == komariAPIKeyMask
	}
	if preserveKomariAPIKey {
		settings.KomariAPIKey = previous.KomariAPIKey
	} else {
		settings.KomariAPIKey = *input.KomariAPIKey
	}
	// The browser never receives the actual key. An empty or masked value means
	// keep the currently saved key; an explicit clear flag removes it.
	expectedRevision := settings.Revision
	var saved core.PanelSettings
	var err error
	if expectedRevision > 0 {
		saved, err = s.store.SavePanelSettingsRevision(request.Context(), settings, expectedRevision)
	} else {
		// Cached pre-v37 frontends do not send revision or any newly introduced
		// policy fields. The store preserves those fields as one compatibility
		// unit while the new UI gets optimistic concurrency protection.
		saved, err = s.store.SavePanelSettings(request.Context(), settings)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "settings.saved", "", "api")
	if agentPolicyChanged(previous, saved) {
		s.DisconnectAllAgents()
	}
	writeJSON(w, http.StatusOK, panelSettingsResponse(saved))
}

func agentPolicyChanged(left, right core.PanelSettings) bool {
	return left.AgentHeartbeatIntervalSeconds != right.AgentHeartbeatIntervalSeconds ||
		left.AgentMetricsIntervalSeconds != right.AgentMetricsIntervalSeconds ||
		left.AgentOfflineThresholdSeconds != right.AgentOfflineThresholdSeconds ||
		left.PublicIPProbeIntervalSeconds != right.PublicIPProbeIntervalSeconds ||
		left.AgentCoreLogMaxMiB != right.AgentCoreLogMaxMiB ||
		left.AgentCoreLogRotateCount != right.AgentCoreLogRotateCount
}

func (s *Server) getDeploymentSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"control_plane_version":        s.controlPlaneVersion,
		"agent_package_version":        s.agentVersion,
		"secure_transport":             s.secureTransport,
		"database_tls_verified":        s.databaseTLSVerified,
		"config_encryption_configured": s.configEncryptionConfigured,
		"webhook_signing_configured":   s.webhookSigningConfigured,
		"trusted_proxy_count":          len(s.trustedProxies),
	})
}

var releaseVersionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:\+.*)?$`)
var commitVersionPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func releaseVersion(value string) ([3]int, bool) {
	match := releaseVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 4 {
		return [3]int{}, false
	}
	var parsed [3]int
	for index := range parsed {
		part, err := strconv.Atoi(match[index+1])
		if err != nil {
			return [3]int{}, false
		}
		parsed[index] = part
	}
	return parsed, true
}

func versionOlder(current, latest string) (bool, bool) {
	left, leftOK := releaseVersion(current)
	right, rightOK := releaseVersion(latest)
	if !leftOK || !rightOK {
		return false, false
	}
	for index := range left {
		if left[index] != right[index] {
			return left[index] < right[index], true
		}
	}
	return false, true
}

func imageVersionStatus(current, latest string) (bool, bool) {
	current = strings.ToLower(strings.TrimSpace(current))
	latest = strings.ToLower(strings.TrimSpace(latest))
	if commitVersionPattern.MatchString(current) && commitVersionPattern.MatchString(latest) {
		matches := strings.HasPrefix(current, latest) || strings.HasPrefix(latest, current)
		return !matches, true
	}
	return versionOlder(current, latest)
}

type registryManifest struct {
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		} `json:"platform"`
	} `json:"manifests"`
}

func registryJSON(ctx context.Context, client *http.Client, token, endpoint, accept string, target any) error {
	upstream, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if token != "" {
		upstream.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		upstream.Header.Set("Accept", accept)
	}
	upstream.Header.Set("User-Agent", "QControlHub/update-check")
	response, err := client.Do(upstream)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("registry returned %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 128<<10)).Decode(target); err != nil {
		return fmt.Errorf("decode registry response: %w", err)
	}
	return nil
}

func latestImageVersion(ctx context.Context, client *http.Client) (string, time.Time, error) {
	var authorization struct {
		Token string `json:"token"`
	}
	if err := registryJSON(ctx, client, "", "https://ghcr.io/token?scope=repository:qimaoww/qcontrol-plane:pull&service=ghcr.io", "application/json", &authorization); err != nil {
		return "", time.Time{}, err
	}
	if strings.TrimSpace(authorization.Token) == "" {
		return "", time.Time{}, errors.New("registry authorization token is empty")
	}
	const registryRoot = "https://ghcr.io/v2/qimaoww/qcontrol-plane/"
	const manifestAccept = "application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json"
	var manifest registryManifest
	if err := registryJSON(ctx, client, authorization.Token, registryRoot+"manifests/latest", manifestAccept, &manifest); err != nil {
		return "", time.Time{}, err
	}
	if manifest.Config.Digest == "" {
		var selected string
		for _, candidate := range manifest.Manifests {
			if candidate.Platform.OS == "linux" && candidate.Platform.Architecture == "amd64" {
				selected = candidate.Digest
				break
			}
		}
		if selected == "" {
			return "", time.Time{}, errors.New("latest image has no linux/amd64 manifest")
		}
		if err := registryJSON(ctx, client, authorization.Token, registryRoot+"manifests/"+selected, manifestAccept, &manifest); err != nil {
			return "", time.Time{}, err
		}
	}
	if !strings.HasPrefix(manifest.Config.Digest, "sha256:") {
		return "", time.Time{}, errors.New("latest image config digest is invalid")
	}
	var imageConfig struct {
		Created time.Time `json:"created"`
		Config  struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if err := registryJSON(ctx, client, authorization.Token, registryRoot+"blobs/"+manifest.Config.Digest, "application/vnd.oci.image.config.v1+json", &imageConfig); err != nil {
		return "", time.Time{}, err
	}
	version := strings.TrimSpace(imageConfig.Config.Labels["org.opencontainers.image.version"])
	if !commitVersionPattern.MatchString(strings.ToLower(version)) && !releaseVersionPattern.MatchString(version) {
		return "", time.Time{}, errors.New("latest image version label is invalid")
	}
	return version, imageConfig.Created, nil
}

func (s *Server) checkUpdate(w http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(next *http.Request, previous []*http.Request) error {
			if len(previous) >= 5 {
				return errors.New("too many container registry redirects")
			}
			host := strings.ToLower(next.URL.Hostname())
			if next.URL.Scheme != "https" || (host != "ghcr.io" && host != "pkg-containers.githubusercontent.com") {
				return errors.New("container registry redirect was rejected")
			}
			return nil
		},
	}
	latest, publishedAt, err := latestImageVersion(ctx, client)
	if err != nil {
		writeError(w, http.StatusBadGateway, "检查 GHCR 最新镜像失败")
		return
	}
	available, comparable := imageVersionStatus(s.controlPlaneVersion, latest)
	displayLatest := latest
	if commitVersionPattern.MatchString(strings.ToLower(latest)) && len(latest) > 7 {
		displayLatest = latest[:7]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"current_control_plane": s.controlPlaneVersion,
		"current_agent_package": s.agentVersion,
		"latest_version":        displayLatest,
		"latest_revision":       latest,
		"release_url":           "https://github.com/qimaoww/qcontrolhub/pkgs/container/qcontrol-plane",
		"published_at":          publishedAt,
		"source":                "ghcr-latest",
		"comparable":            comparable,
		"update_available":      available,
	})
}

func (s *Server) listAudit(w http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
		limit = parsed
	}
	entries, err := s.store.ListAuditLogs(request.Context(), limit)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) listCoreLogs(w http.ResponseWriter, request *http.Request) {
	values := request.URL.Query()
	query := store.CoreLogQuery{
		AgentID: strings.TrimSpace(values.Get("agent_id")),
		Level:   strings.TrimSpace(values.Get("level")),
		Search:  strings.TrimSpace(values.Get("q")),
		Limit:   200,
	}
	if query.AgentID != "" && !validAgentID(query.AgentID) {
		writeError(w, http.StatusBadRequest, "invalid agent_id filter")
		return
	}
	if raw := strings.TrimSpace(values.Get("engine")); raw != "" {
		engine, err := core.ParseEngine(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid engine filter")
			return
		}
		query.Engine = engine
	}
	if query.Level != "" && query.Level != "debug" && query.Level != "info" && query.Level != "warning" && query.Level != "error" && query.Level != "critical" {
		writeError(w, http.StatusBadRequest, "invalid level filter")
		return
	}
	if raw := strings.TrimSpace(values.Get("before")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "before must be a positive integer")
			return
		}
		query.Before = parsed
	}
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		query.Limit = parsed
	}
	entries, err := s.store.ListCoreLogs(request.Context(), query)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) metricSamples(w http.ResponseWriter, request *http.Request) {
	since := time.Now().UTC().Add(-24 * time.Hour)
	if raw := strings.TrimSpace(request.URL.Query().Get("since")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be RFC3339")
			return
		}
		since = parsed
	}
	limit := 1500
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 5000 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 5000")
			return
		}
		limit = parsed
	}
	samples, err := s.store.MetricSamples(request.Context(), request.PathValue("id"), since, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, samples)
}

func (s *Server) listTemplates(w http.ResponseWriter, request *http.Request) {
	templates, err := s.store.ListConfigTemplates(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (s *Server) createTemplate(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Name    string `json:"name"`
		Engine  string `json:"engine"`
		Content string `json:"content"`
	}
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	template, err := s.store.CreateConfigTemplate(request.Context(), input.Name, input.Engine, input.Content)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "template.created", template.ID, template.Name)
	writeJSON(w, http.StatusCreated, template)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteConfigTemplate(request.Context(), request.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "template.deleted", request.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) applyTemplate(w http.ResponseWriter, request *http.Request) {
	var input struct {
		AgentID string `json:"agent_id"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.AgentID = strings.TrimSpace(input.AgentID)
	if input.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	template, agent, rendered, err := s.store.RenderTemplateForAgent(request.Context(), request.PathValue("id"), input.AgentID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	current, currentErr := s.store.AgentConfig(request.Context(), input.AgentID, template.Engine)
	expectedVersion := 0
	if currentErr == nil {
		expectedVersion = current.Version
	} else if !errors.Is(currentErr, store.ErrNotFound) {
		writeStoreError(w, currentErr)
		return
	}
	saved, err := s.store.SaveAgentConfig(request.Context(), core.Config{AgentID: input.AgentID, Name: template.Name + " · 模板", Description: "由配置模板渲染", Engine: template.Engine, Content: rendered}, expectedVersion)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.refreshPortTrafficMonitoring(request.Context(), "")
	s.recordAudit(request, "template.applied", template.ID, agent.Name+" "+string(template.Engine))
	writeJSON(w, http.StatusOK, saved)
}
