package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type TLSMigrationOperation struct {
	TaskID string                   `json:"task_id,omitempty"`
	Plan   TLSFrontendPreview       `json:"plan"`
	State  string                   `json:"state"`
	Result *core.TLSMigrationResult `json:"result,omitempty"`
}

func rejectPendingTLSMigration(ctx context.Context, tx pgx.Tx, node string) error {
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE agent_id=$1 AND action='migrate-tls' AND status IN ('pending','running'))`, node).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return fmt.Errorf("%w: TLS 前端迁移处理中", ErrConflict)
	}
	return nil
}

func (s *Store) SubmitTLSMigration(ctx context.Context, agent core.Agent, engine core.Engine, tag string, input TLSFrontendSelection, planID, taskID string) (TLSMigrationOperation, error) {
	var op TLSMigrationOperation
	if !configMutationTaskID.MatchString(taskID) || len(planID) != 64 {
		return op, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return op, err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return op, err
	}
	if err = requireAgentEngineAccess(ctx, tx, agent.ID, engine); err != nil {
		return op, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM agents WHERE id=$1 FOR UPDATE`, agent.ID); err != nil {
		return op, err
	}
	key, permissions := apiKeyTaskIdentity(ctx)
	owner := scopeForConfig(ctx).OwnerID
	var oldID string
	err = tx.QueryRow(ctx, `SELECT t.id FROM tasks t JOIN tls_migrations m ON m.task_id=t.id WHERE t.agent_id=$1 AND t.owner_id=$2 AND t.api_key_id=$3 AND ((m.plan_id=$4 AND (t.status IN ('pending','running','succeeded') OR m.result->>'state'='recovery_incomplete')) OR t.id=$5) ORDER BY t.created_at DESC LIMIT 1`, agent.ID, owner, key, planID, taskID).Scan(&oldID)
	if err == nil {
		op, err = s.readTLSMigration(ctx, tx, agent.ID, oldID)
		if err == nil {
			selected := slices.ContainsFunc(op.Plan.Members, func(m TLSFrontendMember) bool {
				return input.ExpectedInstanceID != nil && m.InstanceID == *input.ExpectedInstanceID && m.Engine == engine && m.Tag == tag && m.Version == input.ExpectedVersion
			})
			if op.Plan.PlanID != planID || input.TargetMode != op.Plan.TargetMode || !selected {
				return TLSMigrationOperation{}, ErrConflict
			}
		}
		return op, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return op, err
	}
	op.Plan, err = s.previewTLSFrontendTx(ctx, tx, agent, engine, tag, input)
	if err != nil {
		return op, err
	}
	if op.Plan.PlanID != planID {
		return op, &PortConflictError{Field: "plan_id", Reason: "stale_plan", Cause: ErrConflict}
	}
	if len(op.Plan.Blockers) > 0 {
		return op, &PortConflictError{Field: "plan_id", Port: op.Plan.PublicPort, Reason: op.Plan.Blockers[0], Cause: ErrConflict}
	}
	if !op.Plan.ConnectionInterruptionPossible {
		op.State = "unchanged"
		return op, nil
	}
	execution := core.TLSMigration{ID: taskID, Port: op.Plan.PublicPort, Mode: input.TargetMode}
	for _, m := range op.Plan.Members {
		if err = requireAgentEngineAccess(ctx, tx, agent.ID, m.Engine); err != nil {
			return op, ErrForbidden
		}
		execution.Routes = append(execution.Routes, core.TLSMigrationRoute{Engine: m.Engine, InstanceID: m.InstanceID, Tag: m.Tag, Path: m.Path, Domain: m.Domain, Transport: m.Transport, BackendPort: m.BackendPort, CertificateID: m.CertificateID, CertificateVersion: m.CertificateVersion, GrantGeneration: m.GrantGeneration, From: m.From})
	}
	_, err = tx.Exec(ctx, `INSERT INTO tasks(id,agent_id,action,engine,status,created_at,owner_id,api_key_id,api_key_permissions) VALUES($1,$2,'migrate-tls','','pending',now(),$3,$4,$5)`, taskID, agent.ID, owner, key, permissions)
	if err != nil {
		return op, mapError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO tls_migrations(task_id,plan_id,preview,execution) VALUES($1,$2,$3,$4)`, taskID, planID, op.Plan, execution)
	if err != nil {
		return op, err
	}
	if err = tx.Commit(ctx); err != nil {
		return op, err
	}
	s.signalTaskReadyAfterCommit(ctx, agent.ID)
	op.TaskID = taskID
	op.State = "pending"
	return op, nil
}

func (s *Store) readTLSMigration(ctx context.Context, db storeExecutor, node, id string) (TLSMigrationOperation, error) {
	var op TLSMigrationOperation
	err := db.QueryRow(ctx, `SELECT t.id,CASE WHEN t.status='running' AND m.phase<>'' THEN m.phase ELSE t.status END,m.preview,m.result FROM tasks t JOIN tls_migrations m ON m.task_id=t.id WHERE t.id=$1 AND t.agent_id=$2 AND t.owner_id=$3`, id, node, scopeForConfig(ctx).OwnerID).Scan(&op.TaskID, &op.State, &op.Plan, &op.Result)
	if err != nil {
		return op, mapError(err)
	}
	for _, m := range op.Plan.Members {
		if err = requireAgentEngineAccess(ctx, db, node, m.Engine); err != nil {
			return TLSMigrationOperation{}, ErrNotFound
		}
	}
	if op.Result != nil {
		op.State = op.Result.State
	}
	return op, nil
}
func (s *Store) TLSMigrationStatus(ctx context.Context, node, id string) (TLSMigrationOperation, error) {
	return s.readTLSMigration(ctx, s.pool, node, id)
}

func (s *Store) openTLSMigrationTx(ctx context.Context, tx pgx.Tx, task *core.Task, features []string) error {
	if !slices.Contains(features, core.AgentFeatureTLSMigration) {
		return ErrConflict
	}
	var preview TLSFrontendPreview
	var execution core.TLSMigration
	var owner string
	if err := tx.QueryRow(ctx, `SELECT t.owner_id,m.preview,m.execution FROM tls_migrations m JOIN tasks t ON t.id=m.task_id WHERE t.id=$1`, task.ID).Scan(&owner, &preview, &execution); err != nil {
		return err
	}
	for _, m := range preview.Members {
		var current bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM configs WHERE id=$1 AND agent_id=$2 AND owner_id=$3 AND version=$4 AND deleted_at IS NULL) AND
		EXISTS(SELECT 1 FROM certificate_bindings b JOIN panel_certificates c ON b.certificate_id=c.id WHERE b.config_id=$1 AND b.enabled AND b.config_version=$4 AND c.id=$5 AND b.version=$6 AND c.version=$6)`, m.ConfigID, task.AgentID, owner, m.Version, m.CertificateID, m.CertificateVersion).Scan(&current); err != nil {
			return err
		}
		if !current {
			return ErrConflict
		}
		if err := certificateUseTx(ctx, tx, m.CertificateID, owner); err != nil {
			return err
		}
		if m.InstanceID != "" {
			var active bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_shares WHERE id=$1 AND agent_id=$2 AND user_id=$3 AND enabled AND status='accepted' AND $4=ANY(engines) AND (limit_bytes=0 OR used_bytes<limit_bytes))`, m.InstanceID, task.AgentID, owner, m.Engine).Scan(&active); err != nil {
				return err
			}
			g, allowed, err := proxyIngressGrantTx(ctx, tx, task.AgentID, owner, core.TLSFrontend{Mode: m.To, PublicPort: preview.PublicPort})
			if err != nil {
				return err
			}
			if !active || !allowed || g != m.GrantGeneration {
				return ErrForbidden
			}
		}
	}
	task.TLSMigration = &execution
	return nil
}

// Publish a new revision only after all routes have authenticated. Everything
// referenced by the previous revision stays available for historical reads.
func (s *Store) finishTLSMigrationTx(ctx context.Context, tx pgx.Tx, node, id string, result *core.TLSMigrationResult, success bool) (bool, error) {
	var plan TLSFrontendPreview
	if err := tx.QueryRow(ctx, `SELECT preview FROM tls_migrations WHERE task_id=$1`, id).Scan(&plan); err != nil {
		return false, err
	}
	valid := result != nil && len(result.Paths) == len(plan.Members) && slices.Contains([]string{"succeeded", "failed", "rolled_back", "recovery_incomplete"}, result.State)
	if valid {
		valid = slices.Contains([]string{"", "rollback_failed", "port_group_incomplete", "dependency_missing", "business_validation_failed"}, result.Code)
		valid = valid && slices.Contains([]string{"preflight", "handoff", "verify", "restore", "complete"}, result.Step)
		for _, r := range result.Paths {
			valid = valid && slices.Contains([]string{"unknown", "succeeded", "failed"}, r.Candidate) && slices.Contains([]string{"unknown", "succeeded", "failed"}, r.Recovery) && (result.State != "succeeded" || r.Candidate == "succeeded") && (result.State != "rolled_back" || r.Recovery == "succeeded")
		}
	}
	if !valid || (result.State == "succeeded") != success {
		result = &core.TLSMigrationResult{State: "recovery_incomplete", Step: "verify", Code: "rollback_failed", Paths: make([]core.TLSRouteVerification, len(plan.Members))}
		for i := range result.Paths {
			result.Paths[i] = core.TLSRouteVerification{Candidate: "unknown", Recovery: "unknown"}
		}
		success = false
	}
	if success {
		savepoint, err := tx.Begin(ctx)
		if err != nil {
			return false, err
		}
		err = s.publishTLSMigration(ctx, savepoint, plan, id)
		if err != nil {
			if e := savepoint.Rollback(ctx); e != nil {
				return false, e
			}
			if !errors.Is(err, ErrConflict) {
				return false, err
			}
			result.State = "recovery_incomplete"
			result.Step = "complete"
			success = false
		} else if err = savepoint.Commit(ctx); err != nil {
			return false, err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE tls_migrations SET result=$2 WHERE task_id=$1`, id, result)
	return success, err
}

