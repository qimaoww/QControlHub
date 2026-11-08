package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/notify"
)

type NotificationUpdate struct {
	PublishedAt *time.Time `json:"published_at"`
	Version     string     `json:"version"`
	Error       string     `json:"error"`
	CheckedAt   time.Time  `json:"checked_at"`
	NextAt      time.Time  `json:"next_at"`
}

func (s *Store) NotificationUpdateStatus(ctx context.Context) (NotificationUpdate, error) {
	var v NotificationUpdate
	err := s.pool.QueryRow(ctx, `SELECT version,error,checked_at,next_at,published_at FROM notification_update_cache WHERE id=1`).Scan(&v.Version, &v.Error, &v.CheckedAt, &v.NextAt, &v.PublishedAt)
	return v, err
}

// RefreshNotificationUpdate shares one leased GHCR request across subscribers and manual checks.
func (s *Store) RefreshNotificationUpdate(ctx context.Context, manual bool, fetch func(context.Context) (string, time.Time, error)) (NotificationUpdate, error) {
	gate := "next_at<=now()"
	if manual {
		gate = "checked_at<now()-interval '1 minute'"
	}
	tag, err := s.pool.Exec(ctx, `UPDATE notification_update_cache SET lease_until=now()+interval '30 seconds' WHERE id=1 AND (lease_until IS NULL OR lease_until<now()) AND `+gate)
	if err != nil {
		return NotificationUpdate{}, err
	}
	if tag.RowsAffected() == 0 {
		return s.NotificationUpdateStatus(ctx)
	}
	version, published, fetchErr := fetch(ctx)
	failure := ""
	if fetchErr != nil {
		failure = "检查 GHCR 最新镜像失败"
	}
	delay := "1 hour"
	if failure != "" {
		delay = "15 minutes"
	}
	_, err = s.pool.Exec(ctx, `UPDATE notification_update_cache SET version=CASE WHEN $2='' THEN $1 ELSE version END,error=$2,checked_at=now(),next_at=now()+$3::interval,lease_until=NULL,published_at=CASE WHEN $2='' THEN $4 ELSE published_at END WHERE id=1`, version, failure, delay, published)
	if err != nil {
		return NotificationUpdate{}, err
	}
	return s.NotificationUpdateStatus(ctx)
}
func (s *Store) NotificationUpdateDue(ctx context.Context) bool {
	var due bool
	_ = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_rule_states WHERE resource='update' AND (state->>'last')::timestamptz<=now())`).Scan(&due)
	return due
}
func (s *Store) scanUpdateRule(ctx context.Context, tx pgx.Tx, owner string, c notify.Configuration, r notify.Rule) error {
	st, err := readNotificationState(ctx, tx, owner, r.ID, "update")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !st.Initialized {
		st.Initialized = true
		st.Last = now
		st.Since = now
	}
	var latest, current string
	var checked time.Time
	var failure string
	err = tx.QueryRow(ctx, `SELECT version,error,checked_at FROM notification_update_cache WHERE id=1`).Scan(&latest, &failure, &checked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Cache version contains full source identity. API code writes the comparison result for its running build.
	current = latest
	var available bool
	_ = tx.QueryRow(ctx, `SELECT COALESCE((state->>'active')::boolean,false) FROM notification_rule_states WHERE owner_id='' AND rule_id='_update-comparison' AND resource=$1`, current).Scan(&available)
	if failure == "" && available && checked.After(st.Since) && latest != "" && latest != st.Identity && !now.Before(st.Last) {
		e := notify.Event{Type: "panel.update", Time: now, Version: latest, Message: "面板发现可用更新：" + latest}
		if err = s.queueNotification(ctx, tx, owner, c, r, e, "source:update:"+latest, ""); err != nil {
			return err
		}
		st.Identity = latest
	}
	if !now.Before(st.Last) && failure == "" && checked.After(st.Since) {
		st.Last = now.Add(time.Duration(r.Interval) * time.Second)
	}
	return writeNotificationState(ctx, tx, owner, r.ID, "update", st)
}
func (s *Store) SetNotificationUpdateComparison(ctx context.Context, version string, available bool) error {
	_, err := s.pool.Exec(ctx, `WITH removed AS (DELETE FROM notification_rule_states WHERE owner_id='' AND rule_id='_update-comparison' AND resource<>$1) INSERT INTO notification_rule_states(owner_id,rule_id,resource,state) VALUES('','_update-comparison',$1,jsonb_build_object('active',$2::boolean)) ON CONFLICT(owner_id,rule_id,resource) DO UPDATE SET state=EXCLUDED.state`, version, available)
	return err
}
