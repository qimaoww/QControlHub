package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/notify"
)

// DeliverNotification claims one bounded lease. Crashed workers retry after expiry.
// Remote acceptance followed by a lost response can result in a duplicate on retry.
func (s *Store) DeliverNotification(ctx context.Context, sender *notify.Sender, legacySecret string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var d NotificationDelivery
	var authorization string
	err = tx.QueryRow(ctx, `SELECT d.id,d.owner_id,d.rule_id,d.channel_id,d.event,d.resource_owner,d.attempts,d.principal_stamp
 FROM notification_deliveries d WHERE ((d.status='pending' AND d.next_at<=now()) OR (d.status='sending' AND d.lease_until<now()))
 AND NOT EXISTS(SELECT 1 FROM notification_deliveries busy WHERE busy.owner_id=d.owner_id AND busy.channel_id=d.channel_id AND busy.id<>d.id AND ((busy.status='sending' AND busy.lease_until>now()) OR (busy.status='pending' AND busy.attempts>0 AND busy.next_at>now()) OR busy.updated_at>now()-interval '3 seconds' AND busy.attempts>0))
 ORDER BY d.next_at,d.id FOR UPDATE OF d SKIP LOCKED LIMIT 1`).Scan(&d.ID, &d.Owner, &d.RuleID, &d.ChannelID, &d.Event, &d.ResourceOwner, &d.Attempts, &authorization)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Serializes claims for one channel across instances, without a global send lock.
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,91))`, d.Owner+":"+d.ChannelID).Scan(&locked); err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_deliveries WHERE owner_id=$1 AND channel_id=$2 AND id<>$3 AND ((status='sending' AND lease_until>now()) OR (status='pending' AND attempts>0 AND next_at>now()) OR updated_at>now()-interval '3 seconds' AND attempts>0))`, d.Owner, d.ChannelID, d.ID).Scan(&busy); err != nil {
		return false, err
	}
	if busy {
		return false, nil
	}
	c, err := s.notificationConfig(ctx, tx, d.Owner, false)
	if err != nil {
		return false, err
	}
	var channel *notify.Channel
	for _, ch := range c.Channels {
		if ch.ID == d.ChannelID && ch.Enabled {
			copy := ch
			channel = &copy
		}
	}
	enabled := d.Event.Test
	for _, r := range c.Rules {
		if r.ID == d.RuleID && r.Enabled {
			for _, id := range r.Channels {
				if id == d.ChannelID {
					enabled = true
				}
			}
		}
	}
	currentAuthorization, authErr := s.notificationAuthorization(ctx, tx, d.Owner)
	if authErr != nil || currentAuthorization != authorization || !enabled || channel == nil || !s.notificationActive(ctx, tx, d.Owner) || !s.notificationEventAllowed(ctx, tx, d.Owner, d.Event, d.ResourceOwner) {
		_, err = tx.Exec(ctx, `UPDATE notification_deliveries SET status='canceled',error='订阅或资源权限已失效',updated_at=now() WHERE id=$1`, d.ID)
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if d.Attempts >= 5 {
		_, err = tx.Exec(ctx, `UPDATE notification_deliveries SET status='failed',error='重试次数已耗尽，最后一次投递结果可能未知',lease_until=NULL,updated_at=now() WHERE id=$1`, d.ID)
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	settings, err := s.panelSettingsForOwner(ctx, tx, d.Owner)
	if err != nil {
		return false, err
	}
	template, ok := c.Templates[d.Event.Type]
	if !ok {
		template = notify.DefaultTemplate(d.Event.Type)
	}
	if d.Event.Test && d.Event.Type == "test" {
		d.Event.Title = "QControlHub 测试通知"
		d.Event.Message = "这是使用已保存渠道配置发送的测试消息。"
	} else if !d.Event.Test {
		rendered, er := notify.RenderFormatted(template, d.Event, settings.PanelName, settings.TimeZone)
		if er != nil {
			return false, er
		}
		d.Event.Title, d.Event.Message, d.Event.Entities = rendered.Title, rendered.Body, rendered.Entities
	}
	if _, configured := channel.Secrets["secret"]; channel.ID == "legacy-webhook" && !configured {
		if channel.Secrets == nil {
			channel.Secrets = map[string]string{}
		}
		channel.Secrets["secret"] = legacySecret
	}
	d.Lease = rand.Text()
	_, err = tx.Exec(ctx, `UPDATE notification_deliveries SET status='sending',attempts=attempts+1,lease_until=now()+interval '30 seconds',lease_id=$2,updated_at=now() WHERE id=$1`, d.ID, d.Lease)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	sendErr := sender.Send(sendCtx, *channel, d.Event)
	cancel()
	status := "accepted"
	message := ""
	delay := time.Duration(0)
	if sendErr != nil {
		status = "failed"
		message = "通知投递失败"
		var de *notify.DeliveryError
		if errors.As(sendErr, &de) {
			message = de.Reason
			if de.Retry && d.Attempts+1 < 5 {
				status = "pending"
				delay = time.Duration(1<<uint(d.Attempts)) * 15 * time.Second
				if de.After > delay {
					delay = de.After
				}
				if delay > 24*time.Hour {
					delay = 24 * time.Hour
				}
			}
		}
	}
	// Use a fresh bounded context so shutdown cannot silently discard a completed attempt.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	_, err = s.pool.Exec(finishCtx, `UPDATE notification_deliveries SET status=$3,error=$4,next_at=now()+$5::interval,lease_until=NULL,updated_at=now() WHERE id=$1 AND lease_id=$2 AND status='sending'`, d.ID, d.Lease, status, message, fmt.Sprintf("%f seconds", delay.Seconds()))
	return true, err
}
func (s *Store) QueueNotificationTest(ctx context.Context, id string, revision int64) (int64, error) {
	return s.QueueNotificationTemplateTest(ctx, id, revision, "")
}

// QueueNotificationTemplateTest snapshots a saved template with synthetic data.
// It never publishes a real event or changes a rule's detection/cooldown state.
func (s *Store) QueueNotificationTemplateTest(ctx context.Context, id string, revision int64, event string) (int64, error) {
	if event != "" && notify.EventNames[event] == "" {
		return 0, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return 0, err
	}
	owner := notificationOwner(ctx)
	if event != "" && !s.notificationCanEvent(ctx, tx, owner, event) {
		return 0, ErrForbidden
	}
	c, err := s.notificationConfig(ctx, tx, owner, true)
	if err != nil {
		return 0, err
	}
	if c.Revision != revision {
		return 0, ErrConflict
	}
	enabled := false
	for _, ch := range c.Channels {
		if ch.ID == id && ch.Enabled {
			enabled = true
		}
	}
	if !enabled {
		return 0, ErrInvalid
	}
	var recent int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE owner_id=$1 AND event->>'test'='true' AND created_at>now()-interval '1 minute'`, owner).Scan(&recent); err != nil {
		return 0, err
	}
	if recent >= 5 {
		return 0, fmt.Errorf("%w: 每分钟最多 5 次测试", ErrConflict)
	}
	var pending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM(SELECT 1 FROM notification_deliveries WHERE owner_id=$1 AND status IN ('pending','sending') LIMIT 1000) q`, owner).Scan(&pending); err != nil {
		return 0, err
	}
	if pending >= 1000 {
		return 0, fmt.Errorf("%w: 通知队列已满", ErrConflict)
	}
	e := notify.Event{Type: "test", Time: time.Now().UTC(), Test: true, Message: "测试通知"}
	if event != "" {
		e = notify.Sample(event)
		e.Test, e.Time = true, time.Now().UTC()
		template, ok := c.Templates[event]
		if !ok {
			template = notify.DefaultTemplate(event)
		}
		settings, er := s.panelSettingsForOwner(ctx, tx, owner)
		if er != nil {
			return 0, er
		}
		rendered, er := notify.RenderFormatted(template, e, settings.PanelName, settings.TimeZone)
		if er != nil {
			return 0, er
		}
		e.Title, e.Message, e.Entities = rendered.Title, rendered.Body, rendered.Entities
	}
	b, _ := json.Marshal(e)
	authorization, err := s.notificationAuthorization(ctx, tx, owner)
	if err != nil {
		return 0, err
	}
	var result int64
	err = tx.QueryRow(ctx, `INSERT INTO notification_deliveries(owner_id,rule_id,channel_id,dedupe,event,principal_stamp) VALUES($1,'test',$2,$3,$4,$5) RETURNING id`, owner, id, rand.Text(), b, authorization).Scan(&result)
	if err != nil {
		return 0, err
	}
	return result, tx.Commit(ctx)
}
func (s *Store) CleanNotificationRecords(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE notification_deliveries SET status='failed',error='投递工作已超过 30 天有效期',updated_at=now() WHERE id IN(SELECT id FROM notification_deliveries WHERE status IN ('pending','sending') AND created_at<now()-interval '30 days' ORDER BY id LIMIT 1000);
 DELETE FROM notification_deliveries WHERE id IN(SELECT id FROM notification_deliveries WHERE status IN ('accepted','failed','canceled') AND (updated_at<now()-interval '14 days' OR id<(SELECT COALESCE(max(id),0)-100000 FROM notification_deliveries)) ORDER BY id LIMIT 1000);
 DELETE FROM notification_deliveries WHERE id IN(SELECT id FROM (SELECT id,status,row_number() OVER(PARTITION BY owner_id ORDER BY id DESC) n FROM notification_deliveries) ranked WHERE n>5000 AND status IN ('accepted','failed','canceled') LIMIT 1000);
 DELETE FROM notification_events WHERE id IN(SELECT id FROM notification_events WHERE created_at<now()-interval '30 days' OR id<(SELECT COALESCE(max(id),0)-100000 FROM notification_events) ORDER BY id LIMIT 1000);
 DELETE FROM notification_rule_states WHERE (owner_id,rule_id,resource) IN (SELECT owner_id,rule_id,resource FROM notification_rule_states st WHERE st.rule_id<>'_update-comparison' AND st.resource NOT IN ('events','update') AND NOT EXISTS(SELECT 1 FROM agents WHERE id=st.resource) AND NOT EXISTS(SELECT 1 FROM port_traffic_policies WHERE id=st.resource) AND NOT EXISTS(SELECT 1 FROM agent_shares WHERE id=st.resource) LIMIT 1000);
 DELETE FROM notification_configs WHERE owner_id<>'' AND NOT starts_with(owner_id,'token_') AND NOT EXISTS(SELECT 1 FROM panel_users WHERE id=owner_id);`)
	return err
}
