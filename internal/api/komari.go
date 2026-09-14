package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/komari"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func komariNodeResource(node komari.Node) core.KomariNode {
	return core.KomariNode{
		UUID: node.UUID, Name: node.Name, BillingCycle: node.BillingCycle,
		TrafficLimit: node.TrafficLimit, TrafficLimitType: node.TrafficLimitType,
		EffectiveTrafficLimit: node.EffectiveTrafficLimit, EffectiveTrafficType: node.EffectiveTrafficType,
		EffectiveTrafficLimitAvailable: node.EffectiveTrafficLimitSet,
		EffectiveTrafficTypeAvailable:  node.EffectiveTrafficTypeSet,
		TrafficResetDay:                node.TrafficResetDay,
		TrafficUsed:                    node.TrafficUsed,
		TrafficUsedAvailable:           node.TrafficUsedSet,
		ExpiredAt:                      node.ExpiredAt,
		UpdatedAt:                      node.UpdatedAt,
	}
}

func (s *Server) komariForRequest(ctx context.Context, allowEnvironment bool) (*komari.Client, error) {
	if s.store == nil {
		if s.komariConfigError != nil {
			return nil, s.komariConfigError
		}
		return s.komari, nil
	}
	settings, err := s.store.PanelSettings(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(settings.KomariURL) == "" {
		if !allowEnvironment {
			return nil, nil
		}
		return s.komari, s.komariConfigError
	}
	client, err := komari.New(settings.KomariURL, settings.KomariAPIKey, s.komariHTTPClient)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (s *Server) getAgentKomari(w http.ResponseWriter, request *http.Request) {
	agent, err := s.store.GetAgent(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	uuid := store.AgentKomariUUID(agent)
	result := core.KomariLink{UUID: uuid}
	if uuid == "" {
		writeJSON(w, http.StatusOK, result)
		return
	}
	komariClient, clientErr := s.komariForRequest(request.Context(), s.configOwnerID(request) == "")
	if clientErr != nil {
		writeError(w, http.StatusServiceUnavailable, clientErr.Error())
		return
	}
	if komariClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Komari integration is not configured")
		return
	}
	node, err := komariClient.GetNode(request.Context(), uuid)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	result.Server = ptr(komariNodeResource(node))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) putAgentKomari(w http.ResponseWriter, request *http.Request) {
	var input struct {
		UUID       *string `json:"uuid"`
		KomariUUID *string `json:"komari_uuid"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	value := input.UUID
	if value == nil {
		value = input.KomariUUID
	}
	if value == nil {
		writeError(w, http.StatusBadRequest, "uuid is required")
		return
	}
	uuid := strings.TrimSpace(*value)
	if len(uuid) > 100 || strings.ContainsAny(uuid, "\r\n\t") {
		writeError(w, http.StatusBadRequest, "Komari server UUID is invalid")
		return
	}
	if err := s.store.SetAgentKomariUUID(request.Context(), request.PathValue("id"), uuid); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "agent.komari.updated", request.PathValue("id"), "Komari UUID linked")
	writeJSON(w, http.StatusOK, core.KomariLink{UUID: uuid})
}
