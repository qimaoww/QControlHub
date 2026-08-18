package webui

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// buildConfigsPage fills the config archive page fields: the selected config
// (or a blank new one), online deployable agents, revision history, an
// optional revision preview and the reusable configuration templates.
func (s *Server) buildConfigsPage(ctx context.Context, request *http.Request, agents []core.Agent, configs []core.Config, override *core.Config, data *pageData) (pageError string, err error) {
	data.FormConfig, data.IsNewConfig = configForPage(request, configs, override)
	for _, agent := range agents {
		if agent.Status == "online" && agentSupports(agent, data.FormConfig.Engine) {
			data.DeployAgents = append(data.DeployAgents, agent)
		}
	}
	data.Templates, err = s.store.ListConfigTemplates(ctx)
	if err != nil {
		return "", err
	}
	if data.IsNewConfig {
		return "", nil
	}
	data.ConfigRevisions, err = s.store.ListConfigRevisions(ctx, data.FormConfig.ID, 50)
	if err != nil {
		return "", err
	}
	if rawRevision := request.URL.Query().Get("revision"); rawRevision != "" {
		version, parseErr := strconv.Atoi(rawRevision)
		if parseErr != nil || version < 1 {
			return "修订号无效", nil
		}
		preview, revisionErr := s.store.ConfigRevision(ctx, data.FormConfig.ID, version)
		if revisionErr != nil {
			if !errors.Is(revisionErr, store.ErrNotFound) {
				return "", revisionErr
			}
			return "找不到指定配置修订", nil
		}
		data.RevisionPreview, data.HasRevisionPreview = preview, true
	}
	return "", nil
}
