package webui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type liveConfigPageData struct {
	Agent            core.Agent
	Engine           core.Engine
	Runtime          core.RuntimeState
	InstalledEngines []core.Engine
	Config           core.Config
	HasSavedConfig   bool
	SourceLoaded     bool
	SourceTaskID     string
	Draft            bool
	ReturnTo         string
}

func (s *Server) buildLiveConfigPage(ctx context.Context, request *http.Request, agents []core.Agent) (*liveConfigPageData, string, error) {
	agent, engine, found := selectLiveConfigTarget(agents, request.URL.Query().Get("node"), request.URL.Query().Get("engine"))
	if !found {
		return nil, "", nil
	}
	config, err := s.store.AgentConfig(ctx, agent.ID, engine)
	hasSavedConfig := err == nil
	if errors.Is(err, store.ErrNotFound) {
		config = defaultAgentConfig(agent, engine)
	} else if err != nil {
		return nil, "", err
	}
	storedContent := config.Content
	config.Content = ""
	page := &liveConfigPageData{
		Agent: agent, Engine: engine, Runtime: agent.Runtime[engine], InstalledEngines: installedConfigEngines(agent),
		Config: config, HasSavedConfig: hasSavedConfig,
		ReturnTo: "/configs?node=" + url.QueryEscape(agent.ID) + "&engine=" + url.QueryEscape(string(engine)),
	}
	sourceTaskID := strings.TrimSpace(request.URL.Query().Get("source_task"))
	if sourceTaskID != "" {
		content, sourceErr := s.store.ReadTaskConfigSnapshot(ctx, sourceTaskID, agent.ID, engine)
		if errors.Is(sourceErr, store.ErrNotFound) {
			return page, "当前配置尚未读取完成，或读取结果已失效；请重新打开配置页", nil
		}
		if sourceErr != nil {
			return nil, "", sourceErr
		}
		page.Config.Content = content
		page.SourceLoaded = true
		page.SourceTaskID = sourceTaskID
		return page, "", nil
	}
	if rawVersion := strings.TrimSpace(request.URL.Query().Get("draft_version")); rawVersion != "" {
		version, parseErr := strconv.Atoi(rawVersion)
		if parseErr != nil || !hasSavedConfig || version != config.Version {
			return page, "保存的修改版本已变化，请重新读取节点当前配置", nil
		}
		page.Config.Content = storedContent
		page.SourceLoaded = true
		page.Draft = true
	}
	return page, "", nil
}

func selectLiveConfigTarget(agents []core.Agent, requestedAgent, requestedEngine string) (core.Agent, core.Engine, bool) {
	selectEngine := func(agent core.Agent) (core.Engine, bool) {
		engines := installedConfigEngines(agent)
		if len(engines) == 0 {
			return "", false
		}
		if preferred, err := core.ParseEngine(strings.TrimSpace(requestedEngine)); err == nil {
			for _, engine := range engines {
				if engine == preferred {
					return engine, true
				}
			}
		}
		return engines[0], true
	}
	if requestedAgent = strings.TrimSpace(requestedAgent); requestedAgent != "" {
		for _, agent := range agents {
			if agent.ID == requestedAgent {
				engine, ok := selectEngine(agent)
				return agent, engine, ok
			}
		}
	}
	for _, requireOnline := range []bool{true, false} {
		for _, agent := range agents {
			if requireOnline != (agent.Status == "online") {
				continue
			}
			if engine, ok := selectEngine(agent); ok {
				return agent, engine, true
			}
		}
	}
	return core.Agent{}, "", false
}
