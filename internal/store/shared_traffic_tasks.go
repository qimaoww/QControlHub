package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func (s *Store) prepareSharedTaskTx(ctx context.Context, tx pgx.Tx, task *core.Task, configOwner string, features []string, runtime core.RuntimeState) error {
	var isolated, disabled bool
	err := tx.QueryRow(ctx, `SELECT u.role='user' AND a.owner_id<>u.id,u.disabled
		FROM panel_users u JOIN agents a ON a.id=$2 WHERE u.id=$1`, configOwner, task.AgentID).Scan(&isolated, &disabled)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if disabled {
		return fmt.Errorf("%w: configuration owner is disabled", ErrConflict)
	}
	if isolated && task.Action == core.ActionImportExisting {
		return fmt.Errorf("%w: stop the existing service and deploy the isolated user's configuration instead of importing it", ErrConflict)
	}
	if task.Action == core.ActionDeploy {
		var owner string
		var running, uncertain, ownerIsolated bool
		err := tx.QueryRow(ctx, `SELECT state.owner_id,state.running,state.uncertain,COALESCE(u.role='user' AND a.owner_id<>u.id,false)
			FROM agent_engine_ownership state JOIN agents a ON a.id=state.agent_id LEFT JOIN panel_users u ON u.id=state.owner_id
			WHERE state.agent_id=$1 AND state.engine=$2`, task.AgentID, task.Engine).Scan(&owner, &running, &uncertain, &ownerIsolated)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		untrackedActive := strings.EqualFold(runtime.ServiceStatus, "active") || strings.EqualFold(runtime.ServiceStatus, "running") || runtime.ExistingConfigAvailable
		independentInstance := isolated && containsFeature(features, core.AgentFeatureSharedCoreInstances)
		if independentInstance && err == nil && owner == configOwner && (running || uncertain) {
			return fmt.Errorf("%w: stop the legacy shared core before deploying its private instance", ErrConflict)
		}
		if (err == nil && (running || uncertain) && owner != configOwner && (isolated || ownerIsolated)) ||
			(isolated && errors.Is(err, pgx.ErrNoRows) && untrackedActive) {
			if independentInstance && err == nil && !uncertain {
				// A known base service keeps running while this share uses its own
				// instance. Port reservations below still prevent overlap.
			} else {
				return fmt.Errorf("%w: this core belongs to another or untracked deployment; an administrator must stop it before reassignment", ErrConflict)
			}
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t LEFT JOIN configs c ON c.id=t.config_id
			LEFT JOIN panel_users u ON u.id=COALESCE(c.owner_id,t.owner_id) JOIN agents a ON a.id=t.agent_id
			WHERE t.agent_id=$1 AND t.engine=$2 AND t.status IN ('pending','running')
			AND t.action NOT IN ('validate','status') AND COALESCE(c.owner_id,t.owner_id)<>$3
			AND ($4::boolean OR COALESCE(u.role='user' AND a.owner_id<>u.id,false)))`,
			task.AgentID, task.Engine, configOwner, isolated).Scan(&busy); err != nil {
			return err
		}
		if busy && !independentInstance {
			return fmt.Errorf("%w: this core has another user's pending or running task", ErrConflict)
		}
	}
	endpoints := serverconfig.DiscoverTrafficPorts(task.Engine, task.ConfigContent)
	if isolated {
		if !supportsSharedEngines(features) {
			return fmt.Errorf("%w: upgrade the Agent before shared deployments", ErrConflict)
		}
		endpoints, err = serverconfig.SharedTrafficEndpoints(task.Engine, task.ConfigContent)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		var limit, used uint64
		var unrestricted bool
		if err := tx.QueryRow(ctx, `SELECT id,limit_bytes,used_bytes,ports_unrestricted FROM agent_shares
			WHERE user_id=$1 AND agent_id=$2 AND enabled AND status='accepted' AND $3=ANY(engines)`,
			configOwner, task.AgentID, task.Engine).Scan(&task.SharedTrafficID, &limit, &used, &unrestricted); err != nil {
			return mapError(err)
		}
		task.SharedInstance = task.Action == core.ActionDeploy && containsFeature(features, core.AgentFeatureSharedCoreInstances)
		if limit > 0 && used >= limit {
			return fmt.Errorf("%w: the user's cumulative Agent traffic allowance is exhausted", ErrConflict)
		}
		if unrestricted {
			if task.Action == core.ActionDeploy {
				if err := s.reserveSharedDeploymentPortsTx(ctx, tx, task.SharedTrafficID, configOwner, task.AgentID, endpoints); err != nil {
					return err
				}
			} else {
				ports := make([]int, 0, len(endpoints))
				for _, endpoint := range endpoints {
					ports = append(ports, endpoint.Port)
				}
				return s.checkSharedPortsAvailableTx(ctx, tx, task.SharedTrafficID, configOwner, task.AgentID, ports)
			}
		} else {
			for _, endpoint := range endpoints {
				var reserved bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_share_ports
					WHERE share_id=$1 AND agent_id=$2 AND port=$3)`, task.SharedTrafficID, task.AgentID, endpoint.Port).Scan(&reserved); err != nil {
					return err
				}
				if !reserved {
					return fmt.Errorf("%w: port %d is not allocated to this user", ErrConflict, endpoint.Port)
				}
			}
		}
		if task.Action == core.ActionDeploy {
			return bindSharedTrafficPortsTx(ctx, tx, task.SharedTrafficID, task.AgentID, endpoints)
		}
		return nil
	}
	// An unrestricted operator must not accidentally deploy over another
	// user's reserved ports. Administrators can explicitly release/reassign.
	for _, endpoint := range endpoints {
		var foreign bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_share_ports reserved JOIN agent_shares share ON share.id=reserved.share_id
			WHERE reserved.agent_id=$1 AND reserved.port=$2 AND share.user_id<>$3)`, task.AgentID, endpoint.Port, configOwner).Scan(&foreign); err != nil {
			return err
		}
		if foreign {
			return fmt.Errorf("%w: port %d is reserved for another user", ErrConflict, endpoint.Port)
		}
	}
	return nil
}

// Claiming a lifecycle task makes its outcome uncertain until acknowledgement.
// A failed/lost result must not authorize another user to take over a core.
func markEngineExecutionTx(ctx context.Context, tx pgx.Tx, task core.Task) error {
	switch task.Action {
	case core.ActionDeploy, core.ActionImportExisting, core.ActionStart, core.ActionRestart, core.ActionStop:
		if task.Action == core.ActionDeploy && task.SharedInstance {
			_, err := tx.Exec(ctx, `INSERT INTO agent_shared_instance_ownership
				(agent_id,engine,share_id,owner_id,config_id,config_version,running,uncertain,config_uncertain,updated_at)
				SELECT t.agent_id,t.engine,t.shared_traffic_id,COALESCE(c.owner_id,t.owner_id),t.config_id,t.config_version,
					true,true,true,now() FROM tasks t LEFT JOIN configs c ON c.id=t.config_id WHERE t.id=$1
				ON CONFLICT(agent_id,engine,share_id) DO UPDATE SET uncertain=true,config_uncertain=true,
					traffic_settled=false,updated_at=now()`, task.ID)
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO agent_engine_ownership
			(agent_id,engine,owner_id,config_id,config_version,running,uncertain,config_uncertain,traffic_settled,updated_at)
			SELECT t.agent_id,t.engine,COALESCE(c.owner_id,t.owner_id),COALESCE(t.config_id,''),COALESCE(t.config_version,0),
				true,true,t.action IN ('deploy','import-existing'),false,now()
			FROM tasks t LEFT JOIN configs c ON c.id=t.config_id WHERE t.id=$1
			ON CONFLICT(agent_id,engine) DO UPDATE SET uncertain=true,
				config_uncertain=agent_engine_ownership.config_uncertain OR EXCLUDED.config_uncertain,
				traffic_settled=false,updated_at=now()`, task.ID)
		return err
	}
	return nil
}

