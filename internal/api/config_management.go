package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) getConfigManagement(w http.ResponseWriter, r *http.Request) {
	engine, err := core.ParseEngine(r.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.store.AgentConfig(r.Context(), r.PathValue("id"), engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	state, err := s.store.ConfigManagementState(r.Context(), config.ID, config.Version)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Version int `json:"version"`
		serverconfig.ManagedState
	}{config.Version, state})
}

func (s *Server) putConfigManagement(w http.ResponseWriter, r *http.Request) {
	engine, err := core.ParseEngine(r.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		OutboundTag        string                         `json:"outbound_tag"`
		ReplacementTag     string                         `json:"replacement_tag"`
		ExpectedInstanceID *string                        `json:"expected_instance_id,omitempty"`
		Version            int                            `json:"version"`
		Intent             core.Action                    `json:"intent"`
		Disabled           []serverconfig.ManagedResource `json:"disabled"`
	}
	if err = decodeJSON(w, r, &input, 1048576); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.store.AgentConfig(r.Context(), r.PathValue("id"), engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if input.Version != config.Version {
		writeStoreError(w, store.ErrConflict)
		return
	}
	if input.ReplacementTag != "" {
		targeted := false
		for _, ref := range input.Disabled {
			if ref.Tag == input.OutboundTag && (ref.Section == "outbounds" || ref.Section == "proxies" || ref.Section == "proxy-groups") {
				targeted = true
			}
		}
		if !targeted {
			writeError(w, 400, "请选择要停用的出站")
			return
		}
		config.Content, err = s.presetAccountingSource(r.Context(), config)
		if err == nil {
			config.Content, err = serverconfig.ReplaceManagedOutboundReferences(engine, config.Content, input.OutboundTag, input.ReplacementTag, serverconfig.ManagedState{Disabled: input.Disabled})
		}
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	saved, task, err := s.store.SaveAgentConfigAndTask(r.Context(), config, input.Version, store.ConfigMutationOptions{TaskID: r.Header.Get("X-Config-Task-ID"), ExpectedInstanceID: input.ExpectedInstanceID, Action: input.Intent, ManagementState: &serverconfig.ManagedState{Disabled: input.Disabled}})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	s.recordAudit(r, "config.management.updated", saved.ID, string(engine))
	writeJSON(w, http.StatusOK, struct {
		Config core.Config `json:"config"`
		Task   core.Task   `json:"task"`
	}{saved, task})
}
