package api

import (
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

func (s *Server) getAgentConfig(w http.ResponseWriter, request *http.Request) {
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.store.AgentConfig(request.Context(), request.PathValue("id"), engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) putAgentConfig(w http.ResponseWriter, request *http.Request) {
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input, err := decodeEditableConfig(w, request, func(version int) (core.Config, error) {
		return s.store.AgentConfig(request.Context(), request.PathValue("id"), engine)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if input.Engine != "" && input.Engine != engine {
		writeError(w, http.StatusBadRequest, "configuration engine does not match the URL")
		return
	}
	input.AgentID = request.PathValue("id")
	input.Engine = engine
	s.saveAgentConfigResponse(w, request, input)
}

func (s *Server) saveAgentConfigResponse(w http.ResponseWriter, request *http.Request, input core.Config) {
	current, err := s.store.AgentConfig(request.Context(), input.AgentID, input.Engine)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	if _, key := requestAPIKey(request); key && err == nil {
		input.Content, err = core.PreserveRedactedConfig(current.Content, input.Content)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	if err == nil && current.Version != input.Version {
		writeStoreError(w, store.ErrConflict)
		return
	}
	if err == nil && (strings.Contains(current.Content, "qch-trf-") || strings.Contains(input.Content, "qch-trf-") || input.Engine == core.EngineShadowsocksRust && strings.Contains(current.Content, "outbound_fwmark")) {
		input.Content, err = s.accountingUpdateSource(request.Context(), current, input.Content)
		if err == nil && len(serverconfig.DiscoverTrafficPorts(input.Engine, input.Content)) > 0 {
			var plan serverconfig.AccountingPlan
			plan, err = serverconfig.PlanPresetAccounting(input.Engine, input.Content)
			if err == nil {
				input.Content, err = serverconfig.PresetAccountingSource(input.Engine, plan.Content)
			}
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	candidate := input
	candidate.Version++
	policies, err := s.planShadowsocksRustPolicies(request.Context(), candidate)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	config, err := s.store.SaveAgentConfigWithPolicies(request.Context(), input, input.Version, policies)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.refreshSavedAgentTrafficMonitoring(request.Context(), config.AgentID)
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) listConfigs(w http.ResponseWriter, request *http.Request) {
	configs, err := s.store.ListConfigs(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configs)
}

func (s *Server) createConfig(w http.ResponseWriter, request *http.Request) {
	var input core.Config
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, key := requestAPIKey(request); key && strings.Contains(input.Content, "[redacted") {
		writeError(w, 400, "新配置不能使用脱敏占位符")
		return
	}

	config, err := s.store.CreateConfig(request.Context(), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.created", config.ID, config.Name+" ("+string(config.Engine)+")")
	writeJSON(w, http.StatusCreated, config)
}

func (s *Server) updateConfig(w http.ResponseWriter, request *http.Request) {
	input, err := decodeEditableConfig(w, request, func(version int) (core.Config, error) {
		return s.store.ConfigRevision(request.Context(), request.PathValue("id"), version)
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}

	config, err := s.store.UpdateConfig(request.Context(), request.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.updated", config.ID, "v"+strconv.Itoa(config.Version)+" "+config.Name)
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) deleteConfig(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteConfig(request.Context(), request.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.deleted", request.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listConfigRevisions(w http.ResponseWriter, request *http.Request) {
	limit := 20
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "revision limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	revisions, err := s.store.ListConfigRevisions(request.Context(), request.PathValue("id"), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, revisions)
}

func (s *Server) getConfigRevision(w http.ResponseWriter, request *http.Request) {
	version, err := strconv.Atoi(request.PathValue("version"))
	if err != nil || version < 1 {
		writeError(w, http.StatusBadRequest, "revision version must be positive")
		return
	}
	revision, err := s.store.ConfigRevision(request.Context(), request.PathValue("id"), version)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, revision)
}

func (s *Server) restoreConfigRevision(w http.ResponseWriter, request *http.Request) {
	version, err := strconv.Atoi(request.PathValue("version"))
	if err != nil || version < 1 {
		writeError(w, http.StatusBadRequest, "revision version must be positive")
		return
	}
	var input struct {
		ExpectedVersion   int  `json:"expected_version"`
		DisableMutableAPI bool `json:"disable_mutable_api"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "expected_version must be positive")
		return
	}
	restored, err := s.store.RestoreConfigRevision(request.Context(), request.PathValue("id"), version, input.ExpectedVersion, store.ConfigRestoreOptions{DisableMutableAPI: input.DisableMutableAPI})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.restored", restored.ID, "v"+strconv.Itoa(version)+" -> v"+strconv.Itoa(restored.Version))
	writeJSON(w, http.StatusOK, restored)
}

// Saved archives opt into preparation through a configuration-scoped endpoint.
// General task JSON continues to reject install_if_missing. The Store repeats
// ownership, exact revision, target allocation and installation authorization.
func (s *Server) createConfigExecutionTask(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AgentID               string      `json:"agent_id"`
		Action                core.Action `json:"action"`
		ExpectedConfigVersion int         `json:"expected_config_version"`
		ExpectedInstanceID    *string     `json:"expected_instance_id"`
		InstallIfMissing      bool        `json:"install_if_missing"`
	}
	if err := decodeJSON(w, r, &input, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if (input.Action != core.ActionValidate && input.Action != core.ActionDeploy) || input.ExpectedConfigVersion < 1 || input.ExpectedInstanceID == nil {
		writeError(w, http.StatusBadRequest, "请选择校验或部署，并指定已保存配置及目标实例。")
		return
	}
	config, err := s.store.ConfigRevision(r.Context(), r.PathValue("id"), input.ExpectedConfigVersion)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task, err := s.store.CreateTask(r.Context(), core.TaskRequest{AgentID: input.AgentID, Engine: config.Engine, ConfigID: config.ID, ExpectedConfigVersion: input.ExpectedConfigVersion, Action: input.Action, ExpectedInstanceID: input.ExpectedInstanceID, InstallIfMissing: input.InstallIfMissing})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	if !task.Reused {
		s.recordAudit(r, "config.task_created", task.ID, string(task.Action)+" "+string(task.Engine)+" "+task.AgentID)
	}
	status := http.StatusCreated
	if task.Reused {
		status = http.StatusOK
	}
	writeJSON(w, status, task)
}

// configEditInput distinguishes omitted/null content from an explicit replacement.
// Metadata remains a complete PUT representation, as in the browser API.
type configEditInput struct {
	core.Config
	Content *string `json:"content"`
}

func decodeEditableConfig(w http.ResponseWriter, request *http.Request, read func(int) (core.Config, error)) (core.Config, error) {
	if _, key := requestAPIKey(request); !key {
		var input core.Config
		if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
			return input, fmtInvalidConfig(err)
		}
		return input, nil
	}
	var input configEditInput
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		return core.Config{}, fmtInvalidConfig(err)
	}
	if input.Content != nil {
		input.Config.Content = *input.Content
	}
	metadata := input.Config
	metadata.Content = ""
	raw, _ := json.Marshal(metadata)
	if strings.Contains(string(raw), "[redacted") {
		return core.Config{}, fmtInvalidConfig(errors.New("配置元数据不能使用脱敏占位符"))
	}
	if input.Content == nil || strings.Contains(input.Config.Content, "[redacted") {
		current, err := read(input.Version)
		if err != nil {
			return core.Config{}, err
		}
		if input.Version != current.Version {
			return core.Config{}, store.ErrConflict
		}
		if input.Content == nil {
			input.Config.Content = current.Content
		} else {
			input.Config.Content, err = core.PreserveRedactedConfig(current.Content, *input.Content)
			if err != nil {
				return core.Config{}, fmtInvalidConfig(err)
			}
		}
	}
	return input.Config, nil
}
func fmtInvalidConfig(err error) error { return fmt.Errorf("%w: %v", store.ErrInvalid, err) }
