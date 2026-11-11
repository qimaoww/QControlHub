package store

import (
	"context"
	"fmt"
	"regexp"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

var configMutationTaskID = regexp.MustCompile(`^tsk_[a-f0-9]{16}$`)

type unchangedBindingTaskKey struct{}

type ConfigMutationOptions struct {
	ReuseUnchangedBinding bool
	ManagementState       *serverconfig.ManagedState
	TaskID                string
	Action                core.Action
	ClientMetadata        *ConfigClientMetadataMutation
	InstallIfMissing      bool
	ExpectedInstanceID    *string
	// nil preserves existing policies; a non-nil empty slice clears them.
	MainlandPolicies []core.MainlandAccessPolicy
}

// SaveAgentConfigAndTask commits the revision, client-only secrets, access
// policies and exact task snapshot together. A failed task never leaves an
// apparently failed save behind, and Agents are only woken after commit.
func (s *Store) SaveAgentConfigAndTask(ctx context.Context, input core.Config, expectedVersion int, options ConfigMutationOptions) (core.Config, core.Task, error) {
	if options.TaskID != "" && !configMutationTaskID.MatchString(options.TaskID) {
		return core.Config{}, core.Task{}, fmt.Errorf("%w: invalid configuration task identifier", ErrInvalid)
	}
	if options.Action != core.ActionValidate && options.Action != core.ActionDeploy {
		return core.Config{}, core.Task{}, fmt.Errorf("%w: preset intent must be validate or deploy", ErrInvalid)
	}
	if options.MainlandPolicies != nil && input.Engine != core.EngineShadowsocksRust {
		return core.Config{}, core.Task{}, fmt.Errorf("%w: external policies are only supported for ss-rust", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	defer tx.Rollback(ctx)
	var management []serverconfig.ManagedState
	if options.ManagementState != nil {
		management = []serverconfig.ManagedState{*options.ManagementState}
	}
	// Persist editable originals. Generated exits are execution artifacts and
	// must never become user outbounds in the next edit or copy.
	source, err := serverconfig.PresetAccountingSource(input.Engine, input.Content)
	if err != nil {
		return core.Config{}, core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	input.Content = source
	saved := input
	unchanged := false
	if options.ReuseUnchangedBinding {
		if err := lockAgentUser(ctx, tx); err != nil {
			return core.Config{}, core.Task{}, err
		}
		if err := requireAgentEngineAccess(ctx, tx, input.AgentID, input.Engine); err != nil {
			return core.Config{}, core.Task{}, err
		}
		// Match task creation's user -> Agent -> configuration lock order.
		var agentID string
		if err := tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, input.AgentID).Scan(&agentID); err != nil {
			return core.Config{}, core.Task{}, mapError(err)
		}
		var id, content string
		var version int
		if err := tx.QueryRow(ctx, `SELECT id,content,version FROM configs WHERE agent_id=$1 AND engine=$2 AND owner_id=$3 AND deleted_at IS NULL FOR UPDATE`, input.AgentID, input.Engine, scopeForConfig(ctx).OwnerID).Scan(&id, &content, &version); err != nil {
			return core.Config{}, core.Task{}, mapError(err)
		}
		if id != input.ID || version != expectedVersion {
			return core.Config{}, core.Task{}, ErrConflict
		}
		content, err = s.decryptContent(content)
		if err != nil {
			return core.Config{}, core.Task{}, err
		}
		unchanged = content == input.Content
	}
	if !unchanged {
		saved, err = s.saveAgentConfigTx(ctx, tx, input, expectedVersion, options.ClientMetadata, management...)
		if err != nil {
			return core.Config{}, core.Task{}, err
		}
	} else {
		ctx = context.WithValue(ctx, unchangedBindingTaskKey{}, true)
	}
	if options.MainlandPolicies != nil {
		if err := s.replaceMainlandAccessPoliciesTx(ctx, tx, input.AgentID, saved.Version, options.MainlandPolicies); err != nil {
			return core.Config{}, core.Task{}, err
		}
	}
	if options.TaskID != "" {
		// A submission identifier belongs to either its task or its reuse
		// receipt, including when different accounts submit concurrently.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,81083))`, options.TaskID); err != nil {
			return core.Config{}, core.Task{}, err
		}
	}
	task, err := s.createTaskTx(ctx, tx, core.TaskRequest{
		MutationTaskID: options.TaskID, AgentID: saved.AgentID, Engine: saved.Engine, Action: options.Action,
		ConfigID: saved.ID, ExpectedConfigVersion: saved.Version,
		InstallIfMissing:   options.InstallIfMissing,
		ExpectedInstanceID: options.ExpectedInstanceID,
	}, 0)
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	if options.TaskID != "" && task.ID != options.TaskID {
		keyID, _ := apiKeyTaskIdentity(ctx)
		result, err := tx.Exec(ctx, `INSERT INTO config_binding_receipts(submission_id,task_id,owner_id,api_key_id)
		 SELECT $1,$2,$3,$4 WHERE NOT EXISTS(SELECT 1 FROM tasks WHERE id=$1)
		 ON CONFLICT(submission_id) DO UPDATE SET task_id=EXCLUDED.task_id
		 WHERE config_binding_receipts.task_id=EXCLUDED.task_id AND config_binding_receipts.owner_id=EXCLUDED.owner_id AND config_binding_receipts.api_key_id=EXCLUDED.api_key_id`, options.TaskID, task.ID, scopeForConfig(ctx).OwnerID, keyID)
		if err != nil {
			return core.Config{}, core.Task{}, err
		}
		if result.RowsAffected() != 1 {
			return core.Config{}, core.Task{}, ErrConflict
		}
		task.SubmissionID = options.TaskID
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Config{}, core.Task{}, err
	}
	s.signalTaskReadyAfterCommit(ctx, saved.AgentID)
	return saved, task, nil
}
