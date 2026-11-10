package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

func agentTimeZoneAddress(agent core.Agent) netip.Addr {
	for _, candidate := range []struct {
		value  string
		source string
		family bool
	}{
		{agent.Metrics.PublicIPv4, agent.Metrics.PublicIPv4Source, true},
		{agent.Metrics.PublicIPv6, agent.Metrics.PublicIPv6Source, false},
	} {
		if candidate.source != core.PublicIPProbeSourceAgent && candidate.source != core.PublicIPProbeSourceControlPlane {
			continue
		}
		address, err := netip.ParseAddr(authn.NormalizePublicIP(candidate.value))
		if err != nil || address.Is4() != candidate.family || !netpolicy.IsPublicAddress(address) || netpolicy.IsCloudflareAddress(address) {
			continue
		}
		return address.Unmap()
	}
	for _, candidate := range publicInterfaceAddresses(agent.Metrics.NetworkInterfaces) {
		address, parseErr := netip.ParseAddr(candidate.address)
		if parseErr == nil && netpolicy.IsPublicAddress(address) && !netpolicy.IsCloudflareAddress(address) {
			return address.Unmap()
		}
	}
	address, err := netip.ParseAddr(authn.NormalizePublicIP(agent.Metrics.ObservedPublicIP))
	if err == nil && netpolicy.IsPublicAddress(address) && !netpolicy.IsCloudflareAddress(address) {
		return address.Unmap()
	}
	return netip.Addr{}
}

func timeZoneAddressHash(address netip.Addr) string {
	value := "unavailable"
	if address.IsValid() {
		value = address.String()
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func agentTimeZoneCommandNeeded(state core.AgentTimeZoneState, lastAckRevision int64, force bool) bool {
	if state.Mode == "auto" && state.Status == "failed" && state.Error == core.AgentTimeZoneLookupFailure {
		return false
	}
	if state.TimeZone == "" || state.Revision == lastAckRevision {
		return force && state.TimeZone != ""
	}
	return true
}

func (s *Server) getAgentTimeZone(w http.ResponseWriter, request *http.Request) {
	agent, err := s.store.GetAgent(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !agent.CanManage {
		writeError(w, http.StatusForbidden, "只有节点所有者可以查看本机时区设置")
		return
	}
	state, err := s.store.GetAgentTimeZone(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	state.AutoAddressHash = ""
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) putAgentTimeZone(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Mode     string `json:"mode"`
		TimeZone string `json:"time_zone"`
		Revision int64  `json:"revision"`
		Retry    bool   `json:"retry"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.Mode = strings.TrimSpace(input.Mode)
	input.TimeZone = strings.TrimSpace(input.TimeZone)
	if input.Mode != "auto" && input.Mode != "custom" {
		writeError(w, http.StatusBadRequest, "时区模式须为自动或自定义")
		return
	}
	if input.Mode == "custom" && !core.ValidTimeZone(input.TimeZone) {
		writeError(w, http.StatusBadRequest, "请输入有效的 IANA 时区，例如 Asia/Shanghai")
		return
	}
	if input.Mode == "auto" && input.TimeZone != "" {
		writeError(w, http.StatusBadRequest, "自动模式不接受自定义时区")
		return
	}
	agentID := request.PathValue("id")
	agent, err := s.store.GetAgent(request.Context(), agentID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	mode, zone := input.Mode, input.TimeZone
	state, changed, err := s.store.SetAgentTimeZone(request.Context(), agentID, mode, zone, input.Revision, input.Retry)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !changed {
		state.AutoAddressHash = ""
		writeJSON(w, http.StatusOK, state)
		return
	}
	if mode == "auto" {
		state, err = s.resolveAgentTimeZone(request.Context(), agentID, true)
		if err != nil {
			writeInternalError(w, err)
			return
		}
	}
	if agent.Status != "online" && state.Status != "failed" {
		_ = s.store.SetAgentTimeZoneStatus(request.Context(), agentID, state.Revision, "pending", "")
		state.Status, state.Error = "pending", ""
	} else if agent.Status == "online" && !agentHasFeature(agent.Features, core.AgentFeatureProcessTimeZone) {
		_ = s.store.SetAgentTimeZoneStatus(request.Context(), agentID, state.Revision, "unsupported", "请升级 Agent 后再应用时区")
		state.Status, state.Error = "unsupported", "请升级 Agent 后再应用时区"
	} else if state.TimeZone != "" && (state.Status == "pending" || input.Retry) {
		afterRequestCommit(request.Context(), func() { s.refreshAgentTimeZone(agentID) })
	}
	if mode == "auto" && state.Status == "failed" {
		s.recordAudit(request, "agent.time_zone.updated", agentID, "automatic process time zone lookup failed")
	} else {
		s.recordAudit(request, "agent.time_zone.updated", agentID, mode+" process time zone: "+state.TimeZone)
	}
	state.AutoAddressHash = ""
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) resolveAgentTimeZone(ctx context.Context, agentID string, force bool) (core.AgentTimeZoneState, error) {
	agent, err := s.store.GetAgentTimeZoneSource(ctx, agentID)
	if err != nil {
		return core.AgentTimeZoneState{}, err
	}
	state := agent.TimeZone
	if state.Mode != "auto" {
		return state, nil
	}
	address := agentTimeZoneAddress(agent)
	addressHash := timeZoneAddressHash(address)
	if !force && addressHash == state.AutoAddressHash {
		return state, nil
	}
	if !address.IsValid() {
		return s.store.UpdateAgentTimeZoneResolution(ctx, agentID, state.Revision, state.AutoAddressHash, "", addressHash, errors.New("no trusted public address"))
	}
	lookupContext, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	zone, lookupErr := s.geoip.LookupTimeZone(lookupContext, address, force)
	updated, updateErr := s.store.UpdateAgentTimeZoneResolution(ctx, agentID, state.Revision, state.AutoAddressHash, zone, addressHash, lookupErr)
	if updateErr != nil {
		return updated, updateErr
	}
	if updated.Revision != state.Revision || updated.TimeZone != state.TimeZone {
		afterRequestCommit(ctx, func() { s.refreshAgentTimeZone(agentID) })
	}
	return updated, nil
}

func (s *Server) handleAgentTimeZoneResult(ctx context.Context, agentID string, result core.AgentTimeZoneResult) {
	if result.Revision < 1 || !core.ValidTimeZone(result.TimeZone) {
		return
	}
	if _, err := s.store.AcknowledgeAgentTimeZone(ctx, agentID, result); err != nil {
		return
	}
}
