package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type ConfigImportRequest struct {
	SnapshotTaskID     string `json:"snapshot_task_id"`
	ExpectedVersion    int    `json:"expected_version"`
	ExpectedInstanceID string `json:"expected_instance_id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
}

// Import preparation never saves a configuration or a revision. The exact
// encrypted read-task snapshot remains owned by this separate migration task.
func (s *Store) CreateConfigImport(ctx context.Context, agentID string, engine core.Engine, input ConfigImportRequest) (core.Task, error) {
	return s.createConfigImport(ctx, agentID, engine, input, "")
}

func (s *Store) createConfigImport(ctx context.Context, agentID string, engine core.Engine, input ConfigImportRequest, retryID string) (core.Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Task{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return core.Task{}, err
	}
	if err = requireTaskPermission(ctx, tx, core.ActionImportExisting, false); err != nil {
		return core.Task{}, err
	}
	if err = requireAgentAdministration(ctx, tx, agentID); err != nil {
		return core.Task{}, err
	}
	if err = requireHostConfigRead(ctx, tx, agentID, engine); err != nil {
		return core.Task{}, err
	}
	if input.ExpectedInstanceID != "" || !engine.Valid() || input.ExpectedVersion < 0 {
		return core.Task{}, ErrInvalid
	}
	name, description := input.Name, input.Description
	var retrySnapshot string
	if retryID != "" {
		err = tx.QueryRow(ctx, `SELECT COALESCE(i.snapshot_task_id,''),i.expected_version,i.name,i.description,i.snapshot FROM task_imports i JOIN tasks t ON t.id=i.task_id WHERE t.id=$1 AND t.agent_id=$2 AND t.engine=$3 AND t.owner_id=$4 AND t.status IN ('failed','canceled')`, retryID, agentID, engine, scopeForConfig(ctx).OwnerID).Scan(&input.SnapshotTaskID, &input.ExpectedVersion, &name, &description, &retrySnapshot)
		if err != nil {
			return core.Task{}, mapError(err)
		}
	}
	name, description, err = validateConfigMetadata(name, description)
	if err != nil {
		return core.Task{}, err
	}
	var duplicate string
	err = tx.QueryRow(ctx, `SELECT t.id FROM task_imports i JOIN tasks t ON t.id=i.task_id WHERE t.agent_id=$1 AND t.engine=$2 AND t.owner_id=$3 AND i.snapshot_task_id=$4 AND i.expected_version=$5 AND i.name=$6 AND i.description=$7 AND t.status IN ('pending','running','succeeded') ORDER BY t.created_at DESC LIMIT 1`, agentID, engine, scopeForConfig(ctx).OwnerID, input.SnapshotTaskID, input.ExpectedVersion, name, description).Scan(&duplicate)
	if err == nil {
		task, err := s.GetTask(ctx, duplicate)
		task.Reused = true
		return task, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return core.Task{}, err
	}
	var runtime map[core.Engine]core.RuntimeState
	var features []string
	var capabilities []core.Engine
	if err = tx.QueryRow(ctx, `SELECT runtime,features,capabilities FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&runtime, &features, &capabilities); err != nil {
		return core.Task{}, mapError(err)
	}
	if !containsEngine(capabilities, engine) {
		return core.Task{}, ErrForbidden
	}
	if reason := runtime[engine].ManagedBlockReason(features); reason != "" {
		return core.Task{}, fmt.Errorf("%w: %s", ErrConflict, reason)
	}
	if reason := runtime[engine].ExistingConfigUnsupportedReason; reason != "" {
		return core.Task{}, fmt.Errorf("%w: 导入来源不可用：%s", ErrConflict, reason)
	}
	if !runtime[engine].ExistingConfigAvailable {
		return core.Task{}, fmt.Errorf("%w: 没有待导入的系统服务来源", ErrConflict)
	}
	var content string
	if retryID != "" {
		content, err = s.decryptContent(retrySnapshot)
		if err == nil {
			content, _, err = core.DecodeExecutionSnapshot(content)
		}
	} else {
		err = tx.QueryRow(ctx, `SELECT config_content FROM tasks WHERE id=$1 AND agent_id=$2 AND engine=$3 AND owner_id=$4
   AND action='read-config' AND status='succeeded' AND NOT shared_instance AND config_content IS NOT NULL FOR UPDATE`, input.SnapshotTaskID, agentID, engine, scopeForConfig(ctx).OwnerID).Scan(&content)
		if err == nil {
			content, err = s.decryptContent(content)
		}
	}
	if err != nil {
		return core.Task{}, mapError(err)
	}
	if err = core.ValidateConfig(engine, content); err != nil {
		return core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var id string
	var version int
	err = tx.QueryRow(ctx, `SELECT id,version FROM configs WHERE agent_id=$1 AND engine=$2 AND owner_id=$3 AND deleted_at IS NULL FOR UPDATE`, agentID, engine, scopeForConfig(ctx).OwnerID).Scan(&id, &version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return core.Task{}, err
	}
	if version != input.ExpectedVersion {
		return core.Task{}, fmt.Errorf("%w: 配置已变化，导入尚未执行，请重新确认", ErrConflict)
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE agent_id=$1 AND engine=$2 AND NOT shared_instance AND status IN ('pending','running') AND action IN ('deploy','validate','import-existing','install','uninstall','start','stop','restart'))`, agentID, engine).Scan(&busy); err != nil {
		return core.Task{}, err
	}
	if busy {
		return core.Task{}, fmt.Errorf("%w: 同一托管目标已有任务，请完成后再确认迁移", ErrConflict)
	}
	task := core.Task{AgentID: agentID, Engine: engine, Action: core.ActionImportExisting, Status: core.TaskPending, CreatedAt: time.Now().UTC(), ConfigContent: content}
	if err = s.checkTaskPortUseTx(ctx, tx, &task, true); err != nil {
		return core.Task{}, err
	}
	task.ID, err = core.NewID("tsk")
	if err != nil {
		return core.Task{}, err
	}
	snapshot, err := core.EncodeExecutionSnapshot(content, nil, false, false)
	if err != nil {
		return core.Task{}, err
	}
	stored, err := s.encryptContent(snapshot)
	if err != nil {
		return core.Task{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO tasks(id,agent_id,engine,action,status,created_at,owner_id,config_content) VALUES($1,$2,$3,'import-existing','pending',$4,$5,$6)`, task.ID, agentID, engine, task.CreatedAt, scopeForConfig(ctx).OwnerID, stored)
	if err != nil {
		return core.Task{}, err
	}
	targetID := id
	if retryID != "" {
		err = tx.QueryRow(ctx, `SELECT target_config_id FROM task_imports WHERE task_id=$1`, retryID).Scan(&targetID)
		if err != nil {
			return core.Task{}, err
		}
	}
	if targetID == "" {
		targetID, err = core.NewID("cfg")
		if err != nil {
			return core.Task{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_imports(task_id,snapshot_task_id,expected_config_id,expected_version,name,description,snapshot,retry_of,target_config_id) VALUES($1,NULLIF($2,''),$3,$4,$5,$6,$7,NULLIF($8,''),$9)`, task.ID, input.SnapshotTaskID, id, version, name, description, stored, retryID, targetID)
	if err != nil {
		return core.Task{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return core.Task{}, err
	}
	s.signalTaskReady(agentID)
	task.ConfigContent = ""
	return task, nil
}

// Complete only after the Agent confirmed the migration, with the node/task
// locks already held. Failed/canceled imports never enter configuration storage.
func (s *Store) commitConfigImportTx(ctx context.Context, tx pgx.Tx, taskID, agentID string, engine core.Engine, snapshot string) (string, int, error) {
	var source, expectedID, name, description, owner, targetID string
	var expected int
	err := tx.QueryRow(ctx, `SELECT COALESCE(i.snapshot_task_id,''),i.expected_config_id,i.expected_version,i.name,i.description,t.owner_id,i.target_config_id FROM task_imports i JOIN tasks t ON t.id=i.task_id WHERE i.task_id=$1`, taskID).Scan(&source, &expectedID, &expected, &name, &description, &owner, &targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	content, err := s.decryptContent(snapshot)
	if err != nil {
		return "", 0, err
	}
	content, _, err = core.DecodeExecutionSnapshot(content)
	if err != nil {
		return "", 0, err
	}
	var id string
	var version int
	err = tx.QueryRow(ctx, `SELECT id,version FROM configs WHERE agent_id=$1 AND engine=$2 AND owner_id=$3 AND deleted_at IS NULL FOR UPDATE`, agentID, engine, owner).Scan(&id, &version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", 0, err
	}
	if id != expectedID || version != expected {
		return "", 0, fmt.Errorf("%w: 迁移已执行，但保存目标已变化；保留当前配置，请核对托管服务", ErrConflict)
	}
	stored, err := s.encryptContent(content)
	if err != nil {
		return "", 0, err
	}
	now := time.Now().UTC()
	if id == "" {
		id = targetID
		_, err = tx.Exec(ctx, `INSERT INTO configs(id,agent_id,engine,owner_id,name,description,content,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$8)`, id, agentID, engine, owner, name, description, stored, now)
	} else {
		_, err = tx.Exec(ctx, `UPDATE configs SET content=$2,name=$3,description=$4,version=version+1,updated_at=$5 WHERE id=$1`, id, stored, name, description, now)
	}
	if err != nil {
		return "", 0, err
	}
	config := core.Config{ID: id, AgentID: agentID, Engine: engine, OwnerID: owner, Name: name, Description: description, Content: content, Version: version + 1, CreatedAt: now, UpdatedAt: now}
	if err = s.insertConfigRevision(ctx, tx, config); err != nil {
		return "", 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE tasks SET config_id=$2,config_version=$3 WHERE id=$1`, taskID, id, config.Version)
	return id, config.Version, err
}