func (s *Store) publishTLSMigration(ctx context.Context, tx pgx.Tx, plan TLSFrontendPreview, operation string) error {
	groups := map[string][]TLSFrontendMember{}
	for _, m := range plan.Members {
		if m.From != m.To {
			groups[m.ConfigID] = append(groups[m.ConfigID], m)
		}
	}
	for id, rows := range groups {
		m := rows[0]
		var config core.Config
		err := tx.QueryRow(ctx, `UPDATE configs SET version=version+1,updated_at=now() WHERE id=$1 AND version=$2 AND deleted_at IS NULL RETURNING id,agent_id,name,description,owner_id,engine,content,version,updated_at`, id, m.Version).Scan(&config.ID, &config.AgentID, &config.Name, &config.Description, &config.OwnerID, &config.Engine, &config.Content, &config.Version, &config.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		config.Content, err = s.decryptContent(config.Content)
		if err != nil {
			return err
		}
		if err = s.insertConfigRevision(ctx, tx, config); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO config_client_metadata(config_id,config_version,profile_tag,content,created_at) SELECT config_id,$3,profile_tag,content,now() FROM config_client_metadata WHERE config_id=$1 AND config_version=$2`, id, m.Version, config.Version); err != nil {
			return err
		}
		for _, member := range rows {
			var stored string
			if err = tx.QueryRow(ctx, `SELECT content FROM config_client_metadata WHERE config_id=$1 AND config_version=$2 AND profile_tag=$3`, id, config.Version, member.Tag).Scan(&stored); err != nil {
				return err
			}
			raw, err := s.decryptContent(stored)
			if err != nil {
				return err
			}
			var doc, frontend map[string]json.RawMessage
			if err = json.Unmarshal([]byte(raw), &doc); err != nil {
				return err
			}
			if err = json.Unmarshal(doc["tls_frontend"], &frontend); err != nil {
				return err
			}
			frontend["mode"], _ = json.Marshal(plan.TargetMode)
			doc["tls_frontend"], err = json.Marshal(frontend)
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(doc)
			if err != nil {
				return err
			}
			stored, err = s.encryptContent(string(encoded))
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE config_client_metadata SET content=$4 WHERE config_id=$1 AND config_version=$2 AND profile_tag=$3`, id, config.Version, member.Tag, stored); err != nil {
				return err
			}
		}
		// Existing deployment projections resolve a completed deploy receipt.
		// Preserve that contract, including immutable certificate versions.
		receipt, err := core.NewID("tsk")
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO tasks(id,agent_id,action,engine,status,created_at,started_at,finished_at,config_id,config_version,owner_id,shared_instance,shared_traffic_id,api_key_id,api_key_permissions,output) SELECT $2,agent_id,'deploy',$3,'succeeded',created_at,started_at,now(),$4,$5,owner_id,$6,$7,api_key_id,api_key_permissions,$8 FROM tasks WHERE id=$1`, operation, receipt, m.Engine, id, config.Version, m.InstanceID != "", m.InstanceID, "TLS frontend migration "+operation)
		if err != nil {
			return err
		}
		table := "agent_engine_ownership"
		where := ""
		args := []any{plan.AgentID, m.Engine, id, m.Version, config.Version}
		set := "config_version=$5,updated_at=now()"
		if m.InstanceID != "" {
			table = "agent_shared_instance_ownership"
			where = " AND share_id=$6"
			args = append(args, m.InstanceID, receipt)
			args = append(args, plan.PublicPort, m.To, m.GrantGeneration)
			set += `,applied_task_id=$7,deployed_at=now(),applied_proxy_ingresses=(
				SELECT COALESCE(jsonb_agg(g),'[]'::jsonb) FROM jsonb_array_elements(applied_proxy_ingresses) g
				WHERE (g->>'port')::int IS DISTINCT FROM $8::int
			) || jsonb_build_array(jsonb_build_object('mode',$9::text,'port',$8::int,'generation',$10::bigint))`
		}
		res, err := tx.Exec(ctx, "UPDATE "+table+" SET "+set+" WHERE agent_id=$1 AND engine=$2 AND config_id=$3 AND config_version=$4"+where, args...)
		if err != nil {
			return err
		}
		if res.RowsAffected() != 1 {
			return ErrConflict
		}
		if _, err = tx.Exec(ctx, `INSERT INTO certificate_task_versions(task_id,certificate_id,version) SELECT $3,certificate_id,version FROM certificate_bindings WHERE config_id=$1 AND config_version=$2 AND enabled`, id, m.Version, receipt); err != nil {
			return err
		}
		for _, table := range []string{"port_traffic_policies", "certificate_bindings"} {
			if _, err = tx.Exec(ctx, "UPDATE "+table+" SET config_version=$3 WHERE config_id=$1 AND config_version=$2", id, m.Version, config.Version); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) RecordTLSMigrationPhase(ctx context.Context, node string, p core.TLSMigrationProgress) error {
	if p.Phase != "verifying" && p.Phase != "rolling_back" {
		return ErrInvalid
	}
	_, err := s.pool.Exec(ctx, `UPDATE tls_migrations m SET phase=$4 FROM tasks t WHERE m.task_id=t.id AND t.id=$1 AND t.agent_id=$2 AND t.lease_id=$3 AND t.status='running'`, p.TaskID, node, p.LeaseID, p.Phase)
	return err
}
