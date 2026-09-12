package store

import (
	"context"
	"encoding/json"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// DeployedConfig contains only the exact version currently deployed, plus its
// version-bound client secrets. It is an internal snapshot, never a JSON API.
type DeployedConfig struct {
	Deployment  core.Deployment
	Config      core.Config
	Metadata    map[string]string
	Preferences map[string]string
}

// DeployedConfigs resolves current/historical bodies and client metadata in a
// single query instead of downloading all saved bodies and doing two further
// reads per deployment. Deleted configs and pruned revisions remain hidden.
func (s *Store) DeployedConfigs(ctx context.Context) ([]DeployedConfig, error) {
	args := []any{}
	ownerWhere := ownerClause(ctx, "config.owner_id", &args)
	ownerWhere += agentEngineAccessClause(ctx, "latest.agent_id", "latest.engine", &args)
	rows, err := s.pool.Query(ctx, deployedConfigsSQL+ownerWhere, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]DeployedConfig, 0)
	for rows.Next() {
		var item DeployedConfig
		var metadata, preferences []byte
		if err := rows.Scan(&item.Deployment.AgentID, &item.Deployment.Engine, &item.Deployment.ConfigID,
			&item.Deployment.ConfigVersion, &item.Deployment.DeployedAt, &item.Config.Content, &metadata, &item.Config.OwnerID, &preferences); err != nil {
			return nil, err
		}
		item.Config.ID = item.Deployment.ConfigID
		item.Config.Version = item.Deployment.ConfigVersion
		item.Config.Engine = item.Deployment.Engine
		item.Config.Content, err = s.decryptContent(item.Config.Content)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metadata, &item.Metadata); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(preferences, &item.Preferences); err != nil {
			return nil, err
		}
		for tag, ciphertext := range item.Metadata {
			item.Metadata[tag], err = s.decryptContent(ciphertext)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// Materialize only the small latest-service projection. Without this boundary
// the planner can pull config/revision joins into the per-agent lateral loop
// and rescan their entire tables for every node, especially on local databases.
const deployedConfigsSQL = `
	WITH latest AS MATERIALIZED (` + latestDeploymentsSQL + `)
	SELECT latest.agent_id,latest.engine,latest.config_id,latest.config_version,latest.finished_at,
	       CASE WHEN config.version=latest.config_version THEN config.content ELSE revision.content END,
	       COALESCE((SELECT jsonb_object_agg(profile_tag,content) FROM config_client_metadata
		         WHERE config_id=latest.config_id AND config_version=latest.config_version),'{}'::jsonb),
	       config.owner_id,
	       COALESCE((SELECT jsonb_object_agg(label,value) FROM config_client_preferences
		         WHERE config_id=latest.config_id),'{}'::jsonb)
	FROM latest JOIN agents agent ON agent.id=latest.agent_id AND agent.revoked_at IS NULL
	JOIN configs config ON config.id=latest.config_id AND config.deleted_at IS NULL
	LEFT JOIN config_revisions revision ON revision.config_id=latest.config_id
	     AND revision.version=latest.config_version AND config.version<>latest.config_version
	WHERE (config.version=latest.config_version OR revision.config_id IS NOT NULL)`
