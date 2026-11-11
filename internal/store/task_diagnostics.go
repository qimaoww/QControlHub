package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Maintenance already holds the real database owner. Its failures use the
// same terminal task records without pretending to be Agent executions.
func (s *Store) retainBackgroundFailure(ctx context.Context, owner, operation, resource, stage string, cause error) error {
	if cause == nil {
		return nil
	}
	detail := SafeFailureDiagnostic(cause)
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, err := s.RecordPanelFailure(certificateOwnerContext(persist, owner), core.PanelFailure{Operation: operation, Resource: resource, Stage: stage, Summary: operation + "失败", Detail: detail})
	return err
}

// SafeFailureDiagnostic retains causal wrappers and stable SQL identifiers.
// PostgreSQL messages and details can contain a complete failing private row.
func SafeFailureDiagnostic(cause error) string {
	detail := cause.Error()
	var databaseError *pgconn.PgError
	if errors.As(cause, &databaseError) {
		detail = fmt.Sprintf("%s; SQLSTATE=%s; schema=%s; table=%s; constraint=%s", strings.ReplaceAll(detail, databaseError.Error(), "PostgreSQL error"), databaseError.Code, databaseError.SchemaName, databaseError.TableName, databaseError.ConstraintName)
	}
	return core.DiagnosticText(detail)
}

func (s *Store) retainAgentFailure(ctx context.Context, agent, operation, stage string, cause error) error {
	var owner string
	if err := s.pool.QueryRow(ctx, `SELECT owner_id FROM agents WHERE id=$1 AND revoked_at IS NULL`, agent).Scan(&owner); err != nil {
		return err
	}
	return s.retainBackgroundFailure(ctx, owner, operation, agent, stage, cause)
}

