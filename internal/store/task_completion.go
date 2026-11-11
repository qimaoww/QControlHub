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
	agentErrorSummary := result.Error
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
	var shareID, configID, runtimeSnapshot string
	var configVersion int
	if err := tx.QueryRow(ctx, `
		SELECT action,engine,capability_transition,install_if_missing,shared_instance,shared_traffic_id,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(config_content,'') FROM tasks
		WHERE id=$1 AND agent_id=$2 AND lease_id=$3 AND status='running'
		FOR UPDATE`, taskID, agentID, result.LeaseID).Scan(&action, &engine, &transition, &installIfMissing, &shared, &shareID, &configID, &configVersion, &runtimeSnapshot); err != nil {
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
		if _, err := tx.Exec(ctx, `DELETE FROM task_diagnostic_uploads WHERE task_id=$1`, taskID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	versioned := containsFeature(features, core.AgentFeatureSharedCoreLifecycle)
	if !shared && result.InstanceStatus != nil && (action.ChangesInstance() || action == core.ActionStatus || action == core.ActionImportExisting) {
		if result.InstanceStatus.Engine != engine {
			return fmt.Errorf("%w: main instance result does not match its leased target", ErrInvalid)
		}
	}
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
	if result.DiagnosticDigest != "" {
		diagnostic, err := s.uploadedTaskDiagnostic(ctx, tx, taskID, result.LeaseID, result.DiagnosticDigest)
		if err != nil {
			return err
		}
		result.Output = diagnostic.Output
		// Server-side checks can add a cause; they must not replace the Agent cause.
		if result.Error != agentErrorSummary && result.Error != "" {
			diagnostic.Error += "\n控制面结果核对：" + result.Error
		}
		result.Error = diagnostic.Error
	}
	if (action != core.ActionReadConfig && action != core.ActionReadManagedConfig) || !result.Success {
		result.Output = core.RedactDiagnostic(result.Output)
	}
	result.Error = core.RedactDiagnostic(result.Error)
	if action == core.ActionImportExisting && result.Success {
		importedID, importedVersion, err := s.commitConfigImportTx(ctx, tx, taskID, agentID, engine, runtimeSnapshot)
		if errors.Is(err, ErrConflict) {
			result.Success = false
			status = core.TaskFailed
			result.Error += "\n控制面保存阶段：" + err.Error()
		} else if err != nil {
			return err
		}
		if importedID != "" {
			configID, configVersion = importedID, importedVersion
		}
	}
	storedContent := ""
	storedOutput := truncate(core.DiagnosticText(result.Output), 64<<10)
	storedError := truncate(core.DiagnosticText(result.Error), 8<<10)
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
			if action == core.ActionReadConfig && !shared && result.ImportSourceDigest != "" {
				storedContent, err = func() (string, error) {
					original, err := s.decryptContent(runtimeSnapshot)
					if err != nil {
						return "", err
					}
					snapshot, err := core.EncodeImportSnapshot(content, result.ImportSourceDigest, core.ImportSnapshotSource(original))
					if err != nil {
						return "", err
					}
					return core.WithImportDependencies(snapshot, result.ImportDependencies)
				}()
				if err != nil {
					return fmt.Errorf("invalid Agent import provenance: %w", err)
				}
			}
			storedOutput = "current configuration read and validated"
			storedError = ""
		}
	}
	// Successful configuration reads are encrypted snapshots, never diagnostics.
	if action != core.ActionReadConfig && action != core.ActionReadManagedConfig || status != core.TaskSucceeded {
		var attempt int
		if err := tx.QueryRow(ctx, `SELECT attempt FROM tasks WHERE id=$1`, taskID).Scan(&attempt); err != nil {
			return err
		}
		fullOutput, fullError := core.DiagnosticText(result.Output), core.DiagnosticText(result.Error)
		if storedError != "" && fullError == "" {
			fullError = storedError
		}
		source := "legacy-agent"
		if result.DiagnosticDigest != "" {
			source = "agent-diagnostic-v1"
		}
		if result.ExecutedAttempt < 0 || result.ExecutedAttempt > attempt {
			return fmt.Errorf("%w: invalid original execution attempt", ErrInvalid)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO task_diagnostics(task_id,attempt,output,error,source,executed_attempt) VALUES($1,$2,$3,$4,$5,NULLIF($6,0)) ON CONFLICT DO NOTHING`, taskID, attempt, fullOutput, fullError, source, result.ExecutedAttempt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM task_diagnostic_uploads WHERE task_id=$1`, taskID); err != nil {
		return err
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
		appliedRunning := true
		if action == core.ActionDeploy && runtimeSnapshot != "" {
			content, decryptErr := s.decryptContent(runtimeSnapshot)
			if decryptErr != nil {
				return decryptErr
			}
			content, _, decryptErr = core.DecodeExecutionSnapshot(content)
			if decryptErr != nil {
				return decryptErr
			}
			if plan, planErr := serverconfig.PrepareIndependentEgress(engine, content); planErr == nil && plan.Source == "disabled" {
				appliedRunning = false
			}
		}
		if err := recordEngineOwnershipTx(ctx, tx, taskID, action, result.TrafficSettled, appliedRunning); err != nil {
			return err
		}
	}
	if shared && result.InstanceStatus != nil {
		// A freshly bootstrapped sidecar keeps this status task's identity.
		// Adopt that same identity atomically so subsequent heartbeats remain
		// eligible. Older operation observations must retain their own identity.
		statusRefresh := action == core.ActionStatus && result.InstanceStatus.TaskID == taskID
		if err := recordSharedInstanceObservationTx(ctx, tx, agentID, *result.InstanceStatus, versioned, time.Now().UTC(), result.TrafficSettled, nil, statusRefresh); err != nil {
			return err
		}
	} else if shared && status == core.TaskFailed && action.ChangesInstance() {
		if _, err := tx.Exec(ctx, `UPDATE agent_shared_instance_ownership state SET service_status='unknown',status_reason=$2
			FROM tasks t WHERE t.id=$1 AND state.agent_id=t.agent_id AND state.engine=t.engine AND state.share_id=t.shared_traffic_id
			AND state.last_task_id=t.id`, taskID, storedError); err != nil {
			return err
		}
	}
	if !shared && result.InstanceStatus != nil && (action.ChangesInstance() || action == core.ActionStatus || action == core.ActionImportExisting) {
		if err := recordHostInstanceObservationTx(ctx, tx, taskID, *result.InstanceStatus); err != nil {
			return err
		}
	}
	if status == core.TaskSucceeded && action == core.ActionStatus && containsFeature(features, core.AgentFeatureHostPorts) && result.HostPorts != nil {
		if err := recordHostPortsTx(ctx, tx, agentID, taskID, *result.HostPorts); err != nil {
			return err
		}
	}
	if status == core.TaskSucceeded && (action == core.ActionDeploy || action == core.ActionImportExisting) {
		if shared && action == core.ActionDeploy {
			plain, err := s.decryptContent(runtimeSnapshot)
			if err != nil {
				return err
			}
			_, frontends, err := core.DecodeExecutionSnapshot(plain)
			if err != nil {
				return err
			}
			grants := []core.ProxyIngress{}
			for _, f := range frontends {
				if f.SharedIngressGeneration > 0 {
					grants = append(grants, core.ProxyIngress{Mode: f.Mode, Port: f.PublicPort, Generation: f.SharedIngressGeneration})
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE agent_shared_instance_ownership SET applied_proxy_ingresses=$4
				WHERE agent_id=$1 AND engine=$2 AND share_id=$3 AND config_id=$5 AND config_version=$6`, agentID, engine, shareID, grants, configID, configVersion); err != nil {
				return err
			}
		}
		if err := s.finishDeployedTrafficTx(ctx, tx, agentID, engine, shareID, configID, configVersion, result.TrafficSettled); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
