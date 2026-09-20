package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func (s *Server) listAgents(w http.ResponseWriter, request *http.Request) {
	list := s.store.ListAgents
	if s.sessionAllows(request, core.PermissionEnrollmentManage) {
		list = s.store.ListAgentsWithEnrollmentCommands
	}
	agents, err := list(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	// Resolve derived display data before the metrics redaction below: region
	// detection reads the public address out of those metrics.
	s.resolveAgentRegions(request, agents)
	s.attachAgentKomari(request, agents)
	if !s.sessionAllows(request, core.PermissionMetricsRead) {
		for index := range agents {
			agents[index].Metrics = core.HostMetrics{}
		}
	}
	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) redactAgentMetrics(request *http.Request, agent *core.Agent) {
	if !s.sessionAllows(request, core.PermissionMetricsRead) {
		agent.Metrics = core.HostMetrics{}
	}
}

func (s *Server) putAgentName(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if err := s.store.SetAgentName(request.Context(), request.PathValue("id"), input.Name); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "agent.renamed", request.PathValue("id"), input.Name)
	writeJSON(w, http.StatusOK, input)
}

func (s *Server) deleteAgent(w http.ResponseWriter, request *http.Request) {
	agentID := request.PathValue("id")
	if err := s.store.DeleteAgent(request.Context(), agentID); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Error("agent deletion timed out", "agent_id", agentID, "error", err)
			writeError(w, http.StatusGatewayTimeout, "删除节点超时，请刷新确认节点状态后再重试；若持续超时，请管理员查看服务日志。")
			return
		}
		writeStoreError(w, err)
		return
	}
	s.DisconnectAgent(agentID)
	s.recordAudit(request, "agent.deleted", agentID, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) putAgentClientAddress(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Address     *string                `json:"address"`
		Name        *string                `json:"name"`
		AddressMode *string                `json:"address_mode"`
		Profile     *clientProfileSelector `json:"profile"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var address *string
	if input.Address != nil {
		value := strings.TrimSpace(*input.Address)
		address = &value
	}
	if address != nil && *address != "" {
		var err error
		value, err := serverconfig.NormalizeClientAddress(*address)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		address = &value
	}
	var name *string
	if input.Name != nil {
		value := strings.TrimSpace(*input.Name)
		if value != "" && (utf8.RuneCountInString(value) > 100 || strings.ContainsAny(value, "\r\n")) {
			writeError(w, http.StatusBadRequest, "客户端节点名称不能超过 100 个字符")
			return
		}
		name = &value
	}
	var addressMode *string
	if input.AddressMode != nil {
		value := strings.TrimSpace(*input.AddressMode)
		if value != core.SubStoreAddressModeAuto && value != core.SubStoreAddressModeIPv4 && value != core.SubStoreAddressModeIPv6 {
			writeError(w, http.StatusBadRequest, "客户端地址协议栈必须是 auto、ipv4 或 ipv6")
			return
		}
		addressMode = &value
	}
	if address == nil && name == nil && addressMode == nil {
		writeError(w, http.StatusBadRequest, "至少需要提供一个客户端显示参数")
		return
	}
	var saveErr error
	if input.Profile != nil {
		label, err := s.clientProfileNameLabel(request.Context(), request.PathValue("id"), *input.Profile)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		saveErr = s.store.SetConfigClientPreferences(request.Context(), request.PathValue("id"), label, address, name, addressMode)
	} else {
		saveErr = s.store.SetAgentClientPreferences(request.Context(), request.PathValue("id"), address, name, addressMode)
	}
	if err := saveErr; err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "agent.client_display.updated", request.PathValue("id"), "client display preferences updated")
	result := map[string]string{}
	if address != nil {
		result["address"] = *address
	}
	if name != nil {
		result["name"] = *name
	}
	if addressMode != nil {
		result["address_mode"] = *addressMode
	}
	writeJSON(w, http.StatusOK, result)
}