func recordEngineOwnershipTx(ctx context.Context, tx pgx.Tx, taskID string, action core.Action, settled bool) error {
	switch action {
	case core.ActionDeploy, core.ActionImportExisting:
		var sharedInstance bool
		if err := tx.QueryRow(ctx, `SELECT shared_instance FROM tasks WHERE id=$1`, taskID).Scan(&sharedInstance); err != nil {
			return err
		}
		if sharedInstance {
			_, err := tx.Exec(ctx, `INSERT INTO agent_shared_instance_ownership
				(agent_id,engine,share_id,owner_id,config_id,config_version,running,updated_at)
				SELECT t.agent_id,t.engine,t.shared_traffic_id,COALESCE(c.owner_id,t.owner_id),t.config_id,t.config_version,true,now()
				FROM tasks t LEFT JOIN configs c ON c.id=t.config_id WHERE t.id=$1
				ON CONFLICT(agent_id,engine,share_id) DO UPDATE SET owner_id=EXCLUDED.owner_id,
					config_id=EXCLUDED.config_id,config_version=EXCLUDED.config_version,running=true,
					uncertain=false,config_uncertain=false,traffic_settled=false,updated_at=now()`, taskID)
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO agent_engine_ownership(agent_id,engine,owner_id,config_id,config_version,running,updated_at)
			SELECT t.agent_id,t.engine,COALESCE(c.owner_id,t.owner_id),t.config_id,t.config_version,true,now()
			FROM tasks t LEFT JOIN configs c ON c.id=t.config_id WHERE t.id=$1
			ON CONFLICT(agent_id,engine) DO UPDATE SET owner_id=EXCLUDED.owner_id,config_id=EXCLUDED.config_id,
				config_version=EXCLUDED.config_version,running=true,uncertain=false,config_uncertain=false,traffic_settled=false,updated_at=now()`, taskID)
		return err
	case core.ActionStop, core.ActionStart, core.ActionRestart:
		_, err := tx.Exec(ctx, `INSERT INTO agent_engine_ownership(agent_id,engine,owner_id,config_id,config_version,running,updated_at)
			SELECT agent_id,engine,'','',0,$2,now() FROM tasks WHERE id=$1
			ON CONFLICT(agent_id,engine) DO UPDATE SET running=$2,uncertain=false,traffic_settled=$3,updated_at=now()`, taskID, action != core.ActionStop, action == core.ActionStop && settled)
		return err
	}
	return nil
}
