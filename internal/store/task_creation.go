package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func (s *Store) CreateTask(ctx context.Context, request core.TaskRequest) (core.Task, error) {
	return s.createTask(ctx, request, 0)
}

func (s *Store) createTask(ctx context.Context, request core.TaskRequest, readCacheMaxAge time.Duration) (core.Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Task{}, err
	}
	defer tx.Rollback(ctx)
	task, err := s.createTaskTx(ctx, tx, request, readCacheMaxAge)
	if err != nil {
		return core.Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Task{}, err
	}
	if !task.Reused {
		s.signalTaskReady(task.AgentID)
	}
	return task, nil
}

// The caller commits before notifying the Agent. This also allows a preset
// revision and its exact task snapshot to be published in one transaction.
func (s *Store) createTaskTx(ctx context.Context, tx pgx.Tx, request core.TaskRequest, readCacheMaxAge time.Duration) (core.Task, error) {
	scope := scopeForConfig(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.Task{}, err
	}
	if err := requireTaskPermission(ctx, tx, request.Action, false); err != nil {
		return core.Task{}, err
	}
	if scope.Admin && request.ConfigID != "" {
		// An administrator may deploy someone else's configuration. Lock its
		// durable owner before the Agent, just like the owner's own request.
		rows, err := tx.Query(ctx, `SELECT id FROM panel_users
			WHERE id=(SELECT owner_id FROM configs WHERE id=$1) FOR SHARE`, request.ConfigID)
		if err != nil {
			return core.Task{}, err
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return core.Task{}, err
		}
	}
	if err := requireAgentAccess(ctx, tx, request.AgentID); err != nil {
		return core.Task{}, err
	}
	if request.InstallIfMissing || (request.Action != core.ActionDeploy && request.Action != core.ActionValidate && request.Action != core.ActionStatus) {
		if err := requireAgentAdministration(ctx, tx, request.AgentID); err != nil {
			return core.Task{}, err
		}
	}
	if request.InstallIfMissing && (request.Action != core.ActionValidate && request.Action != core.ActionDeploy ||
		request.ExpectedConfigVersion < 1 || request.CoreVersion != "" || request.CoreSource != "") {
		return core.Task{}, fmt.Errorf("%w: automatic installation requires an exact configuration revision and validate or deploy intent", ErrInvalid)
	}
	if request.ExpectedConfigVersion < 0 || (request.ExpectedConfigVersion != 0 && request.Action != core.ActionDeploy && request.Action != core.ActionValidate && request.Action != core.ActionImportExisting) {
		return core.Task{}, fmt.Errorf("%w: expected configuration version requires a configuration task", ErrInvalid)
	}
	if request.Action == core.ActionConfigureTCP {
		settings, err := core.NormalizeTCPSettings(request.TCPSettings)
		if err != nil {
			return core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		request.TCPSettings = settings
	} else if len(request.TCPSettings) != 0 {
		return core.Task{}, fmt.Errorf("%w: TCP settings are only accepted by configure-tcp", ErrInvalid)
	}
	if !request.Action.Valid() {
		return core.Task{}, fmt.Errorf("%w: unsupported action %q", ErrInvalid, request.Action)
	}
	if request.Action.AgentLevel() {
		if request.Engine != "" || request.ConfigID != "" || request.CoreVersion != "" {
			return core.Task{}, fmt.Errorf("%w: agent-level tasks cannot reference an engine, configuration, or core version", ErrInvalid)
		}
	} else if !request.Engine.Valid() {
		return core.Task{}, fmt.Errorf("%w: unsupported engine %q", ErrInvalid, request.Engine)
	}
	if !request.Action.AgentLevel() {
		if err := requireAgentEngineAccess(ctx, tx, request.AgentID, request.Engine); err != nil {
			return core.Task{}, err
		}
	}
	if request.Action == core.ActionInstall {
		normalizedVersion, versionErr := core.NormalizeCoreVersionSelector(request.CoreVersion)
		if versionErr != nil {
			return core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, versionErr)
		}
		request.CoreVersion = normalizedVersion
		source, sourceErr := core.NormalizeCoreSource(request.Engine, normalizedVersion, request.CoreSource)
		if sourceErr != nil {
			return core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, sourceErr)
		}
		request.CoreSource = source
		if request.ConfigID != "" {
			return core.Task{}, fmt.Errorf("%w: install tasks cannot reference a configuration", ErrInvalid)
		}
	} else {
		if request.CoreSource != "" {
			return core.Task{}, fmt.Errorf("%w: core source is only applicable to Mihomo development installs", ErrInvalid)
		}
		request.CoreSource = ""
		request.CoreVersion = ""
	}
	var capabilitiesJSON, featuresJSON, runtimeJSON []byte
	if err := tx.QueryRow(ctx, `SELECT capabilities,features,runtime FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, request.AgentID).Scan(&capabilitiesJSON, &featuresJSON, &runtimeJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return core.Task{}, fmt.Errorf("agent: %w", ErrNotFound)
		}
		return core.Task{}, err
	}
	var capabilities []core.Engine
	if err := json.Unmarshal(capabilitiesJSON, &capabilities); err != nil {
		return core.Task{}, err
	}
	var features []string
	if err := json.Unmarshal(featuresJSON, &features); err != nil {
		return core.Task{}, err
	}
	var runtime map[core.Engine]core.RuntimeState
	if err := json.Unmarshal(runtimeJSON, &runtime); err != nil {
		return core.Task{}, err
	}
	if request.Action == core.ActionReadConfig || request.Action == core.ActionReadManagedConfig || request.Action == core.ActionImportExisting {
		if err := requireHostConfigRead(ctx, tx, request.AgentID, request.Engine); err != nil {
			return core.Task{}, err
		}
	}
	if request.Action == core.ActionUpgradeAgent && !containsFeature(features, core.AgentFeatureSelfUpgrade) {
		return core.Task{}, fmt.Errorf("%w: this Agent does not support remote upgrades; run the current one-click installation once", ErrConflict)
	}
	if request.Action.SystemBBR() && !containsFeature(features, core.AgentFeatureSystemBBR) {
		return core.Task{}, fmt.Errorf("%w: upgrade this Agent before managing system BBR", ErrConflict)
	}
	if request.Action == core.ActionIPQuality {
		if err := s.checkIPQualityTaskTx(ctx, tx, request.AgentID, features); err != nil {
			return core.Task{}, err
		}
	}
	if request.Action.SystemBBR() {
		// TCP settings affect the whole host. Keep the shared exclusion even
		// though task reuse and visibility are scoped to their submitter.
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks
			WHERE agent_id=$1 AND action IN ('enable-bbr','disable-bbr','configure-tcp')
			  AND status IN ('pending','running') AND owner_id<>$2)`, request.AgentID, scope.OwnerID).Scan(&busy); err != nil {
			return core.Task{}, err
		}
		if busy {
			return core.Task{}, fmt.Errorf("%w: another system TCP task is pending or running", ErrConflict)
		}
	}
	if !request.Action.AgentLevel() {
		if err := rejectPendingCapabilityTransition(ctx, tx, request.AgentID, request.Engine); err != nil {
			return core.Task{}, err
		}
		if !containsEngine(capabilities, request.Engine) {
			return core.Task{}, fmt.Errorf("%w: agent does not advertise the requested engine", ErrInvalid)
		}
		if reason := strings.TrimSpace(runtime[request.Engine].ExistingConfigUnsupportedReason); reason != "" {
			return core.Task{}, fmt.Errorf("%w: %s core tasks are disabled because an existing service could not be mapped safely: %s", ErrConflict, request.Engine, reason)
		}
	}
	if (request.Action == core.ActionValidate || request.Action == core.ActionDeploy) &&
		!containsFeature(features, core.AgentFeatureIndependentEgress) {
		return core.Task{}, fmt.Errorf("%w: upgrade the Agent before validating or deploying independent exits", ErrConflict)
	}
	if request.InstallIfMissing && !containsFeature(features, core.AgentFeaturePresetAutoInstall) {
		return core.Task{}, fmt.Errorf("%w: upgrade the Agent before automatically installing a stable core with an inbound", ErrConflict)
	}
	if request.Action == core.ActionReadManagedConfig && !containsFeature(features, core.AgentFeatureManagedConfigRead) {
		return core.Task{}, fmt.Errorf("%w: this Agent cannot read the managed configuration independently; upgrade the Agent through the panel first", ErrConflict)
	}
	if request.Action == core.ActionStart || request.Action == core.ActionRestart {
		if err := requireSafeEngineStart(ctx, tx, request.AgentID, request.Engine); err != nil {
			return core.Task{}, err
		}
	}
	if request.Action == core.ActionInstall && request.Engine == core.EngineMihomo &&
		request.CoreVersion == core.CoreVersionDevelopment &&
		request.CoreSource == string(core.CoreSourceMirror) &&
		!containsFeature(features, core.AgentFeatureMihomoDevelopmentSource) {
		return core.Task{}, fmt.Errorf("%w: this Agent does not support the Mihomo Alpha mirror source; upgrade the Agent through the panel first", ErrConflict)
	}
	if readCacheMaxAge > 0 {
		// Cache reuse is a task-creation optimization, never an authorization or
		// validation shortcut. The Agent lock also serializes it with dispatch
		// and snapshot invalidation.
		recent, err := s.recentReadTask(ctx, tx, request.AgentID, request.Engine, request.Action, readCacheMaxAge)
		if err == nil {
			recent.Reused = true
			return recent, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return core.Task{}, err
		}
	}

	task := core.Task{
		InstallIfMissing: request.InstallIfMissing,
		TCPSettings:      request.TCPSettings,
		AgentID:          request.AgentID, Action: request.Action, Engine: request.Engine,
		ConfigID: request.ConfigID, CoreVersion: request.CoreVersion, CoreSource: request.CoreSource,
		Status: core.TaskPending, CreatedAt: time.Now().UTC(),
	}
	if task.TCPSettings == nil {
		task.TCPSettings = core.TCPSettings{}
	}
	if request.Action == core.ActionDeploy || request.Action == core.ActionValidate || request.Action == core.ActionImportExisting {
		var configEngine core.Engine
		var configAgentID, configOwnerID string
		configArgs := []any{request.ConfigID}
		configWhere := ownerClause(ctx, "owner_id", &configArgs)
		err := tx.QueryRow(ctx, `SELECT engine,content,version,COALESCE(agent_id,''),owner_id FROM configs WHERE id=$1 AND deleted_at IS NULL`+configWhere+` FOR UPDATE`, configArgs...).Scan(&configEngine, &task.ConfigContent, &task.ConfigVersion, &configAgentID, &configOwnerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return core.Task{}, fmt.Errorf("configuration: %w", ErrNotFound)
		}
		if err != nil {
			return core.Task{}, err
		}
		if configEngine != request.Engine {
			return core.Task{}, fmt.Errorf("%w: task engine does not match configuration engine", ErrInvalid)
		}
		if request.ExpectedConfigVersion != 0 && task.ConfigVersion != request.ExpectedConfigVersion {
			return core.Task{}, fmt.Errorf("%w: configuration changed after saving; no task was submitted, reload before deployment", ErrConflict)
		}
		if request.Action == core.ActionImportExisting && configAgentID != request.AgentID {
			return core.Task{}, fmt.Errorf("%w: existing service migration requires this agent's saved snapshot", ErrInvalid)
		}
		if configAgentID != "" && configAgentID != request.AgentID {
			return core.Task{}, fmt.Errorf("%w: node-owned configuration cannot be deployed to another agent", ErrInvalid)
		}
		task.ConfigContent, err = s.decryptContent(task.ConfigContent)
		if err != nil {
			return core.Task{}, err
		}
		settings, settingsErr := s.panelSettingsForOwner(ctx, tx, scopeForConfig(ctx).OwnerID)
		if settingsErr != nil {
			return core.Task{}, settingsErr
		}
		if settings.CNIPSource != nil && settings.CNIPSource.Custom() && request.Action != core.ActionImportExisting {
			if !containsFeature(features, core.AgentFeatureCNIPSource) {
				return core.Task{}, fmt.Errorf("%w: 请先升级 Agent，以支持自定义 CN IP 数据源", ErrConflict)
			}
			source := *settings.CNIPSource
			task.CNIPSource = &source
		}
		if request.Action == core.ActionValidate || request.Action == core.ActionDeploy {
			if err := serverconfig.ValidateIndependentEgress(task.Engine, task.ConfigContent); err != nil {
				return core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, err)
			}
		}
		if err := s.prepareSharedTaskTx(ctx, tx, &task, configOwnerID, features, runtime[request.Engine]); err != nil {
			return core.Task{}, err
		}
		if request.Engine == core.EngineShadowsocksRust {
			rows, policyErr := tx.Query(ctx, `SELECT agent_id,engine,tag,kind,port,config_version,block_mainland_destination,block_mainland_source
				FROM mainland_access_policies WHERE config_id=$1 AND config_version=$2 ORDER BY port,tag`, request.ConfigID, task.ConfigVersion)
			if policyErr != nil {
				return core.Task{}, policyErr
			}
			for rows.Next() {
				var policy core.MainlandAccessPolicy
				if policyErr = rows.Scan(&policy.AgentID, &policy.Engine, &policy.Tag, &policy.Kind, &policy.Port, &policy.ConfigVersion,
					&policy.BlockMainlandDestination, &policy.BlockMainlandSource); policyErr != nil {
					rows.Close()
					return core.Task{}, policyErr
				}
				task.MainlandAccessPolicies = append(task.MainlandAccessPolicies, policy)
			}
			policyErr = rows.Err()
			rows.Close()
			if policyErr != nil {
				return core.Task{}, policyErr
			}
		}
	} else {
		task.ConfigID = ""
	}
	existing, existingErr := scanTask(tx.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at,tcp_settings,install_if_missing
		FROM tasks existing
		WHERE agent_id=$1 AND (action=$2 OR ($2 IN ('enable-bbr','disable-bbr','configure-tcp') AND action IN ('enable-bbr','disable-bbr','configure-tcp'))) AND engine=$3
		  AND COALESCE(config_id,'')=$4 AND COALESCE(config_version,0)=$5 AND COALESCE(core_version,'')=$6
		  AND (CASE WHEN $2='install' AND $3='mihomo' AND $6='development' AND COALESCE($7,'') IN ('','official')
		            THEN 'official' ELSE COALESCE($7,'') END)
		    = (CASE WHEN action='install' AND engine='mihomo' AND core_version='development' AND COALESCE(core_source,'') IN ('','official')
		            THEN 'official' ELSE COALESCE(core_source,'') END)
		  AND owner_id=$8 AND install_if_missing=$9 AND status IN ('pending','running')
		  AND ($2 NOT IN ('read-config','read-managed-config') OR NOT EXISTS(
		      SELECT 1 FROM tasks mutation
		      WHERE mutation.agent_id=existing.agent_id AND mutation.engine=existing.engine
		        AND (mutation.action IN ('deploy','install','import-existing') OR mutation.install_if_missing)
		        AND mutation.status IN ('pending','running') AND mutation.created_at>existing.created_at))
		ORDER BY created_at DESC LIMIT 1`,
		task.AgentID, task.Action, task.Engine, task.ConfigID, task.ConfigVersion, task.CoreVersion, task.CoreSource, scope.OwnerID, task.InstallIfMissing), false)
	if existingErr == nil {
		if task.Action.SystemBBR() && (existing.Action != task.Action || !maps.Equal(existing.TCPSettings, task.TCPSettings)) {
			return core.Task{}, fmt.Errorf("%w: another system TCP task is pending or running", ErrConflict)
		}
		existing.Reused = true
		return existing, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return core.Task{}, existingErr
	}
	var err error
	task.ID, err = core.NewID("tsk")
	if err != nil {
		return core.Task{}, err
	}
	storedConfigContent, err := s.encryptContent(task.ConfigContent)
	if err != nil {
		return core.Task{}, err
	}
	mainlandPoliciesJSON, err := json.Marshal(task.MainlandAccessPolicies)
	if err != nil {
		return core.Task{}, err
	}
	_, err = tx.Exec(ctx, `
			INSERT INTO tasks (id,agent_id,action,engine,config_id,config_version,config_content,mainland_access_policies,core_version,core_source,status,attempt,created_at,tcp_settings,owner_id,shared_traffic_id,cnip_source,install_if_missing)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,0),NULLIF($7,''),$8,NULLIF($9,''),NULLIF($10,''),$11,0,$12,$13,$14,$15,$16,$17)`,
		task.ID, task.AgentID, task.Action, task.Engine, task.ConfigID, task.ConfigVersion, storedConfigContent, mainlandPoliciesJSON, task.CoreVersion, task.CoreSource, task.Status, task.CreatedAt, task.TCPSettings, scope.OwnerID, task.SharedTrafficID, task.CNIPSource, task.InstallIfMissing)
	if err != nil {
		return core.Task{}, mapError(err)
	}
	return task, nil
}
