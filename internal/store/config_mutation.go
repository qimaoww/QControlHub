package store

import (
	"context"
	"fmt"
	"regexp"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

var configMutationTaskID = regexp.MustCompile(`^tsk_[a-f0-9]{16}$`)

type ConfigMutationOptions struct {
	ManagementState  *serverconfig.ManagedState
	TaskID           string
	Action           core.Action
	ClientMetadata   *ConfigClientMetadataMutation
	InstallIfMissing bool
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
	saved, err := s.saveAgentConfigTx(ctx, tx, input, expectedVersion, options.ClientMetadata, management...)
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	if options.MainlandPolicies != nil {
		if err := s.replaceMainlandAccessPoliciesTx(ctx, tx, input.AgentID, saved.Version, options.MainlandPolicies); err != nil {
			return core.Config{}, core.Task{}, err
		}
	}
	task, err := s.createTaskTx(ctx, tx, core.TaskRequest{
		MutationTaskID: options.TaskID, AgentID: saved.AgentID, Engine: saved.Engine, Action: options.Action,
		ConfigID: saved.ID, ExpectedConfigVersion: saved.Version,
		InstallIfMissing: options.InstallIfMissing,
	}, 0)
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Config{}, core.Task{}, err
	}
	s.signalTaskReady(saved.AgentID)
	return saved, task, nil
}
