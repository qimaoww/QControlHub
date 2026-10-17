package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) CertificateEncryptionReady() bool { return s.cryptor != nil }
func (s *Store) sealCertificate(value any) (string, error) {
	if s.cryptor == nil {
		return "", fmt.Errorf("%w: 证书中心需要配置 QCH_CONFIG_ENCRYPTION_KEY；秘密不能以明文保存", ErrInvalid)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return s.encryptContent(string(raw))
}
func (s *Store) openCertificate(secret string, value any) error {
	if s.cryptor == nil || !strings.HasPrefix(secret, keyedEncryptedPrefix) {
		return errors.New("证书秘密缺少有效加密配置或密文格式")
	}
	raw, err := s.decryptContent(secret)
	if err != nil {
		return errors.New("证书秘密无法解密；请恢复原加密密钥或配置 previous keys")
	}
	return json.Unmarshal([]byte(raw), value)
}
func certificateActor(ctx context.Context) string { return scopeForConfig(ctx).OwnerID }
func certificateUserActive(ctx context.Context, ex storeExecutor, owner string) error {
	var denied bool
	if err := ex.QueryRow(ctx, `SELECT (starts_with($1::text,'usr_') AND NOT EXISTS(SELECT 1 FROM panel_users WHERE id=$1 AND NOT disabled))`, owner).Scan(&denied); err != nil {
		return err
	}
	if denied {
		return fmt.Errorf("%w: 资源所有者已停用", ErrForbidden)
	}
	return nil
}
func certificateUseTx(ctx context.Context, ex storeExecutor, id, owner string) error {
	if err := certificateUserActive(ctx, ex, owner); err != nil {
		return err
	}
	var allowed bool
	err := ex.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM panel_certificates c WHERE c.id=$1 AND (c.owner_id=$2 OR EXISTS(SELECT 1 FROM certificate_grants g WHERE g.certificate_id=c.id AND g.user_id=$2)) AND (NOT starts_with(c.owner_id,'usr_') OR EXISTS(SELECT 1 FROM panel_users u WHERE u.id=c.owner_id AND NOT u.disabled)))`, id, owner).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%w: 无证书使用授权", ErrForbidden)
	}
	return nil
}
func (s *Store) ListCertificateAccounts(ctx context.Context) ([]core.CertificateAccount, error) {
	rows, err := s.pool.Query(ctx, `SELECT metadata FROM certificate_accounts WHERE owner_id=$1 ORDER BY created_at`, certificateActor(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.CertificateAccount{}
	for rows.Next() {
		var raw []byte
		var item core.CertificateAccount
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) CreateCertificateAccount(ctx context.Context, item core.CertificateAccount, secret any) (core.CertificateAccount, error) {
	if strings.TrimSpace(item.Name) == "" || len(item.Name) > 100 {
		return item, ErrInvalid
	}
	id, err := core.NewID("cac")
	if err != nil {
		return item, err
	}
	item.ID = id
	sealed, err := s.sealCertificate(secret)
	if err != nil {
		return item, err
	}
	raw, _ := json.Marshal(item)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return item, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return item, err
	}
	if err := certificateUserActive(ctx, tx, certificateActor(ctx)); err != nil {
		return item, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO certificate_accounts(id,owner_id,kind,metadata,secret) VALUES($1,$2,$3,$4,$5)`, id, certificateActor(ctx), item.Kind, raw, sealed)
	if err != nil {
		return item, err
	}
	return item, tx.Commit(ctx)
}
func (s *Store) CertificateAccountSecret(ctx context.Context, id, kind string, value any) (core.CertificateAccount, error) {
	var raw []byte
	var secret string
	err := s.pool.QueryRow(ctx, `SELECT metadata,secret FROM certificate_accounts WHERE id=$1 AND owner_id=$2 AND kind=$3`, id, certificateActor(ctx), kind).Scan(&raw, &secret)
	var item core.CertificateAccount
	if err != nil {
		return item, mapError(err)
	}
	if err = json.Unmarshal(raw, &item); err != nil {
		return item, err
	}
	return item, s.openCertificate(secret, value)
}
func (s *Store) DeleteCertificateAccount(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM certificate_accounts a WHERE a.id=$1 AND a.owner_id=$2 AND NOT EXISTS(SELECT 1 FROM panel_certificates c WHERE c.metadata->>'account_id'=a.id OR c.metadata->>'dns_account_id'=a.id)`, id, certificateActor(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: 账号不存在或仍被证书引用", ErrConflict)
	}
	return nil
}
func (s *Store) ListCertificates(ctx context.Context) ([]core.Certificate, error) {
	owner := certificateActor(ctx)
	rows, err := s.pool.Query(ctx, `SELECT c.metadata,c.version,c.auto_renew,c.next_attempt,c.created_at,c.owner_id,COALESCE((SELECT o.status FROM certificate_orders o WHERE o.certificate_id=c.id ORDER BY o.created_at DESC LIMIT 1),''),COALESCE((SELECT o.error FROM certificate_orders o WHERE o.certificate_id=c.id ORDER BY o.created_at DESC LIMIT 1),''),(SELECT count(*) FROM certificate_bindings b JOIN configs f ON f.id=b.config_id WHERE b.certificate_id=c.id AND f.deleted_at IS NULL AND (c.owner_id=$1 OR b.owner_id=$1)),(SELECT count(*) FROM certificate_bindings b JOIN configs f ON f.id=b.config_id WHERE b.certificate_id=c.id AND f.deleted_at IS NULL AND (c.owner_id=$1 OR b.owner_id=$1) AND (b.version<c.version OR b.status<>'synced')) FROM panel_certificates c WHERE c.owner_id=$1 OR (EXISTS(SELECT 1 FROM certificate_grants g WHERE g.certificate_id=c.id AND g.user_id=$1) AND (NOT starts_with(c.owner_id,'usr_') OR EXISTS(SELECT 1 FROM panel_users u WHERE u.id=c.owner_id AND NOT u.disabled))) ORDER BY c.created_at DESC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Certificate{}
	for rows.Next() {
		var raw []byte
		var item core.Certificate
		var orderStatus, orderError string
		if err := rows.Scan(&raw, &item.Version, &item.AutoRenew, &item.NextAttempt, &item.CreatedAt, &item.OwnerID, &orderStatus, &orderError, &item.BindingCount, &item.PendingCount); err != nil {
			return nil, err
		}
		bindingCount, pendingCount := item.BindingCount, item.PendingCount
		version, auto, next, created, resourceOwner := item.Version, item.AutoRenew, item.NextAttempt, item.CreatedAt, item.OwnerID
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		item.Version, item.AutoRenew, item.NextAttempt, item.CreatedAt, item.OwnerID = version, auto, next, created, resourceOwner
		item.BindingCount, item.PendingCount = bindingCount, pendingCount
		item.Owned = owner == resourceOwner
		if orderStatus != "" && orderStatus != "succeeded" && (orderStatus != "canceled" || item.Version == 0) {
			item.Status = orderStatus
			item.Error = orderError
		}
		if item.NotAfter != nil && time.Now().After(*item.NotAfter) && item.Status == "issued" {
			item.Status = "expired"
		}
		// Account identifiers and validation infrastructure belong to the owner.
		if !item.Owned {
			item.AccountID = ""
			item.DNSAccountID = ""
			item.AgentID = ""
			item.Error = ""
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) GetCertificate(ctx context.Context, id string) (core.Certificate, error) {
	items, err := s.ListCertificates(ctx)
	if err != nil {
		return core.Certificate{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return core.Certificate{}, ErrNotFound
}
func (s *Store) createCertificateTx(ctx context.Context, tx pgx.Tx, item *core.Certificate) error {
	if err := lockAgentUser(ctx, tx); err != nil {
		return err
	}
	if err := certificateUserActive(ctx, tx, certificateActor(ctx)); err != nil {
		return err
	}
	if len(item.Name) < 1 || len(item.Name) > 100 {
		return fmt.Errorf("%w: 证书名称为 1–100 个字符", ErrInvalid)
	}
	var err error
	item.Domains, err = certificates.Domains(item.Domains)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	item.ID, err = core.NewID("crt")
	if err != nil {
		return err
	}
	item.OwnerID = certificateActor(ctx)
	item.Owned = true
	item.CreatedAt = time.Now().UTC()
	raw, _ := json.Marshal(item)
	_, err = tx.Exec(ctx, `INSERT INTO panel_certificates(id,owner_id,metadata,auto_renew) VALUES($1,$2,$3,$4)`, item.ID, item.OwnerID, raw, item.AutoRenew)
	return err
}
func (s *Store) SaveCertificatePair(ctx context.Context, id, name, source string, pair certificates.Pair) (core.Certificate, error) {
	info, err := certificates.Inspect(pair, time.Now())
	if err != nil {
		return core.Certificate{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	sealed, err := s.sealCertificate(pair)
	if err != nil {
		return core.Certificate{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Certificate{}, err
	}
	defer tx.Rollback(ctx)
	var item core.Certificate
	if id == "" {
		item = core.Certificate{Name: name, Domains: info.Domains, Source: source, KeyType: info.KeyType, Status: "issued"}
		if err = s.createCertificateTx(ctx, tx, &item); err != nil {
			return item, err
		}
	} else {
		if err := lockAgentUser(ctx, tx); err != nil {
			return item, err
		}
		var raw []byte
		if err = tx.QueryRow(ctx, `SELECT metadata FROM panel_certificates WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, certificateActor(ctx)).Scan(&raw); err != nil {
			return item, mapError(err)
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return item, err
		}
		if err = certificates.Covers(pair, item.Domains); err != nil {
			return item, fmt.Errorf("%w: 替换证书必须覆盖原有全部域名: %v", ErrInvalid, err)
		}
		var busy bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM certificate_orders WHERE certificate_id=$1 AND status IN ('queued','running','waiting_dns','validating','canceling'))`, id).Scan(&busy); err != nil {
			return item, err
		}
		if busy {
			return item, ErrConflict
		}
	}
	item.Domains = info.Domains
	item.KeyType = info.KeyType
	item.Fingerprint = info.Fingerprint
	item.NotBefore = &info.NotBefore
	item.NotAfter = &info.NotAfter
	item.Status = "issued"
	item.Error = ""
	if id != "" {
		item.Source = source
		item.AutoRenew = false
	}
	var version int
	if err = tx.QueryRow(ctx, `UPDATE panel_certificates SET version=version+1 WHERE id=$1 RETURNING version`, item.ID).Scan(&version); err != nil {
		return item, err
	}
	item.Version = version
	raw, _ := json.Marshal(item)
	_, err = tx.Exec(ctx, `INSERT INTO certificate_versions(certificate_id,version,secret,metadata) VALUES($1,$2,$3,$4)`, item.ID, version, sealed, raw)
	if err != nil {
		return item, err
	}
	_, err = tx.Exec(ctx, `UPDATE panel_certificates SET metadata=$2,auto_renew=$3,next_attempt=now()+interval '1 day' WHERE id=$1`, item.ID, raw, item.AutoRenew)
	if err != nil {
		return item, err
	}
	return item, tx.Commit(ctx)
}
func (s *Store) CertificateBindings(ctx context.Context, id string) ([]core.CertificateBinding, error) {
	item, err := s.GetCertificate(ctx, id)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT b.config_id,c.agent_id,b.owner_id,c.engine,b.version,b.task_id,CASE WHEN b.version<p.version AND b.status='synced' THEN 'pending' ELSE b.status END,CASE WHEN b.enabled AND b.config_version<>c.version THEN '当前配置有未部署修改；请确认后手动部署最新配置和证书' ELSE b.error END, (p.owner_id=b.owner_id OR EXISTS(SELECT 1 FROM certificate_grants g WHERE g.certificate_id=p.id AND g.user_id=b.owner_id)) FROM certificate_bindings b JOIN configs c ON c.id=b.config_id JOIN panel_certificates p ON p.id=b.certificate_id WHERE b.certificate_id=$1 AND c.deleted_at IS NULL AND ($2 OR b.owner_id=$3)`, id, item.Owned, certificateActor(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.CertificateBinding{}
	for rows.Next() {
		b := core.CertificateBinding{CertificateID: id}
		if err := rows.Scan(&b.ConfigID, &b.AgentID, &b.OwnerID, &b.Engine, &b.Version, &b.TaskID, &b.Status, &b.Error, &b.Authorized); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) CertificateGrants(ctx context.Context, id string) ([]core.CertificateGrant, error) {
	item, err := s.GetCertificate(ctx, id)
	if err != nil {
		return nil, err
	}
	if !item.Owned {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT g.user_id,u.username FROM certificate_grants g JOIN panel_users u ON u.id=g.user_id WHERE certificate_id=$1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.CertificateGrant{}
	for rows.Next() {
		g := core.CertificateGrant{CertificateID: id}
		if err := rows.Scan(&g.UserID, &g.Username); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) SetCertificateGrant(ctx context.Context, id, username string, remove bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return err
	}
	var owner string
	if err = tx.QueryRow(ctx, `SELECT owner_id FROM panel_certificates WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, certificateActor(ctx)).Scan(&owner); err != nil {
		return mapError(err)
	}
	var user string
	if err = tx.QueryRow(ctx, `SELECT id FROM panel_users WHERE username=$1 AND ($2 OR NOT disabled)`, strings.ToLower(strings.TrimSpace(username)), remove).Scan(&user); err != nil {
		return mapError(err)
	}
	if user == owner {
		return ErrInvalid
	}
	if remove {
		_, err = tx.Exec(ctx, `DELETE FROM certificate_grants WHERE certificate_id=$1 AND user_id=$2`, id, user)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO certificate_grants(certificate_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, user)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) DeleteCertificate(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return err
	}
	var owner string
	if err = tx.QueryRow(ctx, `SELECT owner_id FROM panel_certificates WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, certificateActor(ctx)).Scan(&owner); err != nil {
		return mapError(err)
	}
	var used bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM certificate_bindings b JOIN configs c ON c.id=b.config_id WHERE b.certificate_id=$1 AND c.deleted_at IS NULL) OR EXISTS(SELECT 1 FROM certificate_orders WHERE certificate_id=$1 AND (status IN ('queued','running','waiting_dns','validating','canceling') OR stage='cleanup')) OR EXISTS(SELECT 1 FROM certificate_task_versions v JOIN tasks t ON t.id=v.task_id WHERE v.certificate_id=$1 AND t.status IN ('pending','running'))`, id).Scan(&used); err != nil {
		return err
	}
	if used {
		return fmt.Errorf("%w: 请先在协议配置中解绑并部署，再取消未完成订单", ErrConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM certificate_task_versions WHERE certificate_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM certificate_bindings WHERE certificate_id=$1`, id); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM panel_certificates WHERE id=$1`, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) SetCertificateAutoRenew(ctx context.Context, id string, enabled bool) error {
	item, err := s.GetCertificate(ctx, id)
	if err != nil {
		return err
	}
	if !item.Owned {
		return ErrForbidden
	}
	if enabled && (item.Source != "acme" || item.Method == "dns-manual") {
		return fmt.Errorf("%w: 此来源需人工续期或替换", ErrInvalid)
	}
	_, err = s.pool.Exec(ctx, `UPDATE panel_certificates SET auto_renew=$3,next_attempt=now() WHERE id=$1 AND owner_id=$2`, id, certificateActor(ctx), enabled)
	return err
}

func (s *Store) CertificateVersionHistory(ctx context.Context, id string) ([]core.Certificate, error) {
	if _, err := s.GetCertificate(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT metadata FROM certificate_versions WHERE certificate_id=$1 ORDER BY version DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Certificate{}
	for rows.Next() {
		var raw []byte
		var c core.Certificate
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		c.AccountID = ""
		c.DNSAccountID = ""
		c.AgentID = ""
		out = append(out, c)
	}
	return out, rows.Err()
}

// Rewrap a bounded batch on each scheduler pass. Previous keys remain necessary
// until all resources and backups have been migrated; writes compare ciphertext
// to avoid overwriting a concurrently updated order.
func (s *Store) rewrapCertificateSecrets(ctx context.Context) error {
	if s.cryptor == nil {
		return nil
	}
	prefix := keyedEncryptedPrefix + s.cryptor.primary.id + ":"
	for _, table := range []string{"certificate_accounts", "certificate_versions", "certificate_orders"} {
		rows, err := s.pool.Query(ctx, "SELECT ctid::text,secret FROM "+table+" WHERE NOT starts_with(secret,$1) LIMIT 20", prefix)
		if err != nil {
			return err
		}
		type entry struct{ tid, secret string }
		batch := []entry{}
		for rows.Next() {
			var e entry
			if err = rows.Scan(&e.tid, &e.secret); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, e)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		for _, e := range batch {
			var value json.RawMessage
			if err = s.openCertificate(e.secret, &value); err != nil {
				return err
			}
			sealed, err := s.sealCertificate(value)
			if err != nil {
				return err
			}
			if _, err = s.pool.Exec(ctx, "UPDATE "+table+" SET secret=$3 WHERE ctid=$1::tid AND secret=$2", e.tid, e.secret, sealed); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) CertificatePublicChain(ctx context.Context, id string) (string, error) {
	if _, err := s.GetCertificate(ctx, id); err != nil {
		return "", err
	}
	var secret string
	if err := s.pool.QueryRow(ctx, `SELECT v.secret FROM certificate_versions v JOIN panel_certificates c ON c.id=v.certificate_id AND c.version=v.version WHERE c.id=$1`, id).Scan(&secret); err != nil {
		return "", mapError(err)
	}
	var pair certificates.Pair
	if err := s.openCertificate(secret, &pair); err != nil {
		return "", err
	}
	return pair.Certificate, nil
}

// Credential rotation keeps the account identifier used by renewal orders.
func (s *Store) UpdateCertificateDNSCredentials(ctx context.Context, id, name string, credentials certificates.DNSCredentials) (core.CertificateAccount, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.CertificateAccount{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return core.CertificateAccount{}, err
	}
	var raw []byte
	var item core.CertificateAccount
	if err = tx.QueryRow(ctx, `SELECT metadata FROM certificate_accounts WHERE id=$1 AND owner_id=$2 AND kind='dns' FOR UPDATE`, id, certificateActor(ctx)).Scan(&raw); err != nil {
		return item, mapError(err)
	}
	if err = json.Unmarshal(raw, &item); err != nil {
		return item, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return item, ErrInvalid
	}
	if err = (certificates.DNSClient{Provider: item.Provider, Credentials: credentials}).Validate(); err != nil {
		return item, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	sealed, err := s.sealCertificate(credentials)
	if err != nil {
		return item, err
	}
	item.Name = name
	raw, _ = json.Marshal(item)
	if _, err = tx.Exec(ctx, `UPDATE certificate_accounts SET metadata=$2,secret=$3 WHERE id=$1`, id, raw, sealed); err != nil {
		return item, err
	}
	return item, tx.Commit(ctx)
}
