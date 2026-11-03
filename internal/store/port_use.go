package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// The caller holds the Agent row lock. Task snapshots protect the interval
// before a service starts; dispatch/reconnect check the same effective ports.
func (s *Store) checkTaskPortUseTx(ctx context.Context, tx pgx.Tx, task *core.Task, readOnly bool) error {
	switch task.Action {
	case core.ActionDeploy, core.ActionValidate, core.ActionImportExisting, core.ActionStart, core.ActionRestart:
	default:
		return nil
	}
	owner := scopeForConfig(ctx).OwnerID
	if task.ConfigID != "" {
		if err := tx.QueryRow(ctx, `SELECT owner_id FROM configs WHERE id=$1`, task.ConfigID).Scan(&owner); err != nil {
			return err
		}
	}
	if task.ConfigID == "" && task.ID != "" {
		if err := tx.QueryRow(ctx, `SELECT owner_id FROM tasks WHERE id=$1`, task.ID).Scan(&owner); err != nil {
			return err
		}
	}
	if task.ConfigContent == "" {
		if task.Action != core.ActionStart && task.Action != core.ActionRestart {
			return nil
		}
		var competing bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_shared_instance_ownership
			WHERE agent_id=$1 AND owner_id<>$2 AND (running OR uncertain))
			OR EXISTS(SELECT 1 FROM tasks t LEFT JOIN configs c ON c.id=t.config_id
				WHERE t.agent_id=$1 AND t.id<>$3 AND COALESCE(c.owner_id,t.owner_id)<>$2
				AND t.status IN ('pending','running') AND t.action IN ('deploy','import-existing','start','restart'))`,
			task.AgentID, owner, task.ID).Scan(&competing); err != nil {
			return err
		}
		if competing {
			return fmt.Errorf("%w: 无法确认待启动服务的监听端口，请先部署已校验配置", ErrConflict)
		}
		return nil
	}
	var ports []int
	for _, endpoint := range serverconfig.DiscoverTrafficPorts(task.Engine, task.ConfigContent) {
		ports = append(ports, endpoint.Port)
	}
	if err := s.checkSharedPortsAvailableTx(ctx, tx, task.SharedTrafficID, owner, task.AgentID, ports, task); err != nil {
		return err
	}
	// Public proxy ports are checked independently of the metered core backend.
	check := *task
	if readOnly {
		check.Action = core.ActionValidate
	}
	if err := s.prepareTLSFrontendPortsTx(ctx, tx, &check, owner); err != nil {
		return err
	}
	for _, port := range ports {
		var shareID string
		var engine core.Engine
		err := tx.QueryRow(ctx, `SELECT COALESCE(share_id,''),engine FROM port_traffic_policies WHERE agent_id=$1 AND port=$2 FOR UPDATE`, task.AgentID, port).Scan(&shareID, &engine)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if shareID != "" && shareID != task.SharedTrafficID {
			if err := retireUnusedSharedPolicyTx(ctx, tx, task.AgentID, port, shareID, engine, readOnly || task.Action == core.ActionValidate); err != nil {
				return err
			}
		}
	}
	return nil
}

// Starting the base service also claims its retained listeners. A saved draft
// must not replace the last deployed revision when protecting a queued start.
func (s *Store) prepareRetainedHostPortsTx(ctx context.Context, tx pgx.Tx, task *core.Task) error {
	if task.SharedInstance || (task.Action != core.ActionStart && task.Action != core.ActionRestart) {
		return nil
	}
	var content string
	err := tx.QueryRow(ctx, `SELECT state.config_id,state.config_version,COALESCE(revision.content,config.content,'')
  FROM agent_engine_ownership state JOIN configs config ON config.id=state.config_id
  LEFT JOIN config_revisions revision ON revision.config_id=state.config_id AND revision.version=state.config_version
  WHERE state.agent_id=$1 AND state.engine=$2 AND NOT state.config_uncertain
  AND (revision.config_id IS NOT NULL OR config.version=state.config_version)`, task.AgentID, task.Engine).Scan(&task.ConfigID, &task.ConfigVersion, &content)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	task.ConfigContent, err = s.decryptContent(content)
	if err != nil {
		return err
	}
	task.TLSFrontends, err = s.managedTLSFrontendsTx(ctx, tx, task.ConfigID, task.ConfigVersion, task.Engine, task.ConfigContent)
	return err
}
