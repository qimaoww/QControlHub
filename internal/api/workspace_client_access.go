package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// This opaque token binds a rendered row to its exact account, instance,
// configuration body and client-only metadata. It is never a display revision.
func workspaceClientSource(agent core.Agent, config core.Config, inputs []serverconfig.Input) string {
	instance := ""
	if v := agent.Runtime[config.Engine].Instance; v != nil {
		instance = v.ID
	}
	share := ""
	if agent.SharedAllocation != nil {
		share = agent.SharedAllocation.ID
	}
	data, _ := json.Marshal([]any{agent.ID, instance, share, config.OwnerID, config.ID, config.Engine, config.Version, config.Content, inputs})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Server) exportWorkspaceInbound(w http.ResponseWriter, r *http.Request) {
	engine, err := core.ParseEngine(r.PathValue("engine"))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	agent, err := s.store.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	config, err := s.store.AgentConfig(r.Context(), agent.ID, engine)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	original := config
	config.AgentID, config.Engine = agent.ID, engine
	snapshot := r.URL.Query().Get("snapshot")
	if snapshot != "" {
		if !s.sessionAllows(r, core.PermissionTasksRead) {
			writeError(w, http.StatusForbidden, "没有读取配置快照的权限")
			return
		}
		config.Content, err = s.store.ReadWorkspaceTaskConfigSnapshot(r.Context(), snapshot, agent.ID, engine)
		if err != nil {
			writeStoreError(w, err)
			return
		}
	}
	inputs := serverconfig.ParseAll(engine, config.Content)
	if config.ID != "" && config.Content == original.Content {
		if err := s.hydrateClientMetadata(r.Context(), config, inputs); err != nil {
			writeInternalError(w, err)
			return
		}
	}
	if token := r.URL.Query().Get("source"); token == "" || token != workspaceClientSource(agent, config, inputs) {
		writeError(w, 409, "配置或实例已变化，请重新读取并核对入站")
		return
	}
	port, _ := strconv.Atoi(r.URL.Query().Get("port"))
	var matches []serverconfig.Input
	for _, input := range inputs {
		if input.Tag == r.URL.Query().Get("tag") && input.Port == port && input.Protocol == r.URL.Query().Get("protocol") {
			matches = append(matches, input)
		}
	}
	if len(matches) != 1 {
		writeError(w, 409, "无法唯一确定此入站，请重新读取并核对")
		return
	}
	preferences, err := s.store.ConfigClientPreferences(r.Context(), config.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	labels := cloneClientLabels(agent.Labels, preferences)
	item, err := buildClientAccessProfile(engine, matches[0], clientAddressCandidates(agent), firstLabel(agent, "tls_server_name", "server_name"), labels)
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	content, format, err := workspaceClientExport(item.Profile)
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	// Recheck access and the workspace after generation. No save, plan, task or
	// credential generation is involved, including for offline/shared instances.
	latestAgent, err := s.store.GetAgent(r.Context(), agent.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	latest, err := s.store.AgentConfig(r.Context(), agent.ID, engine)
	if err != nil && !(original.ID == "" && errors.Is(err, store.ErrNotFound)) {
		writeStoreError(w, err)
		return
	}
	if latest.ID != original.ID || latest.Version != original.Version || latest.Content != original.Content || workspaceClientSource(latestAgent, config, inputs) != workspaceClientSource(agent, config, inputs) {
		writeError(w, 409, "配置或实例已变化，请重新读取并核对入站")
		return
	}
	// Bypass the middleware's request-local session copy so logout and expiry
	// during generation are observed before disclosing credentials.
	fresh := r.WithContext(context.WithValue(r.Context(), validatedSessionKey{}, struct{}{}))
	validated, valid := s.validateSession(w, fresh)
	if !valid {
		return
	}
	if !s.sessionAllows(validated, core.PermissionAgentConfigRead) || !s.sessionAllows(validated, core.PermissionClientAccessRead) || (snapshot != "" && !s.sessionAllows(validated, core.PermissionTasksRead)) {
		writeError(w, http.StatusForbidden, "客户端导出授权已变化，请重新登录后核对")
		return
	}
	source := "saved"
	if snapshot != "" {
		source = "snapshot"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"content": content, "format": format, "source": source})
}

// Variants are alternatives, not independent inbounds. Export every unique
// alternative exactly once, retaining native formats when a URI is incomplete.
func workspaceClientExport(profile serverconfig.ClientProfile) (string, string, error) {
	if len(profile.Variants) > 0 {
		// A group containing native Xray alternatives must remain one valid
		// client document, rather than concatenated JSON documents or a mix of
		// JSON and URIs. Each alternative gets its own outbound tag.
		native := false
		for _, v := range profile.Variants {
			native = native || v.Profile.URIError != "" || !strings.Contains(v.Profile.URI, "://")
		}
		if native {
			var document map[string]any
			var outbounds []any
			seen := map[string]bool{}
			for _, v := range profile.Variants {
				var doc map[string]any
				if json.Unmarshal([]byte(v.Profile.Xray), &doc) != nil {
					return "", "", fmt.Errorf("%s 缺少完整 Xray 客户端配置", v.Name)
				}
				if document == nil {
					document = doc
				}
				items, ok := doc["outbounds"].([]any)
				if !ok || len(items) == 0 {
					return "", "", fmt.Errorf("%s 缺少客户端出站配置", v.Name)
				}
				for _, outbound := range items {
					data, _ := json.Marshal(outbound)
					if seen[string(data)] {
						continue
					}
					seen[string(data)] = true
					value, ok := outbound.(map[string]any)
					if !ok {
						return "", "", errors.New("客户端出站配置无效")
					}
					value["tag"] = fmt.Sprintf("proxy-%d", len(outbounds)+1)
					outbounds = append(outbounds, value)
				}
			}
			if len(outbounds) == 0 {
				return "", "", errors.New("缺少客户端出站配置")
			}
			document["outbounds"] = outbounds
			data, err := json.MarshalIndent(document, "", "  ")
			return string(data), "Xray client JSON", err
		}
		seen := map[string]bool{}
		var values, formats []string
		for _, variant := range profile.Variants {
			value, format, err := workspaceClientExport(variant.Profile)
			if err != nil {
				return "", "", fmt.Errorf("%s：%w", variant.Name, err)
			}
			if !seen[value] {
				seen[value] = true
				values = append(values, value)
				formats = append(formats, format)
			}
		}
		if len(values) == 0 {
			return "", "", errors.New("暂无可用客户端配置")
		}
		return strings.Join(values, "\n\n"), strings.Join(formats, " / "), nil
	}
	if strings.TrimSpace(profile.URI) != "" {
		return profile.URI, profile.Format, nil
	}
	if strings.TrimSpace(profile.Xray) != "" {
		return profile.Xray, "Xray client JSON", nil
	}
	if profile.URIError != "" {
		return "", "", errors.New(profile.URIError)
	}
	return "", "", errors.New("缺少客户端连接参数")
}
