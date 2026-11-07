package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/notify"
)

func (s *Store) importNotificationTasks(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT t.id,t.attempt,t.owner_id,t.agent_id,a.owner_id,a.name,t.engine,t.action,t.error,t.finished_at,COALESCE(t.config_id,''),t.config_version,t.shared_traffic_id
 FROM tasks t JOIN agents a ON a.id=t.agent_id WHERE t.status='failed' AND t.finished_at>GREATEST(now()-interval '30 days',(SELECT min(created_at) FROM notification_configs))
 AND t.notification_failure_attempt<t.attempt ORDER BY t.finished_at FOR UPDATE OF t SKIP LOCKED LIMIT 100`)
	if err != nil {
		return err
	}
	type source struct {
		owner, key, agentOwner string
		event                  notify.Event
	}
	items := []source{}
	for rows.Next() {
		var v source
		var attempt int
		if err = rows.Scan(&v.event.TaskID, &attempt, &v.owner, &v.event.AgentID, &v.agentOwner, &v.event.Agent, &v.event.Engine, &v.event.Action, &v.event.Error, &v.event.Time, &v.event.ConfigID, &v.event.ConfigVersion, &v.event.ShareID); err != nil {
			rows.Close()
			return err
		}
		v.key = fmt.Sprintf("task:%s:%d", v.event.TaskID, attempt)
		v.event.Type = notify.TypeTaskFailed
		v.event.Error = notify.Redact(v.event.Error)
		v.event.Message = "任务失败：" + v.event.Engine + " / " + v.event.Action
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Sequence IDs are consumed only while holding the publication lock until
	// commit. Otherwise a later committed ID could move subscriber cursors past
	// an earlier, still uncommitted event on another server instance.
	if len(items) > 0 {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(91823411)`); err != nil {
			return err
		}
	}
	for _, v := range items {
		raw, er := json.Marshal(v.event)
		if er != nil {
			return er
		}
		if _, err = tx.Exec(ctx, `INSERT INTO notification_events(source_key,owner_id,resource_owner,event) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, v.key, v.owner, v.agentOwner, raw); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE tasks SET notification_failure_attempt=attempt WHERE id=$1`, v.event.TaskID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