// Panel failures are terminal records. They never enter dispatch, instance
// locks, capability transitions, retry or accounting.
func (s *Store) RecordPanelFailure(ctx context.Context, input core.PanelFailure) (string, error) {
	scope, ok := requestConfigScope(ctx)
	if !ok {
		return "", ErrForbidden
	}
	if input.Operation == "" || input.Stage == "" || input.Detail == "" || len(input.Detail) > core.MaxTaskDiagnosticBytes || len(input.Operation) > 300 || len(input.Stage) > 100 || len(input.RequestID) > 100 || len(input.Resource) > 4096 || len(input.Code) > 200 || len(input.Field) > 500 || len(input.Summary) > core.MaxTaskDiagnosticBytes {
		return "", ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return "", err
	}
	if input.AgentID != "" {
		if err := requireAgentEngineAccess(ctx, tx, input.AgentID, input.Engine); err != nil {
			return "", err
		}
	}
	// Resolve the actual allocation from the current principal. Never trust a
	// submitted instance ID or merge two allocations by their name or port.
	input.InstanceID = ""
	if input.AgentID != "" && input.Engine != "" {
		shared, err := sharedInstanceRecipient(ctx, tx, input.AgentID)
		if err != nil {
			return "", err
		}
		if shared {
			if err := tx.QueryRow(ctx, `SELECT id FROM agent_shares WHERE user_id=$1 AND agent_id=$2 AND enabled AND status='accepted' AND $3=ANY(engines)`, scope.OwnerID, input.AgentID, input.Engine).Scan(&input.InstanceID); err != nil {
				return "", mapError(err)
			}
		}
	}
	if input.Engine != "" && !input.Engine.Valid() {
		return "", ErrInvalid
	}
	input.Detail = core.DiagnosticText(input.Detail)
	input.Summary = core.DiagnosticText(input.Summary)
	input.Summary = truncate(input.Summary, 4096)
	input.Operation = core.DiagnosticText(input.Operation)
	input.Field = core.DiagnosticText(input.Field)
	input.Resource = core.DiagnosticText(input.Resource)
	input.Stage = core.DiagnosticText(input.Stage)
	input.Code = core.DiagnosticText(input.Code)
	input.RequestID = core.DiagnosticText(input.RequestID)
	if input.OccurredAt.IsZero() {
		input.OccurredAt = time.Now().UTC()
	}
	// The browser appends this request label to the preserved raw error.
	// A fresh request identifier must not turn an identical refresh failure
	// into a new task. Keep the original text in the immutable diagnostic.
	fingerprintDetail := input.Detail
	if input.RequestID != "" {
		fingerprintDetail = strings.ReplaceAll(fingerprintDetail, "（请求 ID："+input.RequestID+"）", "（请求 ID：[request_id]）")
	}
	fingerprint := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%s\x00%s\x00%s\x00%s", input.Operation, input.Resource, input.Stage, fingerprintDetail, input.HTTPStatus, input.AgentID, input.Engine, input.InstanceID, input.Code, input.Field)))
	key := hex.EncodeToString(fingerprint[:])
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope.OwnerID+key); err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `UPDATE tasks SET failure_count=failure_count+1,last_failure_at=now() WHERE id=(
	 SELECT id FROM tasks WHERE owner_id=$1 AND failure_key=$2 AND created_at>now()-interval '5 minutes' ORDER BY created_at DESC LIMIT 1) RETURNING id`, scope.OwnerID, key).Scan(&id)
	if err == nil {
		return id, tx.Commit(ctx)
	}
	if err != pgx.ErrNoRows {
		return "", err
	}
	id, err = core.NewID("tsk")
	if err != nil {
		return "", err
	}
	metadata := input
	metadata.Detail = "" // The full body belongs to error, once per response.
	content, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO tasks(id,owner_id,agent_id,engine,action,status,attempt,error,output,created_at,finished_at,failure_key,last_failure_at,shared_instance,shared_traffic_id)
	 VALUES($1,$2,NULLIF($3,''),$4,'panel-failure','failed',0,$5,$6,now(),now(),$7,now(),$8,$9)`, id, scope.OwnerID, input.AgentID, input.Engine, truncate(input.Summary, 8<<10), "未进入 Agent 执行\n"+input.Operation+"\n阶段："+input.Stage, key, input.InstanceID != "", input.InstanceID)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_diagnostics(task_id,attempt,output,error,source) VALUES($1,0,$2,$3,'panel')`, id, "未进入 Agent 执行\n"+string(content), input.Detail)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func taskAccessClause(ctx context.Context, args *[]any) string {
	access := agentEngineAccessClause(ctx, "tasks.agent_id", "tasks.engine", args)
	if scope, ok := requestConfigScope(ctx); ok {
		*args = append(*args, scope.OwnerID)
		// Private failure ownership does not preserve a revoked resource grant.
		// Unbound panel failures remain available to their submitting user.
		return fmt.Sprintf(" AND ((action='panel-failure' AND owner_id=$%d AND (tasks.agent_id IS NULL OR (EXISTS(SELECT 1 FROM agents failure_agent WHERE failure_agent.id=tasks.agent_id AND failure_agent.revoked_at IS NULL) %s AND (NOT tasks.shared_instance OR EXISTS(SELECT 1 FROM agent_shares failure_share WHERE failure_share.id=tasks.shared_traffic_id AND failure_share.user_id=tasks.owner_id AND failure_share.agent_id=tasks.agent_id AND failure_share.enabled AND failure_share.status='accepted' AND tasks.engine=ANY(failure_share.engines)))))) OR (action<>'panel-failure' %s))", len(*args), access, access)
	}
	return access
}

func (s *Store) TaskDiagnostic(ctx context.Context, id string, selected ...int) (core.TaskDiagnostic, error) {
	task, err := s.GetTask(ctx, id)
	if err != nil {
		return core.TaskDiagnostic{}, err
	}
	var d core.TaskDiagnostic
	attempt := task.Attempt
	if len(selected) > 0 {
		attempt = selected[0]
	}
	if attempt < 0 || attempt > task.Attempt {
		return d, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `SELECT attempt,source,COALESCE(executed_attempt,0) FROM task_diagnostics WHERE task_id=$1 ORDER BY attempt`, id)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var a core.DiagnosticAttempt
		if err = rows.Scan(&a.Attempt, &a.Source, &a.ExecutedAttempt); err != nil {
			return d, err
		}
		d.Attempts = append(d.Attempts, a)
	}
	if err = rows.Err(); err != nil {
		return d, err
	}
	rows.Close()
	err = s.pool.QueryRow(ctx, `SELECT output,error,source FROM task_diagnostics WHERE task_id=$1 AND attempt=$2`, id, attempt).Scan(&d.Output, &d.Error, &d.Source)
	if err != nil && err != pgx.ErrNoRows {
		return d, err
	}
	if err == pgx.ErrNoRows && attempt != task.Attempt {
		return d, ErrNotFound
	}
	if d.Source == "" && task.Status == core.TaskSucceeded && (task.Action == core.ActionReadConfig || task.Action == core.ActionReadManagedConfig) {
		d.Output, d.Source = task.Output, "configuration-snapshot"
	}
	if d.Source == "" {
		d.Output, d.Error, d.Source = task.Output, task.Error, "legacy-agent"
	}
	err = s.pool.QueryRow(ctx, `SELECT failure_count,last_failure_at FROM tasks WHERE id=$1`, id).Scan(&d.Occurrences, &d.LastFailureAt)
	if err != nil {
		return d, err
	}
	_ = s.pool.QueryRow(ctx, `SELECT COALESCE(retry_of,'') FROM task_imports WHERE task_id=$1`, id).Scan(&d.RetryOf)
	return d, nil
}

