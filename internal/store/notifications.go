package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/notify"
)

func notificationOwner(ctx context.Context) string { return scopeForConfig(ctx).OwnerID }
func (s *Store) notificationConfig(ctx context.Context, x storeExecutor, owner string, lock bool) (notify.Configuration, error) {
	var raw string
	var c notify.Configuration
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	err := x.QueryRow(ctx, `SELECT content,revision FROM notification_configs WHERE owner_id=$1`+suffix, owner).Scan(&raw, &c.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		c = notify.Configuration{Revision: 1, Channels: []notify.Channel{}, Rules: []notify.Rule{}, Templates: map[string]notify.Template{}}
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if raw != "" {
		raw, err = s.decryptContent(raw)
		if err != nil {
			return c, err
		}
		rev := c.Revision
		err = json.Unmarshal([]byte(raw), &c)
		c.Revision = rev
		return c, err
	}
	c.Channels = []notify.Channel{}
	c.Rules = []notify.Rule{}
	c.Templates = map[string]notify.Template{}
	settings, err := s.panelSettingsForOwner(ctx, x, owner)
	if err != nil {
		return c, err
	}
	if settings.WebhookURL != "" {
		c.Channels = append(c.Channels, notify.Channel{ID: "legacy-webhook", Name: "原有 Webhook", Type: "webhook", Enabled: true, Values: map[string]string{}, Secrets: map[string]string{"url": settings.WebhookURL}})
		for _, item := range []struct {
			event   string
			enabled bool
		}{{notify.TypeTaskFailed, settings.NotifyTaskFailed}, {notify.TypeAgentOffline, settings.NotifyAgentOffline}, {notify.TypeAgentOnline, settings.NotifyAgentOnline}, {notify.TypeTrafficQuota, settings.NotifyTrafficQuota}} {
			c.Rules = append(c.Rules, notify.Rule{ID: "legacy-" + strings.ReplaceAll(item.event, ".", "-"), Name: notify.EventNames[item.event], Event: item.event, Enabled: item.enabled, Channels: []string{"legacy-webhook"}, Duration: 30, Cooldown: 300, Interval: 86400})
		}
	}
	return c, nil
}
func (s *Store) NotificationConfiguration(ctx context.Context) (notify.Configuration, error) {
	owner := notificationOwner(ctx)
	if _, err := s.pool.Exec(ctx, `INSERT INTO notification_configs(owner_id) VALUES($1) ON CONFLICT DO NOTHING`, owner); err != nil {
		return notify.Configuration{}, err
	}
	return s.notificationConfig(ctx, s.pool, owner, false)
}
func (s *Store) SaveNotificationConfiguration(ctx context.Context, c notify.Configuration) (notify.Configuration, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return c, err
	}
	saved, err := s.saveNotificationConfiguration(ctx, tx, c)
	if err != nil {
		return c, err
	}
	if err = recordAPIKeyWork(ctx, tx, "notification-configuration", notificationOwner(ctx)); err != nil {
		return c, err
	}
	return saved, tx.Commit(ctx)
}
func (s *Store) saveNotificationConfiguration(ctx context.Context, tx pgx.Tx, c notify.Configuration) (notify.Configuration, error) {
	var err error
	owner := notificationOwner(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO notification_configs(owner_id) VALUES($1) ON CONFLICT DO NOTHING`, owner); err != nil {
		return c, err
	}
	previous, err := s.notificationConfig(ctx, tx, owner, true)
	if err != nil {
		return c, err
	}
	if previous.Revision != c.Revision {
		return c, ErrConflict
	}
	for i, ch := range c.Channels {
		if ch.Secrets == nil {
			ch.Secrets = map[string]string{}
		}
		for _, old := range previous.Channels {
			if old.ID == ch.ID && old.Type == ch.Type {
				if err := validateAPIKeyNotificationDestination(ctx, old, ch); err != nil {
					return c, err
				}
				for k, v := range old.Secrets {
					if _, provided := ch.Secrets[k]; !provided {
						ch.Secrets[k] = v
					}
				}
			}
		}
		ch.Configured = nil
		c.Channels[i] = ch
	}
	if err = c.Validate(); err != nil {
		return c, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for _, r := range c.Rules {
		if !r.Enabled {
			continue
		}
		if err = s.validateNotificationRule(ctx, tx, r); err != nil {
			return c, err
		}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	sealed, err := s.encryptContent(string(raw))
	if err != nil {
		return c, err
	}
	_, err = tx.Exec(ctx, `UPDATE notification_configs SET content=$2,revision=revision+1 WHERE owner_id=$1`, owner, sealed)
	if err != nil {
		return c, err
	}
	// A modified rule starts from the current journal, without replaying history.
	retainedRules := []notify.Rule{}
	for _, next := range c.Rules {
		for _, old := range previous.Rules {
			if next.Enabled && old.Enabled && next.ID == old.ID && next.Event == old.Event && next.AgentID == old.AgentID && next.PolicyID == old.PolicyID {
				retainedRules = append(retainedRules, next)
			}
		}
	}
	for _, r := range previous.Rules {
		same := false
		for _, next := range c.Rules {
			if sameNotificationRule(r, next) {
				same = true
			}
		}
		if !same {
			retained, er := preserveNotificationSourceRules(ctx, tx, owner, r.ID, retainedRules)
			if er != nil {
				return c, er
			}
			if _, err = tx.Exec(ctx, `DELETE FROM notification_rule_states WHERE owner_id=$1 AND rule_id=$2`, owner, r.ID); err != nil {
				return c, err
			}
			if _, err = tx.Exec(ctx, `UPDATE notification_deliveries SET status='canceled',error='规则已修改',updated_at=now() WHERE owner_id=$1 AND rule_id=$2 AND status IN ('pending','sending') AND NOT(id=ANY($3::bigint[]))`, owner, r.ID, retained); err != nil {
				return c, err
			}
		}
	}
	for _, old := range previous.Channels {
		same := false
		for _, next := range c.Channels {
			if sameNotificationChannel(old, next) && next.Enabled {
				same = true
			}
		}
		if !same {
			if _, err = tx.Exec(ctx, `UPDATE notification_deliveries SET status='canceled',error='渠道已修改或停用',updated_at=now() WHERE owner_id=$1 AND channel_id=$2 AND status IN ('pending','sending')`, owner, old.ID); err != nil {
				return c, err
			}
		}
	}
	for _, r := range c.Rules {
		if !r.Enabled || r.Event == "panel.update" || r.Event == "traffic.warning" || strings.HasPrefix(r.Event, "speed.") {
			continue
		}
		st, er := readNotificationState(ctx, tx, owner, r.ID, "events")
		if er != nil {
			return c, er
		}
		if !st.Initialized {
			st.Initialized = true
			st.Since = time.Now().UTC()
			if er = writeNotificationState(ctx, tx, owner, r.ID, "events", st); er != nil {
				return c, er
			}
		}
	}
	c.Revision++
	return c, nil
}

// A writer may keep an unread credential in place, but cannot redirect it to a
// new authentication destination. Explicitly supplied replacements do not use
// the stored credential and remain editable without secrets.read.
func validateAPIKeyNotificationDestination(ctx context.Context, old, next notify.Channel) error {
	key, isKey := ctx.Value(apiKeyAuthContextKey{}).(apiKeyAuth)
	if !isKey || core.HasPermission(key.Effective, core.PermissionSecretsRead) {
		return nil
	}
	retained := func(name string) bool {
		_, replaced := next.Secrets[name]
		return !replaced && old.Secrets[name] != ""
	}
	changed := false
	if next.Type == "smtp" && retained("password") {
		for _, name := range []string{"host", "port", "tls", "username"} {
			changed = changed || old.Get(name) != next.Get(name)
		}
	} else if retained("authorization") || retained("secret") || retained("token") || retained("device_key") {
		if target, provided := next.Secrets["url"]; provided {
			changed = old.Get("url") != target
		}
	}
	if changed {
		return fmt.Errorf("%w: 修改通知认证目标须替换凭据或具备 secrets.read", ErrForbidden)
	}
	return nil
}

// Labels and recipient ordering do not change detection or revoke queued work.
func sameNotificationRule(a, b notify.Rule) bool {
	a.Name, b.Name = "", ""
	a.Channels, b.Channels = append([]string{}, a.Channels...), append([]string{}, b.Channels...)
	sort.Strings(a.Channels)
	sort.Strings(b.Channels)
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func sameNotificationChannel(a, b notify.Channel) bool {
	a.Name, b.Name, a.Configured, b.Configured = "", "", nil, nil
	if len(a.Values) == 0 {
		a.Values = nil
	}
	if len(b.Values) == 0 {
		b.Values = nil
	}
	if len(a.Secrets) == 0 {
		a.Secrets = nil
	}
	if len(b.Secrets) == 0 {
		b.Secrets = nil
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Overlapping subscriptions share a source delivery. Removing one subscription
// must retain work still covered by an existing, enabled subscription.
func preserveNotificationSourceRules(ctx context.Context, tx pgx.Tx, owner, rule string, retained []notify.Rule) ([]int64, error) {
	ids := []int64{}
	if len(retained) == 0 {
		return ids, nil
	}
	rows, err := tx.Query(ctx, `SELECT id,channel_id,event FROM notification_deliveries WHERE owner_id=$1 AND rule_id=$2 AND status IN ('pending','sending') AND starts_with(dedupe,'source:') LIMIT 1000`, owner, rule)
	if err != nil {
		return nil, err
	}
	type item struct {
		id      int64
		channel string
		event   notify.Event
	}
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.id, &v.channel, &v.event); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, v := range items {
		for _, r := range retained {
			if r.Event != v.event.Type || r.AgentID != "" && r.AgentID != v.event.AgentID || r.PolicyID != "" && r.PolicyID != v.event.ResourceID {
				continue
			}
			for _, channel := range r.Channels {
				if channel != v.channel {
					continue
				}
				if _, err = tx.Exec(ctx, `UPDATE notification_deliveries SET rule_id=$2 WHERE id=$1`, v.id, r.ID); err != nil {
					return nil, err
				}
				ids = append(ids, v.id)
				goto nextItem
			}
		}
	nextItem:
	}
	return ids, nil
}
func (s *Store) validateNotificationRule(ctx context.Context, x storeExecutor, r notify.Rule) error {
	if !s.notificationCanEvent(ctx, x, notificationOwner(ctx), r.Event) {
		return ErrForbidden
	}
	if r.AgentID != "" {
		if err := requireAgentAccess(ctx, x, r.AgentID); err != nil {
			return err
		}
	}
	if strings.HasPrefix(r.Event, "speed.") {
		if err := requireAgentAdministration(ctx, x, r.AgentID); err != nil {
			return err
		}
	}
	if r.PolicyID != "" {
		args := []any{r.PolicyID}
		where := trafficAccessClause(ctx, "p.agent_id", "p.id", &args)
		var agent string
		if err := x.QueryRow(ctx, `SELECT p.agent_id FROM port_traffic_policies p WHERE p.id=$1 AND p.monitoring_enabled`+where, args...).Scan(&agent); err != nil {
			return mapError(err)
		}
		if r.AgentID != "" && agent != r.AgentID {
			return ErrInvalid
		}
	}
	return nil
}

// NotificationEventAllowed validates against current ownership and live grants, including before delivery and record reads.
func (s *Store) notificationEventAllowed(ctx context.Context, x storeExecutor, owner string, e notify.Event, resourceOwner string) bool {
	if !s.notificationCanEvent(ctx, x, owner, e.Type) {
		return false
	}
	if e.Test || e.Type == "panel.update" {
		return true
	}
	if e.AgentID == "" {
		return false
	}
	var currentOwner string
	if x.QueryRow(ctx, `SELECT owner_id FROM agents WHERE id=$1 AND revoked_at IS NULL`, e.AgentID).Scan(&currentOwner) != nil || currentOwner != resourceOwner {
		return false
	}
	scoped := s.notificationScope(ctx, x, owner)
	if e.Type == notify.TypeTaskFailed {
		var permitted bool
		err := x.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t WHERE t.id=$1 AND t.owner_id=$2 AND t.agent_id=$3 AND COALESCE(t.config_id,'')=$4 AND t.config_version=$5 AND t.shared_traffic_id=$6 AND (t.config_id IS NULL OR EXISTS(SELECT 1 FROM configs c WHERE c.id=t.config_id AND c.owner_id=t.owner_id AND c.deleted_at IS NULL)) AND
   (t.shared_traffic_id='' OR EXISTS(SELECT 1 FROM agent_shares g WHERE g.id=t.shared_traffic_id AND g.user_id=$2 AND g.enabled AND g.status='accepted' AND t.engine=ANY(g.engines))))`, e.TaskID, owner, e.AgentID, e.ConfigID, e.ConfigVersion, e.ShareID).Scan(&permitted)
		return err == nil && permitted && requireAgentAccess(scoped, x, e.AgentID) == nil
	}
	if strings.HasPrefix(e.Type, "speed.") {
		return requireAgentAdministration(scoped, x, e.AgentID) == nil
	}
	if strings.HasPrefix(e.Type, "traffic.") {
		args := []any{e.ResourceID, e.ConfigID, e.ConfigVersion, e.ShareID}
		where := trafficAccessClause(scoped, "p.agent_id", "p.id", &args)
		var ok bool
		err := x.QueryRow(scoped, `SELECT EXISTS(SELECT 1 FROM port_traffic_policies p WHERE p.id=$1 AND p.config_id=$2 AND p.config_version=$3 AND COALESCE(p.share_id,'')=$4 AND p.monitoring_enabled`+where+` AND (p.share_id IS NULL OR p.share_id='' OR EXISTS(SELECT 1 FROM agent_shares g JOIN panel_users u ON u.id=g.user_id WHERE g.id=p.share_id AND g.enabled AND g.status='accepted' AND NOT u.disabled AND p.engine=ANY(g.engines))))`, args...).Scan(&ok)
		return err == nil && ok
	}
	return requireAgentAccess(scoped, x, e.AgentID) == nil
}
func (s *Store) notificationActive(ctx context.Context, x storeExecutor, owner string) bool {
	if owner == "" {
		return true
	}
	if strings.HasPrefix(owner, "token_") {
		return s.notificationTokenPermission(owner, core.PermissionSettingsRead)
	}
	var active bool
	_ = x.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM panel_users WHERE id=$1 AND NOT disabled AND (role='admin' OR permissions ? 'settings.read'))`, owner).Scan(&active)
	return active
}
func (s *Store) notificationLegacyChanged(ctx context.Context, tx pgx.Tx, old, next core.PanelSettings) error {
	if old.WebhookURL == next.WebhookURL && old.NotifyTaskFailed == next.NotifyTaskFailed && old.NotifyAgentOnline == next.NotifyAgentOnline && old.NotifyAgentOffline == next.NotifyAgentOffline && old.NotifyTrafficQuota == next.NotifyTrafficQuota {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO notification_configs(owner_id) VALUES($1) ON CONFLICT DO NOTHING`, notificationOwner(ctx)); err != nil {
		return err
	}
	c, err := s.notificationConfig(ctx, tx, notificationOwner(ctx), true)
	if err != nil {
		return err
	}
	channels := c.Channels[:0]
	for _, ch := range c.Channels {
		if ch.ID != "legacy-webhook" {
			channels = append(channels, ch)
		}
	}
	c.Channels = channels
	rules := c.Rules[:0]
	for _, r := range c.Rules {
		if !strings.HasPrefix(r.ID, "legacy-") {
			rules = append(rules, r)
		}
	}
	c.Rules = rules
	if next.WebhookURL == "" {
		for i, r := range c.Rules {
			targets := []string{}
			for _, id := range r.Channels {
				if id != "legacy-webhook" {
					targets = append(targets, id)
				}
			}
			c.Rules[i].Channels = targets
			if len(targets) == 0 {
				c.Rules[i].Enabled = false
			}
		}
	}
	if next.WebhookURL != "" {
		c.Channels = append(c.Channels, notify.Channel{ID: "legacy-webhook", Name: "原有 Webhook", Type: "webhook", Enabled: true, Values: map[string]string{}, Secrets: map[string]string{"url": next.WebhookURL}})
		for _, v := range []struct {
			event string
			on    bool
		}{{notify.TypeTaskFailed, next.NotifyTaskFailed}, {notify.TypeAgentOnline, next.NotifyAgentOnline}, {notify.TypeAgentOffline, next.NotifyAgentOffline}, {notify.TypeTrafficQuota, next.NotifyTrafficQuota}} {
			c.Rules = append(c.Rules, notify.Rule{ID: "legacy-" + strings.ReplaceAll(v.event, ".", "-"), Name: notify.EventNames[v.event], Event: v.event, Enabled: v.on, Channels: []string{"legacy-webhook"}, Duration: 30, Cooldown: 300, Interval: 86400})
		}
	}
	_, err = s.saveNotificationConfiguration(ctx, tx, c)
	return err
}

