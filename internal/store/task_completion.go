package store

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func (s *Store) CompleteTask(ctx context.Context, agentID, taskID string, result core.TaskResultRequest, archives ...core.IPQualityArchive) error {
	if len(result.LeaseID) < 32 {
		return fmt.Errorf("%w: invalid task lease", ErrConflict)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Match creation/claim lock order: node first, then task. A successful
	// transition updates eligibility atomically with the task acknowledgement.
	var selected, supported []core.Engine
	var features []string
	if err := tx.QueryRow(ctx, `SELECT capabilities,COALESCE(supported_capabilities,capabilities),features FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&selected, &supported, &features); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var action core.Action
	var engine core.Engine
	var transition, installIfMissing, shared bool
	var shareID, configID string
	var configVersion int
	if err := tx.QueryRow(ctx, `
		SELECT action,engine,capability_transition,install_if_missing,shared_instance,shared_traffic_id,COALESCE(config_id,''),COALESCE(config_version,0) FROM tasks
		WHERE id=$1 AND agent_id=$2 AND lease_id=$3 AND status='running'
		FOR UPDATE`, taskID, agentID, result.LeaseID).Scan(&action, &engine, &transition, &installIfMissing, &shared, &shareID, &configID, &configVersion); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1 AND agent_id=$2)`, taskID, agentID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return fmt.Errorf("%w: task is not running", ErrConflict)
	}
	// Also cover tasks dispatched before this server version was deployed.
	// Failed or unauthorized results cannot prove the host stayed unchanged.
	if err := invalidateConfigReadSnapshotsTx(ctx, tx, core.Task{
		AgentID: agentID, Engine: engine, Action: action, InstallIfMissing: installIfMissing, SharedInstance: shared, SharedTrafficID: shareID,
	}); err != nil {
		return err
	}
	var unauthorized bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t WHERE t.id=$1
		AND ((`+unauthorizedTaskPrincipalSQL+`) OR (`+unauthorizedHostConfigTaskSQL+`)
		OR (t.shared_instance AND t.action IN ('start','stop','restart','status','read-config','read-managed-config') AND NOT $2::boolean)
		OR (t.shared_traffic_id<>'' AND NOT EXISTS(SELECT 1 FROM agent_shares sh JOIN panel_users u ON u.id=sh.user_id
			WHERE sh.id=t.shared_traffic_id AND sh.agent_id=t.agent_id AND sh.enabled AND sh.status='accepted' AND NOT u.disabled
			AND t.engine=ANY(sh.engines) AND (t.action IN ('stop','status','read-config','read-managed-config') OR sh.limit_bytes=0 OR sh.used_bytes<sh.limit_bytes)))))`,
		taskID, containsFeature(features, core.AgentFeatureSharedCoreLifecycle)).Scan(&unauthorized); err != nil {
		return err
	}
	if unauthorized {
		// Never retain an unauthorized read, including partial content in an
		// error. A later owner takeover must not make this result readable.
		if _, err := tx.Exec(ctx, `UPDATE tasks SET status='failed',output=NULL,
			error='task authorization changed; previous execution is unknown',
			finished_at=now(),config_content=NULL,lease_id=NULL WHERE id=$1`, taskID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	versioned := containsFeature(features, core.AgentFeatureSharedCoreLifecycle)
	if shared && (action.ChangesInstance() || action == core.ActionStatus) {
		observation := result.InstanceStatus
		if observation != nil && (validateSharedInstanceStatus(*observation) != nil || observation.Engine != engine || observation.ShareID != shareID ||
			action.ChangesInstance() && observation.TaskID != taskID) {
			return fmt.Errorf("%w: instance result does not match its leased target", ErrInvalid)
		}
		emptyDeploymentStopped := false
		if action == core.ActionDeploy && observation != nil && observation.Status == "inactive" {
			var content string
			if err := tx.QueryRow(ctx, `SELECT COALESCE(config_content,'') FROM tasks WHERE id=$1`, taskID).Scan(&content); err != nil {
				return err
			}
			content, err = s.decryptContent(content)
			if err != nil {
				return err
			}
			content, _, err = core.DecodeExecutionSnapshot(content)
			if err != nil {
				return err
			}
			endpoints, parseErr := serverconfig.SharedTrafficEndpoints(engine, content)
			emptyDeploymentStopped = parseErr == nil && len(endpoints) == 0
		}
		if result.Success && versioned && (observation == nil || action.ChangesInstance() &&
			(action == core.ActionStop && observation.Status != "inactive" || action != core.ActionStop &&
				(observation.Status != "active" && !emptyDeploymentStopped || observation.ConfigID != configID || observation.ConfigVersion != configVersion))) {
			result.Success, result.Error = false, "目标实例的配置版本或启动检查未通过，请查看实例状态及日志"
		}
	}
	if err := s.completeCertificateBindingsTx(ctx, tx, taskID, result.Success, result.Error); err != nil {
		return err
	}

	status := core.TaskFailed
	if result.Success {
		status = core.TaskSucceeded
	}
	// Re-enrollment can narrow declared support while an offline transition
	// is queued. Never restore a capability outside that current support set.
	if transition && result.Success && action == core.ActionStart && !containsEngine(supported, engine) {
		status = core.TaskFailed
		result.Error = "Agent no longer declares support for this engine; capability was not enabled"
	}
	if transition && status == core.TaskSucceeded {
		if err := setEngineCapability(ctx, tx, agentID, engine, action == core.ActionStart, selected); err != nil {
			return err
		}
	}
	storedContent := ""
	storedOutput := truncate(result.Output, 64<<10)
	storedError := truncate(result.Error, 8<<10)
	if action == core.ActionIPQuality && result.Success {
		report, validationErr := core.NormalizeIPQualityResult(result.IPQuality)
		if validationErr == nil {
			validationErr = saveIPQualityResultTx(ctx, tx, taskID, report, archives)
			if validationErr != nil && !errors.Is(validationErr, ErrInvalid) {
				return validationErr
			}
		}
		if validationErr != nil {
			status = core.TaskFailed
			storedOutput = ""
			storedError = "Agent returned an invalid IPQuality report: " + validationErr.Error()
		} else {
			storedOutput = "IPQuality report saved"
			storedError = ""
		}
	}
	if (action == core.ActionReadConfig || action == core.ActionReadManagedConfig) && result.Success {
		content := result.Output
		if !utf8.ValidString(content) {
			status = core.TaskFailed
			storedOutput = ""
			storedError = "agent returned a current configuration that is not valid UTF-8"
		} else if len(content) > core.MaxConfigBytes {
			status = core.TaskFailed
			storedOutput = ""
			storedError = "agent returned a current configuration larger than the supported limit"
		} else if validationErr := core.ValidateConfig(engine, content); validationErr != nil {
			status = core.TaskFailed
			storedOutput = ""
			storedError = "agent returned an invalid current configuration: " + validationErr.Error()
		} else {
			storedContent = content
			storedOutput = "current configuration read and validated"
			storedError = ""
		}
	}
	storedContent, err = s.encryptContent(storedContent)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE tasks SET status=$4,output=$5,error=$6,finished_at=now(),config_content=NULLIF($7,''),lease_id=NULL
		WHERE id=$1 AND agent_id=$2 AND lease_id=$3 AND status='running'`,
		taskID, agentID, result.LeaseID, status, storedOutput, storedError, storedContent)
	if err != nil {
		return err
	}
	if (action == core.ActionReadConfig || action == core.ActionReadManagedConfig) && status == core.TaskSucceeded {
		if _, err := tx.Exec(ctx, `
			UPDATE tasks SET config_content=NULL
			WHERE agent_id=$1 AND engine=$2 AND action=$3 AND id<>$4 AND config_content IS NOT NULL
			AND shared_instance=$5 AND shared_traffic_id=$6`,
			agentID, engine, action, taskID, shared, shareID); err != nil {
			return err
		}
	}
	if status == core.TaskSucceeded && !(shared && result.InstanceStatus != nil) {
		if err := recordEngineOwnershipTx(ctx, tx, taskID, action, result.TrafficSettled); err != nil {
			return err
		}
	}
	if shared && result.InstanceStatus != nil {
		if err := recordSharedInstanceObservationTx(ctx, tx, agentID, *result.InstanceStatus, versioned, time.Now().UTC(), result.TrafficSettled, nil); err != nil {
			return err
		}
	} else if shared && status == core.TaskFailed && action.ChangesInstance() {
		if _, err := tx.Exec(ctx, `UPDATE agent_shared_instance_ownership state SET service_status='unknown',status_reason=$2
			FROM tasks t WHERE t.id=$1 AND state.agent_id=t.agent_id AND state.engine=t.engine AND state.share_id=t.shared_traffic_id
			AND state.last_task_id=t.id`, taskID, storedError); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
