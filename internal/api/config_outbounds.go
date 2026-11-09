package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) getManagedOutbound(w http.ResponseWriter, r *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, r)
	if !ok {
		return
	}
	config, err := s.store.AgentConfig(r.Context(), agent.ID, engine)
	snapshot := r.URL.Query().Get("snapshot")
	if err != nil && !(errors.Is(err, store.ErrNotFound) && snapshot != "") {
		writeStoreError(w, err)
		return
	}
	if snapshot != "" {
		if !s.sessionAllows(r, core.PermissionTasksRead) {
			writeError(w, http.StatusForbidden, "没有读取配置快照的权限")
			return
		}
		config.Content, err = s.store.ReadTaskConfigSnapshot(r.Context(), snapshot, agent.ID, engine)
		if err != nil {
			writeStoreError(w, err)
			return
		}
	}
	fragment, err := serverconfig.ManagedOutboundFragment(engine, config.Content, r.PathValue("section"), r.PathValue("tag"))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	object, err := serverconfig.NativePresetObject(engine, fragment)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, struct {
		Version  int    `json:"version"`
		Fragment string `json:"fragment"`
		Object   string `json:"object"`
	}{config.Version, fragment, object})
}

func (s *Server) saveManagedOutbound(w http.ResponseWriter, r *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, r)
	if !ok {
		return
	}
	var input struct {
		SnapshotTaskID     string      `json:"snapshot_task_id"`
		InboundTag         string      `json:"inbound_tag"`
		InboundPort        int         `json:"inbound_port"`
		ReplacementTag     string      `json:"replacement_tag"`
		PresetBase         string      `json:"preset_base"`
		ExpectedInstanceID *string     `json:"expected_instance_id,omitempty"`
		Version            int         `json:"version"`
		Intent             core.Action `json:"intent"`
		Section            string      `json:"section"`
		Tag                string      `json:"tag"`
		Fragment           string      `json:"fragment"`
		Operation          string      `json:"operation"`
	}
	if err := decodeJSON(w, r, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	config, err := s.store.AgentConfig(r.Context(), agent.ID, engine)
	if errors.Is(err, store.ErrNotFound) && (input.Operation == "add" || input.SnapshotTaskID != "") {
		content := `{"inbounds":[],"outbounds":[]}`
		if engine == core.EngineMihomo {
			content = "listeners: []\nproxies: []\nrules: ['MATCH,DIRECT']\n"
		}
		config = core.Config{AgentID: agent.ID, Engine: engine, Name: agent.Name + " · " + string(engine), Content: content}
		err = nil
	} else if err != nil {
		writeStoreError(w, err)
		return
	}
	if config.Version != input.Version {
		writeStoreError(w, store.ErrConflict)
		return
	}
	state := serverconfig.ManagedState{}
	if config.ID != "" {
		state, err = s.store.ConfigManagementState(r.Context(), config.ID, config.Version)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if input.SnapshotTaskID != "" {
		if !s.sessionAllows(r, core.PermissionTasksRead) {
			writeError(w, http.StatusForbidden, "没有读取配置快照的权限")
			return
		}
		snapshot, readErr := s.store.ReadTaskConfigSnapshot(r.Context(), input.SnapshotTaskID, agent.ID, engine)
		if readErr != nil {
			writeStoreError(w, readErr)
			return
		}
		config.Content, err = s.accountingUpdateSource(r.Context(), config, snapshot)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if input.PresetBase != "" {
		if input.Operation != "modify" {
			writeError(w, 400, "预设回填仅用于修改已有出站")
			return
		}
		original, readErr := serverconfig.ManagedOutboundFragment(engine, config.Content, input.Section, input.Tag)
		if readErr != nil {
			writeError(w, 400, readErr.Error())
			return
		}
		input.Fragment, err = serverconfig.PatchNativePreset(engine, original, input.PresetBase, input.Fragment)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	config.Content, err = s.presetAccountingSource(r.Context(), config)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if input.ReplacementTag != "" {
		if input.Operation != "delete" {
			writeError(w, 400, "仅删除出站时选择替代出站")
			return
		}
		config.Content, err = serverconfig.ReplaceManagedOutboundReferences(engine, config.Content, input.Tag, input.ReplacementTag, state)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	if input.Operation != "bind" {
		config.Content, state, err = serverconfig.MutateManagedOutbound(engine, config.Content, input.Section, input.Tag, input.Fragment, input.Operation, state)
	} else if input.InboundTag == "" {
		writeError(w, 400, "请选择绑定入站")
		return
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if input.InboundTag != "" {
		target := input.Tag
		if input.Operation == "add" || input.Operation == "modify" {
			object, parseErr := serverconfig.NativePresetObject(engine, input.Fragment)
			if parseErr != nil {
				writeError(w, 400, parseErr.Error())
				return
			}
			var native map[string]any
			json.Unmarshal([]byte(object), &native)
			key := "tag"
			if engine == core.EngineMihomo {
				key = "name"
			}
			target, _ = native[key].(string)
		}
		config.Content, err = serverconfig.BindManagedOutbound(engine, config.Content, input.InboundTag, input.InboundPort, target, state)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	if len(serverconfig.DiscoverTrafficPorts(engine, config.Content)) > 0 {
		plan, planErr := serverconfig.PlanPresetAccounting(engine, config.Content)
		if planErr != nil {
			writeError(w, 400, planErr.Error())
			return
		}
		config.Content = plan.Content
	}
	saved, task, err := s.store.SaveAgentConfigAndTask(r.Context(), config, input.Version, store.ConfigMutationOptions{TaskID: r.Header.Get("X-Config-Task-ID"), ExpectedInstanceID: input.ExpectedInstanceID, Action: input.Intent, ManagementState: &state})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	s.recordAudit(r, "config.outbound.updated", saved.ID, string(engine))
	writeJSON(w, 200, configMutationResult{Config: saved, Task: task})
}
