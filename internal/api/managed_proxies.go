package api

import (
	"net/http"
	"slices"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type nodeProxyReport struct {
	AgentID   string           `json:"agent_id"`
	Status    string           `json:"status"`
	Supported bool             `json:"supported"`
	Nginx     core.ProxyStatus `json:"nginx"`
	Caddy     core.ProxyStatus `json:"caddy"`
}

func (s *Server) getManagedProxies(w http.ResponseWriter, r *http.Request) {
	agent, err := s.store.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !agent.CanManage {
		writeError(w, http.StatusForbidden, "当前账号无权查看此节点的托管代理")
		return
	}
	response := nodeProxyReport{AgentID: agent.ID, Status: agent.Status, Supported: slices.Contains(agent.Features, core.AgentFeatureManagedProxies), Nginx: agent.ManagedProxies["nginx"], Caddy: agent.ManagedProxies["caddy"]}
	if response.Nginx.State == "" {
		response.Nginx.State = "unknown"
	}
	if response.Caddy.State == "" {
		response.Caddy.State = "unknown"
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) checkManagedProxy(w http.ResponseWriter, r *http.Request) {
	s.queueManagedProxy(w, r, "check")
}

func (s *Server) updateManagedProxy(w http.ResponseWriter, r *http.Request) {
	s.queueManagedProxy(w, r, "update")
}

func (s *Server) queueManagedProxy(w http.ResponseWriter, r *http.Request, operation string) {
	action := core.Action(operation + "-" + r.PathValue("component"))
	if action.ProxyComponent() == "" {
		writeError(w, http.StatusBadRequest, "未知代理")
		return
	}
	id := r.PathValue("id")
	task, err := s.store.CreateTask(r.Context(), core.TaskRequest{Action: action, AgentID: id})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !task.Reused {
		s.recordAudit(r, "task.created", task.ID, string(action)+" "+id)
	}
	writeJSON(w, http.StatusAccepted, task)
}
