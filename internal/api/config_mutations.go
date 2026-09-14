package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Server) agentEngineFromPath(w http.ResponseWriter, request *http.Request) (core.Engine, core.Agent, bool) {
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", core.Agent{}, false
	}
	agent, err := s.store.GetAgent(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return "", core.Agent{}, false
	}
	if !agentSupportsEngine(agent, engine) {
		writeError(w, http.StatusBadRequest, "agent does not support the requested engine")
		return "", core.Agent{}, false
	}
	return engine, agent, true
}

func agentSupportsEngine(agent core.Agent, engine core.Engine) bool {
	for _, candidate := range agent.Capabilities {
		if candidate == engine {
			return true
		}
	}
	return false
}

func (s *Server) createConfigMutationTask(w http.ResponseWriter, request *http.Request, config core.Config, intent string) (core.Task, bool) {
	action := core.ActionValidate
	if intent == "deploy" {
		action = core.ActionDeploy
	} else if intent != "validate" {
		writeError(w, http.StatusBadRequest, "intent must be validate or deploy")
		return core.Task{}, false
	}
	task, err := s.store.CreateTask(request.Context(), core.TaskRequest{
		AgentID: config.AgentID, Engine: config.Engine, Action: action, ConfigID: config.ID, ExpectedConfigVersion: config.Version,
	})
	if err != nil {
		writeStoreError(w, err)
		return core.Task{}, false
	}
	task.ConfigContent = ""
	return task, true
}
