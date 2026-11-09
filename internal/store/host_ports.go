package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func recordHostPortsTx(ctx context.Context, tx pgx.Tx, agentID, taskID string, observation core.HostPortObservation) error {
	if len(observation.Ports) > 65535 || observation.ObservedAt.IsZero() || observation.ObservedAt.After(time.Now().UTC().Add(time.Second)) {
		return fmt.Errorf("%w: invalid host port observation", ErrInvalid)
	}
	for _, port := range observation.Ports {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%w: invalid observed port", ErrInvalid)
		}
	}
	if observation.Ports == nil {
		return fmt.Errorf("%w: incomplete host port observation", ErrInvalid)
	}
	_, err := tx.Exec(ctx, `INSERT INTO agent_host_port_observations(agent_id,task_id,observed_at,ports)
		SELECT agent_id,id,$3,$4 FROM tasks WHERE id=$2 AND agent_id=$1 AND action='status'
		AND started_at<=$3 AND $3>now()-interval '15 seconds'
		ON CONFLICT(agent_id) DO UPDATE SET task_id=excluded.task_id,observed_at=excluded.observed_at,ports=excluded.ports
		WHERE agent_host_port_observations.observed_at<excluded.observed_at`, agentID, taskID, observation.ObservedAt, observation.Ports)
	return err
}

// Updating a verified instance may keep its own existing listeners. The host
// socket table cannot attribute sockets, so only a retained, confirmed identity
// of this exact owner/configuration/instance can provide that exception.
func (s *Store) retainedCandidatePortsTx(ctx context.Context, tx pgx.Tx, task *core.Task, ownerID string) (map[int]bool, error) {
	result := map[int]bool{}
	if task == nil || task.ConfigID == "" {
		return result, nil
	}
	var content string
	var version int
	err := tx.QueryRow(ctx, `SELECT COALESCE(revision.content,CASE WHEN config.version=state.config_version THEN config.content END,''),state.config_version
		FROM (
		 SELECT config_id,config_version,owner_id,config_uncertain FROM agent_engine_ownership
		 WHERE agent_id=$1 AND engine=$2 AND NOT $3::boolean
		 UNION ALL SELECT config_id,config_version,owner_id,config_uncertain FROM agent_shared_instance_ownership
		 WHERE agent_id=$1 AND engine=$2 AND $3::boolean AND share_id=$4
		) state JOIN configs config ON config.id=state.config_id AND config.owner_id=state.owner_id
		LEFT JOIN config_revisions revision ON revision.config_id=state.config_id AND revision.version=state.config_version
		WHERE state.owner_id=$5 AND state.config_id=$6 AND NOT state.config_uncertain AND config.deleted_at IS NULL`, task.AgentID, task.Engine, task.SharedInstance, task.SharedTrafficID, ownerID, task.ConfigID).Scan(&content, &version)
	if err == pgx.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	content, err = s.decryptContent(content)
	if err != nil {
		return nil, err
	}
	content, err = s.compileConfigManagementRuntime(ctx, tx, task.ConfigID, version, task.Engine, content)
	if err != nil {
		return nil, err
	}
	for _, endpoint := range serverconfig.DiscoverTrafficPorts(task.Engine, content) {
		result[endpoint.Port] = true
	}
	frontends, err := s.managedTLSFrontendsTx(ctx, tx, task.ConfigID, version, task.Engine, content)
	if err != nil {
		return nil, err
	}
	for _, frontend := range frontends {
		result[frontend.PublicPort] = true
		if frontend.LocalOnly {
			result[frontend.BackendPort] = true
		}
	}
	return result, nil
}

// A short-lived, authenticated status observation can resolve port safety
// independently of legacy configuration identity. It grants no read access or
// ownership and is invalidated by any intervening service mutation or queue.
func observedHostPortsTx(ctx context.Context, tx pgx.Tx, agentID, candidateID string) (map[int]bool, error) {
	var ports []int
	err := tx.QueryRow(ctx, `SELECT observation.ports FROM agent_host_port_observations observation
		JOIN tasks proof ON proof.id=observation.task_id AND proof.agent_id=observation.agent_id
		WHERE observation.agent_id=$1 AND proof.action='status' AND proof.status='succeeded'
		AND observation.observed_at>now()-interval '15 seconds'
		AND NOT EXISTS(SELECT 1 FROM tasks changed WHERE changed.agent_id=$1 AND changed.id<>$2
			AND (changed.action IN ('deploy','import-existing','start','stop','restart','install','uninstall','upgrade-agent') OR changed.install_if_missing)
			AND (changed.status IN ('pending','running') OR changed.started_at>=proof.started_at))`, agentID, candidateID).Scan(&ports)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := map[int]bool{}
	for _, port := range ports {
		result[port] = true
	}
	return result, nil
}
