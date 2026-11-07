package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/notify"
)

type notificationState struct {
	Initialized bool      `json:"initialized"`
	Active      bool      `json:"active"`
	Since       time.Time `json:"since"`
	Last        time.Time `json:"last"`
	Sample      time.Time `json:"sample"`
	Identity    string    `json:"identity"`
	Used        uint64    `json:"used"`
	Sequence    int64     `json:"sequence"`
}

func readNotificationState(ctx context.Context, tx pgx.Tx, owner, rule, resource string) (notificationState, error) {
	var v notificationState
	err := tx.QueryRow(ctx, `SELECT state FROM notification_rule_states WHERE owner_id=$1 AND rule_id=$2 AND resource=$3`, owner, rule, resource).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return v, err
}
func writeNotificationState(ctx context.Context, tx pgx.Tx, owner, rule, resource string, v notificationState) error {
	b, _ := json.Marshal(v)
	_, err := tx.Exec(ctx, `INSERT INTO notification_rule_states(owner_id,rule_id,resource,state) VALUES($1,$2,$3,$4) ON CONFLICT(owner_id,rule_id,resource) DO UPDATE SET state=EXCLUDED.state`, owner, rule, resource, b)
	return err
}
func (s *Store) queueNotification(ctx context.Context, tx pgx.Tx, owner string, c notify.Configuration, r notify.Rule, e notify.Event, key, resourceOwner string) error {
	// Backpressure aborts the transaction, preserving source cursors and rule states for retry.
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM notification_deliveries WHERE owner_id=$1 AND status IN ('pending','sending') LIMIT 1000) q`, owner).Scan(&count); err != nil {
		return err
	}
	if count+len(r.Channels) > 1000 {
		return errors.New("notification queue capacity reached")
	}
	e.Error = notify.Redact(e.Error)
	if !strings.HasPrefix(key, "source:") {
		key = fmt.Sprintf("%d:%s", c.Revision, key)
	}
	authorization, err := s.notificationAuthorization(ctx, tx, owner)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	for _, id := range r.Channels {
		enabled := false
		for _, ch := range c.Channels {
			if ch.ID == id && ch.Enabled {
				enabled = true
			}
		}
		if !enabled {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO notification_deliveries(owner_id,rule_id,channel_id,dedupe,event,resource_owner,principal_stamp) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, owner, r.ID, id, key, raw, resourceOwner, authorization); err != nil {
			return err
		}
	}
	return nil
}

// ScanNotifications does bounded database work only. External delivery never holds a scanner lock.
func (s *Store) ScanNotifications(ctx context.Context) error {
	if err := s.importNotificationTasks(ctx); err != nil {
		return err
	}

	for i := 0; i < 20; i++ {
		processed, err := s.scanNotificationOwner(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("scan notification subscriber", "error", err)
			continue
		}
		if !processed {
			break
		}
	}
	return nil
}
func (s *Store) scanNotificationOwner(ctx context.Context) (processed bool, scanErr error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var owner string
	var cursor int64
	var created time.Time
	err = tx.QueryRow(ctx, `SELECT owner_id,cursor,created_at FROM notification_configs WHERE scanned_at<now()-interval '10 seconds' ORDER BY scanned_at,owner_id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&owner, &cursor, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() {
		if scanErr != nil {
			tx.Rollback(ctx)
			_, _ = s.pool.Exec(ctx, `UPDATE notification_configs SET scanned_at=now() WHERE owner_id=$1`, owner)
		}
	}()
	c, err := s.notificationConfig(ctx, tx, owner, false)
	if err != nil {
		return false, err
	}
	if s.notificationActive(ctx, tx, owner) {
		rows, err := tx.Query(ctx, `SELECT id,owner_id,event,resource_owner,source_key FROM notification_events WHERE id>$1 ORDER BY id LIMIT 100`, cursor)
		if err != nil {
			return false, err
		}
		type source struct {
			id            int64
			owner         string
			e             notify.Event
			resourceOwner string
			key           string
		}
		sources := []source{}
		for rows.Next() {
			var v source
			if err = rows.Scan(&v.id, &v.owner, &v.e, &v.resourceOwner, &v.key); err != nil {
				rows.Close()
				return false, err
			}
			sources = append(sources, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, err
		}
		for _, v := range sources {
			cursor = v.id
			if v.e.Time.Before(created) {
				continue
			}
			resourceOwner := v.owner
			if v.e.Type == notify.TypeTaskFailed {
				if v.owner != owner {
					continue
				}
				resourceOwner = v.resourceOwner
			}

			if v.e.Type == notify.TypeTrafficQuota && v.e.ShareID != "" {
				var used, limit uint64
				var live bool
				if tx.QueryRow(ctx, `SELECT used_bytes,limit_bytes,enabled AND status='accepted' FROM agent_shares WHERE id=$1`, v.e.ShareID).Scan(&used, &limit, &live) != nil || !live {
					continue
				}
				v.e.UsedBytes = used
				v.e.LimitBytes = limit
			}
			if !s.notificationEventAllowed(ctx, tx, owner, v.e, resourceOwner) {
				continue
			}
			for _, r := range c.Rules {
				if !r.Enabled || r.Event != v.e.Type || (r.AgentID != "" && r.AgentID != v.e.AgentID) || (r.PolicyID != "" && r.PolicyID != v.e.ResourceID) {
					continue
				}
				// New/edited event rules establish a journal baseline before listening.
				st, er := readNotificationState(ctx, tx, owner, r.ID, "events")
				if er != nil {
					return false, er
				}
				if !st.Initialized {
					continue
				}
				if v.e.Time.Before(st.Since) {
					continue
				}
				if err = s.queueNotification(ctx, tx, owner, c, r, v.e, "source:"+v.key, resourceOwner); err != nil {
					return false, err
				}
			}
		}
		for _, r := range c.Rules {
			if !r.Enabled || !s.notificationCanEvent(ctx, tx, owner, r.Event) {
				continue
			}
			switch r.Event {
			case "speed.high", "speed.low":
				err = s.scanSpeedRule(ctx, tx, owner, c, r)
			case "traffic.warning":
				err = s.scanTrafficRule(ctx, tx, owner, c, r)
			case notify.TypeTrafficQuota:
				err = s.scanSharedQuotaRule(ctx, tx, owner, c, r)
			case "panel.update":
				err = s.scanUpdateRule(ctx, tx, owner, c, r)
			default:
				var st notificationState
				st, err = readNotificationState(ctx, tx, owner, r.ID, "events")
				if err == nil && !st.Initialized {
					st.Initialized = true
					st.Since = time.Now().UTC()
					err = writeNotificationState(ctx, tx, owner, r.ID, "events", st)
				}
			}
			if err != nil {
				return false, err
			}
		}
	} else {
		if _, err = tx.Exec(ctx, `DELETE FROM notification_rule_states WHERE owner_id=$1;`, owner); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE notification_deliveries SET status='canceled',error='账号权限已失效',updated_at=now() WHERE owner_id=$1 AND status IN ('pending','sending')`, owner); err != nil {
			return false, err
		}
	}

	_, err = tx.Exec(ctx, `UPDATE notification_configs SET cursor=$2,scanned_at=now() WHERE owner_id=$1`, owner, cursor)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
func (s *Store) scanSpeedRule(ctx context.Context, tx pgx.Tx, owner string, c notify.Configuration, r notify.Rule) error {
	scoped := s.notificationScope(ctx, tx, owner)
	if requireAgentAdministration(scoped, tx, r.AgentID) != nil {
		return nil
	}
	var e notify.Event
	var metrics core.HostMetrics
	var lastSeen time.Time
	var threshold int
	err := tx.QueryRow(ctx, `SELECT a.name,a.owner_id,a.last_seen,`+strings.ReplaceAll(agentOfflineThresholdSQL, "agents.", "a.")+`,COALESCE(l.metrics,'{}') FROM agents a LEFT JOIN agent_live_state l ON l.agent_id=a.id WHERE a.id=$1 AND a.revoked_at IS NULL`, r.AgentID).Scan(&e.Agent, &e.ResourceOwner, &lastSeen, &threshold, &metrics)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	st, err := readNotificationState(ctx, tx, owner, r.ID, r.AgentID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	valid := metrics.NetworkAvailable && metrics.NetworkRateAvailable != nil && *metrics.NetworkRateAvailable && now.Sub(lastSeen) < time.Duration(threshold)*time.Second && now.Sub(metrics.NetworkSampledAt) < time.Duration(threshold)*time.Second && !metrics.NetworkSampledAt.After(now.Add(5*time.Second))
	if !valid {
		st.Since = time.Time{}
		st.Sample = time.Time{}
		return writeNotificationState(ctx, tx, owner, r.ID, r.AgentID, st)
	}
	if !st.Initialized || st.Identity != e.ResourceOwner {
		st = notificationState{Initialized: true, Sample: metrics.NetworkSampledAt, Identity: e.ResourceOwner, Sequence: st.Sequence}
		return writeNotificationState(ctx, tx, owner, r.ID, r.AgentID, st)
	}
	if st.Sample.IsZero() || metrics.NetworkSampledAt.Sub(st.Sample) > time.Duration(threshold)*time.Second {
		st.Sample = metrics.NetworkSampledAt
		st.Since = time.Time{}
		return writeNotificationState(ctx, tx, owner, r.ID, r.AgentID, st)
	}

	if !metrics.NetworkSampledAt.After(st.Sample) {
		return nil
	}
	st.Sample = metrics.NetworkSampledAt
	rate := float64(metrics.NetworkRXBPS + metrics.NetworkTXBPS)
	if r.Direction == "upload" {
		rate = float64(metrics.NetworkTXBPS)
	} else if r.Direction == "download" {
		rate = float64(metrics.NetworkRXBPS)
	}
	abnormal := r.Event == "speed.high" && rate >= r.Threshold || r.Event == "speed.low" && rate <= r.Threshold
	recovered := r.Event == "speed.high" && rate <= r.Recovery || r.Event == "speed.low" && rate >= r.Recovery
	trigger := ""
	if !st.Active {
		if abnormal {
			if st.Since.IsZero() {
				st.Since = metrics.NetworkSampledAt
			}
			if metrics.NetworkSampledAt.Sub(st.Since) >= time.Duration(r.Duration)*time.Second && now.Sub(st.Last) >= time.Duration(r.Cooldown)*time.Second {
				st.Active = true
				st.Last = now
				st.Since = time.Time{}
				trigger = r.Event
			}
		} else {
			st.Since = time.Time{}
		}
	} else if recovered {
		if st.Since.IsZero() {
			st.Since = metrics.NetworkSampledAt
		}
		if metrics.NetworkSampledAt.Sub(st.Since) >= time.Duration(r.Duration)*time.Second {
			st.Active = false
			st.Since = time.Time{}
			trigger = "speed.recovered"
		}
	} else {
		st.Since = time.Time{}
	}
	if trigger != "" {
		st.Sequence++
		e.Type = trigger
		e.Time = now
		e.AgentID = r.AgentID
		e.Direction = map[string]string{"upload": "上传（TX）", "download": "下载（RX）", "total": "上传 + 下载"}[r.Direction]
		e.Current = fmt.Sprintf("%.0f B/s", rate)
		value := r.Threshold
		if trigger == "speed.recovered" {
			value = r.Recovery
		}
		e.Threshold = fmt.Sprintf("%.0f B/s", value)
		e.Status = trigger
		e.Message = notify.EventNames[trigger] + "：" + e.Agent + " / " + e.Direction + " / " + e.Current + "（阈值 " + e.Threshold + "）"
		if err = s.queueNotification(ctx, tx, owner, c, r, e, fmt.Sprintf("speed:%s:%d", r.AgentID, st.Sequence), e.ResourceOwner); err != nil {
			return err
		}
	}
	return writeNotificationState(ctx, tx, owner, r.ID, r.AgentID, st)
}
func (s *Store) scanTrafficRule(ctx context.Context, tx pgx.Tx, owner string, c notify.Configuration, r notify.Rule) error {
	scoped := s.notificationScope(ctx, tx, owner)
	args := []any{r.PolicyID}
	where := trafficAccessClause(scoped, "p.agent_id", "p.id", &args)
	var e notify.Event
	var generation int64
	var period string
	var shareID string
	var collected *time.Time
	err := tx.QueryRow(ctx, `SELECT p.agent_id,a.name,a.owner_id,p.engine,p.port,p.used_bytes,p.limit_bytes,p.reset_generation,
 COALESCE(p.period_start::text,'')||' / '||COALESCE(p.period_end::text,''),COALESCE(p.share_id,''),p.last_collected_at,p.config_id,p.config_version
 FROM port_traffic_policies p JOIN agents a ON a.id=p.agent_id WHERE p.id=$1 AND p.monitoring_enabled AND (p.quota_enabled OR COALESCE(p.share_id,'')<>'')`+where, args...).Scan(&e.AgentID, &e.Agent, &e.ResourceOwner, &e.Engine, &e.Port, &e.UsedBytes, &e.LimitBytes, &generation, &period, &shareID, &collected, &e.ConfigID, &e.ConfigVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Shared policies expose the grant's allocation, not host-wide counters.
	e.ShareID = shareID
	if shareID != "" {
		var valid bool
		err = tx.QueryRow(ctx, `SELECT g.used_bytes,g.limit_bytes,g.enabled AND g.status='accepted' AND NOT u.disabled FROM agent_shares g JOIN panel_users u ON u.id=g.user_id WHERE g.id=$1`, shareID).Scan(&e.UsedBytes, &e.LimitBytes, &valid)
		if err != nil || !valid {
			return nil
		}
		e.ShareID = shareID
	}
	resource := r.PolicyID
	if shareID != "" {
		resource = shareID
		period = "分配累计（不按月重置）"
		e.Port = 0
		e.Engine = ""
	}
	if r.Event == notify.TypeTrafficQuota {
		if shareID == "" {
			return nil
		}
		r.Threshold = 100
	}
	st, err := readNotificationState(ctx, tx, owner, r.ID, resource)
	if err != nil {
		return err
	}
	identity := fmt.Sprintf("%s:%s:%d:%d:%s", e.ResourceOwner, shareID, generation, e.LimitBytes, period)
	if shareID != "" {
		identity = fmt.Sprintf("%s:%s:%d", e.ResourceOwner, shareID, e.LimitBytes)
	}
	above := e.LimitBytes > 0 && float64(e.UsedBytes)/float64(e.LimitBytes)*100 >= r.Threshold
	if !st.Initialized || st.Identity != identity || e.UsedBytes < st.Used {
		st = notificationState{Initialized: true, Identity: identity, Used: e.UsedBytes, Active: above, Sequence: st.Sequence}
		return writeNotificationState(ctx, tx, owner, r.ID, resource, st)
	}
	st.Used = e.UsedBytes
	if !above {
		st.Active = false
	}
	if above && !st.Active && collected != nil && time.Since(*collected) < 5*time.Minute {
		st.Active = true
		st.Sequence++
		e.Time = time.Now().UTC()
		e.Type = r.Event
		e.ResourceID = r.PolicyID
		e.Period = period
		e.Direction = "上传 + 下载（计费口径）"
		e.Current = fmt.Sprintf("%.2f%%", float64(e.UsedBytes)/float64(e.LimitBytes)*100)
		e.Threshold = fmt.Sprintf("%.2f%%", r.Threshold)
		e.Message = notify.EventNames[r.Event] + "：" + e.Agent + " / " + e.Current + "（阈值 " + e.Threshold + "）"
		if err = s.queueNotification(ctx, tx, owner, c, r, e, fmt.Sprintf("traffic:%s:%s:%d", r.PolicyID, identity, st.Sequence), e.ResourceOwner); err != nil {
			return err
		}
	}
	return writeNotificationState(ctx, tx, owner, r.ID, resource, st)
}
func (s *Store) notificationScope(ctx context.Context, x storeExecutor, owner string) context.Context {
	admin := owner == ""
	var role string
	if owner != "" {
		_ = x.QueryRow(ctx, `SELECT role FROM panel_users WHERE id=$1 AND NOT disabled`, owner).Scan(&role)
		admin = role == "admin"
	}
	return WithConfigScope(ctx, owner, admin)
}

func (s *Store) scanSharedQuotaRule(ctx context.Context, tx pgx.Tx, owner string, c notify.Configuration, r notify.Rule) error {
	if r.PolicyID != "" {
		return s.scanTrafficRule(ctx, tx, owner, c, r)
	}
	scoped := s.notificationScope(ctx, tx, owner)
	args := []any{r.AgentID}
	where := trafficAccessClause(scoped, "p.agent_id", "p.id", &args)
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON(p.share_id) p.id FROM port_traffic_policies p WHERE COALESCE(p.share_id,'')<>'' AND p.monitoring_enabled AND ($1='' OR p.agent_id=$1)`+where+` ORDER BY p.share_id,p.id LIMIT 1000`, args...)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		r.PolicyID = id
		if err = s.scanTrafficRule(ctx, tx, owner, c, r); err != nil {
			return err
		}
	}
	return nil
}