func (s *Store) UploadTaskDiagnostic(ctx context.Context, agentID, taskID string, c core.TaskDiagnosticChunk) error {
	if c.Index < 0 || c.Count < 1 || c.Count > core.MaxTaskDiagnosticChunks || c.Index >= c.Count || len(c.Content) > core.TaskDiagnosticChunkBytes || len(c.Digest) != 64 {
		return ErrInvalid
	}
	if _, err := hex.DecodeString(c.Digest); err != nil {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT NOT ((`+unauthorizedTaskPrincipalSQL+`) OR (`+unauthorizedHostConfigTaskSQL+`)) FROM tasks t WHERE t.id=$1 AND t.agent_id=$2 AND t.lease_id=$3 AND t.status='running' FOR UPDATE`, taskID, agentID, c.LeaseID).Scan(&allowed); err != nil {
		if err == pgx.ErrNoRows {
			return ErrConflict
		}
		return err
	}
	if !allowed {
		return ErrConflict
	}
	// Bound each leased upload independently. A retransmission replaces only
	// its matching chunk; changed manifests are rejected at completion.
	sealed, err := s.encryptContent(c.Content)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_diagnostic_uploads(task_id,lease_id,chunk_index,chunk_count,digest,content) VALUES($1,$2,$3,$4,$5,$6)
	 ON CONFLICT(task_id,lease_id,chunk_index) DO UPDATE SET chunk_count=EXCLUDED.chunk_count,digest=EXCLUDED.digest,content=EXCLUDED.content`, taskID, c.LeaseID, c.Index, c.Count, c.Digest, sealed)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) uploadedTaskDiagnostic(ctx context.Context, tx pgx.Tx, id, lease, digest string) (core.TaskDiagnostic, error) {
	rows, err := tx.Query(ctx, `SELECT chunk_index,chunk_count,digest,content FROM task_diagnostic_uploads WHERE task_id=$1 AND lease_id=$2 ORDER BY chunk_index`, id, lease)
	if err != nil {
		return core.TaskDiagnostic{}, err
	}
	defer rows.Close()
	var full strings.Builder
	index, total := 0, 0
	for rows.Next() {
		var i, n int
		var hash, value string
		if err = rows.Scan(&i, &n, &hash, &value); err != nil {
			return core.TaskDiagnostic{}, err
		}
		value, err = s.decryptContent(value)
		if err != nil {
			return core.TaskDiagnostic{}, err
		}
		if i != index || hash != digest || total != 0 && n != total || full.Len()+len(value) > core.MaxEncodedTaskDiagnosticBytes {
			return core.TaskDiagnostic{}, ErrInvalid
		}
		total = n
		index++
		full.WriteString(value)
	}
	if err = rows.Err(); err != nil {
		return core.TaskDiagnostic{}, err
	}
	hash := sha256.Sum256([]byte(full.String()))
	if index == 0 || index != total || hex.EncodeToString(hash[:]) != digest {
		return core.TaskDiagnostic{}, fmt.Errorf("%w: incomplete task diagnostic upload", ErrConflict)
	}
	var d core.TaskDiagnostic
	err = json.Unmarshal([]byte(full.String()), &d)
	if len(d.Output) > core.MaxTaskDiagnosticBytes/2+512 || len(d.Error) > core.MaxTaskDiagnosticBytes/2+512 {
		return core.TaskDiagnostic{}, ErrInvalid
	}
	return d, err
}
