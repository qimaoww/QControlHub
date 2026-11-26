package api

import (
	"context"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Old manual edits could save new listener identities alongside old generated
// routes. Use only this authorized workspace's revisions as repair evidence.
// The optimistic save still rejects a concurrent change after this read.
func (s *Server) presetAccountingSource(ctx context.Context, config core.Config) (string, error) {
	processMode, err := s.store.ConfigUsesProcessMetering(ctx, config.AgentID, config.OwnerID)
	if err != nil {
		return "", err
	}
	if processMode {
		return config.Content, nil
	}
	source, originalErr := serverconfig.PresetAccountingSource(config.Engine, config.Content)
	if originalErr == nil {
		return source, nil
	}
	return s.accountingUpdateSource(ctx, config, config.Content)
}

func (s *Server) accountingUpdateSource(ctx context.Context, config core.Config, content string) (string, error) {
	processMode, err := s.store.ConfigUsesProcessMetering(ctx, config.AgentID, config.OwnerID)
	if err != nil {
		return "", err
	}
	if processMode {
		return content, nil
	}
	// Explicitly removing all managed entries in the source editor is allowed.
	// The new configuration is still parsed and compiled before it can deploy.
	if config.Engine != core.EngineShadowsocksRust && !strings.Contains(content, "qch-trf-") {
		return content, nil
	}
	source, originalErr := serverconfig.AccountingUpdateSource(config.Engine, content, config.Content)
	if originalErr == nil {
		return source, nil
	}
	revisions, err := s.store.ConfigAccountingRevisions(ctx, config.ID)
	if err != nil {
		return "", err
	}
	for _, revision := range revisions {
		if revision.Engine != config.Engine || revision.AgentID != config.AgentID || revision.OwnerID != config.OwnerID || revision.Version >= config.Version {
			continue
		}
		if source, err := serverconfig.AccountingSourceFromRevision(config.Engine, content, revision.Content); err == nil {
			return source, nil
		}
	}
	return "", originalErr
}

func (s *Server) planWorkspaceAccounting(ctx context.Context, agentID string, engine core.Engine, content string) (serverconfig.AccountingPlan, error) {
	processMode, err := s.store.ConfigUsesProcessMetering(ctx, agentID, "")
	if err != nil {
		return serverconfig.AccountingPlan{}, err
	}
	if processMode {
		return serverconfig.SharedProcessPlan(engine, content)
	}
	return serverconfig.PlanPresetAccounting(engine, content)
}
