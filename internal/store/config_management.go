package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Missing state means all source objects are enabled, including pre-migration revisions.
func (s *Store) configManagementStateTx(ctx context.Context, db storeExecutor, id string, version int) (serverconfig.ManagedState, error) {
	state := serverconfig.ManagedState{Disabled: []serverconfig.ManagedResource{}}
	var content string
	err := db.QueryRow(ctx, `SELECT content FROM config_management_states WHERE config_id=$1 AND config_version=$2`, id, version).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	content, err = s.decryptContent(content)
	if err != nil {
		return state, err
	}
	err = json.Unmarshal([]byte(content), &state)
	return state, err
}

func (s *Store) saveConfigManagementStateTx(ctx context.Context, tx pgx.Tx, config core.Config, state serverconfig.ManagedState) error {
	if _, err := serverconfig.CompileManagedConfig(config.Engine, config.Content, state); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(state.Disabled) == 0 {
		return nil
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	encrypted, err := s.encryptContent(string(payload))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO config_management_states(config_id,config_version,content) VALUES($1,$2,$3)`, config.ID, config.Version, encrypted)
	return mapError(err)
}

// Authorization uses the same owner and instance boundary as configuration history.
func (s *Store) ConfigManagementState(ctx context.Context, id string, version int) (serverconfig.ManagedState, error) {
	if _, err := s.ConfigRevision(ctx, id, version); err != nil {
		return serverconfig.ManagedState{}, err
	}
	return s.configManagementStateTx(ctx, s.pool, id, version)
}

// State and the full source form one CAS revision. Validation/deployment reads
// this exact revision, never whichever state happens to be current later.
func (s *Store) SaveAgentConfigManagement(ctx context.Context, input core.Config, expectedVersion int, state serverconfig.ManagedState, action core.Action) (core.Config, core.Task, error) {
	if action != core.ActionValidate && action != core.ActionDeploy {
		return core.Config{}, core.Task{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	defer tx.Rollback(ctx)
	saved, err := s.saveAgentConfigTx(ctx, tx, input, expectedVersion, nil, state)
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	task, err := s.createTaskTx(ctx, tx, core.TaskRequest{AgentID: saved.AgentID, Engine: saved.Engine, Action: action, ConfigID: saved.ID, ExpectedConfigVersion: saved.Version}, 0)
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return core.Config{}, core.Task{}, err
	}
	s.signalTaskReadyAfterCommit(ctx, saved.AgentID)
	return saved, task, nil
}

func (s *Store) compileConfigManagementRuntime(ctx context.Context, db storeExecutor, configID string, version int, engine core.Engine, content string) (string, error) {
	state, err := s.configManagementStateTx(ctx, db, configID, version)
	if err != nil {
		return "", err
	}
	runtime, err := serverconfig.CompileManagedConfig(engine, content, state)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(state.Disabled) == 0 {
		return runtime, nil
	}
	plan, err := serverconfig.PrepareIndependentEgress(engine, runtime)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return plan.Content, nil
}

// ConfigManagementDeployment describes the applied revision independently from
// saved target switches. A successful validation never advances this status.
type ConfigManagementDeployment struct {
	State         string `json:"state"`
	ServiceStatus string `json:"service_status"`
	TaskID        string `json:"task_id,omitempty"`
	TaskStatus    string `json:"task_status,omitempty"`
}

func (s *Store) ConfigManagementDeployment(ctx context.Context, id string, version int) (ConfigManagementDeployment, error) {
	result := ConfigManagementDeployment{State: "pending", ServiceStatus: "unknown"}
	config, err := s.ConfigRevision(ctx, id, version)
	if err != nil {
		return result, err
	}
	agent, err := s.GetAgent(ctx, config.AgentID)
	if err != nil {
		return result, err
	}
	if agent.Status == "online" {
		result.ServiceStatus = agent.Runtime[config.Engine].ServiceStatus
		if result.ServiceStatus == "" {
			result.ServiceStatus = "unknown"
		}
	}
	query := `SELECT config_id,config_version,(config_uncertain OR uncertain) FROM agent_engine_ownership WHERE agent_id=$1 AND engine=$2 AND owner_id=$3`
	args := []any{config.AgentID, config.Engine, config.OwnerID}
	sharedID := ""
	if !agent.CanManage {
		if agent.SharedAllocation == nil {
			return result, ErrNotFound
		}
		sharedID = agent.SharedAllocation.ID
		query = `SELECT config_id,config_version,(config_uncertain OR uncertain) FROM agent_shared_instance_ownership WHERE agent_id=$1 AND engine=$2 AND owner_id=$3 AND share_id=$4`
		args = append(args, sharedID)
	}
	var appliedID string
	var appliedVersion int
	var uncertain bool
	err = s.pool.QueryRow(ctx, query, args...).Scan(&appliedID, &appliedVersion, &uncertain)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if uncertain {
		result.State = "unknown"
	} else if appliedID == id && appliedVersion == version {
		result.State = "applied"
	}
	err = s.pool.QueryRow(ctx, `SELECT id,status FROM tasks WHERE agent_id=$1 AND engine=$2 AND owner_id=$3
		AND config_id=$4 AND config_version=$5 AND action='deploy' AND shared_instance=$6 AND shared_traffic_id=$7
		ORDER BY created_at DESC,id DESC LIMIT 1`, config.AgentID, config.Engine, config.OwnerID, id, version, !agent.CanManage, sharedID).Scan(&result.TaskID, &result.TaskStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	switch result.TaskStatus {
	case "pending", "running":
		result.State = "deploying"
	case "failed":
		result.State = "failed"
	case "canceled":
		result.State = "canceled"
	}
	return result, nil
}