type NotificationDelivery struct {
	ID            int64        `json:"id"`
	Owner         string       `json:"-"`
	RuleID        string       `json:"rule_id"`
	ChannelID     string       `json:"channel_id"`
	Event         notify.Event `json:"event"`
	ResourceOwner string       `json:"-"`
	Status        string       `json:"status"`
	Attempts      int          `json:"attempts"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
	NextAt        time.Time    `json:"next_at"`
	Error         string       `json:"error"`
	Lease         string       `json:"-"`
}

func (s *Store) NotificationDeliveries(ctx context.Context) ([]NotificationDelivery, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,rule_id,channel_id,event,resource_owner,status,attempts,created_at,updated_at,next_at,error FROM notification_deliveries WHERE owner_id=$1 ORDER BY id DESC LIMIT 100`, notificationOwner(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotificationDelivery{}
	for rows.Next() {
		var d NotificationDelivery
		if err = rows.Scan(&d.ID, &d.RuleID, &d.ChannelID, &d.Event, &d.ResourceOwner, &d.Status, &d.Attempts, &d.CreatedAt, &d.UpdatedAt, &d.NextAt, &d.Error); err != nil {
			return nil, err
		}
		if s.notificationEventAllowed(ctx, s.pool, notificationOwner(ctx), d.Event, d.ResourceOwner) {
			out = append(out, d)
		}
	}
	return out, rows.Err()
}

func (s *Store) notificationCanEvent(ctx context.Context, x storeExecutor, owner, event string) bool {
	permission := "agents.read"
	switch {
	case event == "test" || event == "panel.update":
		permission = "settings.read"
	case event == notify.TypeTaskFailed:
		permission = "tasks.read"
	case strings.HasPrefix(event, "speed."):
		permission = "metrics.read"
	case strings.HasPrefix(event, "traffic."):
		permission = "traffic.read"
	}
	return s.notificationCanPermission(ctx, x, owner, core.Permission(permission))
}

func (s *Store) notificationCanPermission(ctx context.Context, x storeExecutor, owner string, permission core.Permission) bool {
	if owner == "" {
		return true
	}
	if strings.HasPrefix(owner, "token_") {
		return s.notificationTokenPermission(owner, permission) && s.notificationTokenPermission(owner, core.PermissionSettingsRead)
	}
	var ok bool
	_ = x.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM panel_users WHERE id=$1 AND NOT disabled AND (role='admin' OR (permissions ? $2 AND permissions ? 'settings.read')))`, owner, permission).Scan(&ok)
	return ok
}

// Environment principals are revalidated against this process's current auth registry.
func (s *Store) SetNotificationTokenPermissions(principals map[string][]core.Permission) {
	s.notificationTokenMu.Lock()
	defer s.notificationTokenMu.Unlock()
	s.notificationTokens = principals
}
func (s *Store) notificationTokenPermission(owner string, permission core.Permission) bool {
	s.notificationTokenMu.RLock()
	defer s.notificationTokenMu.RUnlock()
	for _, p := range s.notificationTokens[owner] {
		if p == permission {
			return true
		}
	}
	return false
}

func (s *Store) notificationAuthorization(ctx context.Context, x storeExecutor, owner string) (string, error) {
	if owner == "" {
		return "system", nil
	}
	if strings.HasPrefix(owner, "token_") {
		s.notificationTokenMu.RLock()
		b, _ := json.Marshal(s.notificationTokens[owner])
		s.notificationTokenMu.RUnlock()
		return fmt.Sprintf("%x", sha256.Sum256(b)), nil
	}
	var auth, access int64
	err := x.QueryRow(ctx, `SELECT auth_revision,agent_access_revision FROM panel_users WHERE id=$1 AND NOT disabled`, owner).Scan(&auth, &access)
	return fmt.Sprintf("%d:%d", auth, access), err
}
