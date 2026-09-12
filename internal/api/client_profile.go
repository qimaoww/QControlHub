package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type clientProfileSelector struct {
	Engine core.Engine `json:"engine"`
	Tag    string      `json:"tag"`
	Port   int         `json:"port"`
}

// Resolve only the actually deployed revision, not a pending server draft or
// an arbitrary client-supplied label key. Ambiguous/stale selections fail closed.
func (s *Server) clientProfileNameLabel(ctx context.Context, agentID string, selected clientProfileSelector) (store.ClientProfileScope, error) {
	if !selected.Engine.Valid() || strings.TrimSpace(selected.Tag) == "" || selected.Port < 1 || selected.Port > 65535 {
		return store.ClientProfileScope{}, fmt.Errorf("%w: profile requires engine, tag, and port", store.ErrInvalid)
	}
	deployments, err := s.store.LatestDeployments(ctx)
	if err != nil {
		return store.ClientProfileScope{}, err
	}
	for _, deployment := range deployments {
		if deployment.AgentID != agentID || deployment.Engine != selected.Engine {
			continue
		}
		config, err := s.store.ConfigRevision(ctx, deployment.ConfigID, deployment.ConfigVersion)
		if err != nil {
			return store.ClientProfileScope{}, err
		}
		label := ""
		for _, input := range serverconfig.ParseAll(selected.Engine, config.Content) {
			if input.Tag == selected.Tag && input.Port == selected.Port {
				if label != "" {
					return store.ClientProfileScope{}, fmt.Errorf("%w: ambiguous deployed profile", store.ErrConflict)
				}
				label = core.ClientProfileNameLabel(selected.Engine, input.Listen, input.Port)
			}
		}
		if label != "" {
			return store.ClientProfileScope{ConfigID: config.ID, Version: config.Version, Label: label}, nil
		}
	}
	return store.ClientProfileScope{}, fmt.Errorf("%w: deployed profile changed; refresh before saving", store.ErrNotFound)
}
