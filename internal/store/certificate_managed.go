package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type managedCandidate struct {
	core.ManagedCertificateSource
	Owner string
}

// Use only the confirmed applied revision, never the latest editable draft,
// an Agent-provided owner, a hostname match, or a filesystem path.
func (s *Store) managedCandidates(ctx context.Context, tx pgx.Tx, agentID string) ([]managedCandidate, error) {
	rows, err := tx.Query(ctx, `WITH applied AS (
 SELECT agent_id,engine,owner_id,config_id,config_version,''::text AS share_id FROM agent_engine_ownership WHERE agent_id=$1 AND NOT config_uncertain
 UNION ALL
 SELECT st.agent_id,st.engine,st.owner_id,st.config_id,st.config_version,st.share_id FROM agent_shared_instance_ownership st
 JOIN agent_shares sh ON sh.id=st.share_id AND sh.agent_id=st.agent_id AND sh.user_id=st.owner_id
 WHERE st.agent_id=$1 AND NOT st.config_uncertain AND sh.enabled AND sh.status='accepted' AND st.engine=ANY(sh.engines)
 ) SELECT t.id,t.engine,t.config_id,t.owner_id,t.shared_traffic_id,t.config_version,
 CASE WHEN c.version=ap.config_version THEN c.content ELSE r.content END
 FROM applied ap JOIN agents a ON a.id=ap.agent_id AND a.revoked_at IS NULL
 JOIN configs c ON c.id=ap.config_id AND c.owner_id=ap.owner_id AND c.deleted_at IS NULL
 LEFT JOIN config_revisions r ON r.config_id=ap.config_id AND r.version=ap.config_version
 JOIN LATERAL (SELECT t.* FROM tasks t WHERE t.agent_id=ap.agent_id AND t.engine=ap.engine
 AND t.config_id=ap.config_id AND t.config_version=ap.config_version AND t.owner_id=ap.owner_id
 AND t.shared_traffic_id=ap.share_id AND t.action IN ('deploy','import-existing') AND t.status='succeeded'
 ORDER BY t.finished_at DESC LIMIT 1) t ON true
 WHERE (c.version=ap.config_version OR r.config_id IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM panel_users u WHERE u.id=ap.owner_id AND u.disabled)`, agentID)
	if err != nil {
		return nil, err
	}
	type snapshot struct {
		candidate managedCandidate
		version   int
		content   string
	}
	snapshots := []snapshot{}
	for rows.Next() {
		var snap snapshot
		n := &snap.candidate
		if err = rows.Scan(&n.TaskID, &n.Engine, &n.ConfigID, &n.Owner, &n.ShareID, &snap.version, &snap.content); err != nil {
			rows.Close()
			return nil, err
		}
		snapshots = append(snapshots, snap)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := []managedCandidate{}
	for _, snap := range snapshots {
		n := snap.candidate
		content, e := s.decryptContent(snap.content)
		if e != nil {
			return nil, e
		}
		frontends, e := s.managedTLSFrontendsTx(ctx, tx, n.ConfigID, snap.version, n.Engine, content)
		if e != nil {
			return nil, e
		}
		seen := map[string]bool{}
		for _, f := range frontends {
			if f.Mode != "caddy" || seen[f.Domain] {
				continue
			}
			if err = f.Validate(); err != nil {
				return nil, err
			}
			seen[f.Domain] = true
			n.AgentID = agentID
			n.Domain = f.Domain
			sum := sha256.Sum256([]byte(agentID + "\x00" + n.Owner + "\x00" + n.ConfigID + "\x00" + n.Domain))
			n.ID = "mcs_" + hex.EncodeToString(sum[:16])
			out = append(out, n)
		}
	}
	return out, nil
}

func (s *Store) AgentManagedCertificateSources(ctx context.Context, agentID string) ([]core.ManagedCertificateSource, error) {
	// Keep discovery metadata even when encryption is unavailable, but never ask
	// for private material until it can be stored encrypted.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM agents WHERE id=$1 FOR UPDATE`, agentID); err != nil {
		return nil, err
	}
	if !s.CertificateEncryptionReady() {
		_, err = tx.Exec(ctx, `UPDATE managed_certificate_sources SET status='blocked',error='面板未配置证书加密密钥，已阻止私钥上传；配置 QCH_CONFIG_ENCRYPTION_KEY 后自动重试' WHERE agent_id=$1 AND status NOT IN ('paused','detached','deleted')`, agentID)
		if err != nil {
			return nil, err
		}
		return []core.ManagedCertificateSource{}, tx.Commit(ctx)
	}
	candidates, err := s.managedCandidates(ctx, tx, agentID)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE managed_certificate_sources SET present=false WHERE agent_id=$1`, agentID); err != nil {
		return nil, err
	}
	for _, n := range candidates {
		if _, err = tx.Exec(ctx, `INSERT INTO managed_certificate_sources(id,agent_id,owner_id,config_id,task_id,engine,share_id,domain)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO UPDATE SET present=true,task_id=EXCLUDED.task_id`, n.ID, agentID, n.Owner, n.ConfigID, n.TaskID, n.Engine, n.ShareID, n.Domain); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT id,config_id,task_id,engine,share_id,domain FROM managed_certificate_sources WHERE agent_id=$1 AND present AND status NOT IN ('paused','detached','deleted') ORDER BY last_requested NULLS FIRST,id LIMIT 128`, agentID)
	if err != nil {
		return nil, err
	}
	out := []core.ManagedCertificateSource{}
	for rows.Next() {
		var n core.ManagedCertificateSource
		n.AgentID = agentID
		if err = rows.Scan(&n.ID, &n.ConfigID, &n.TaskID, &n.Engine, &n.ShareID, &n.Domain); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, n)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// Rotate bounded batches even when a source is blocked or a node hosts
	// more than 128 subjects; a failing source must not starve other tenants.
	for _, source := range out {
		if _, err = tx.Exec(ctx, `UPDATE managed_certificate_sources SET last_requested=now() WHERE id=$1`, source.ID); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}

// A report carries no trusted ownership fields. Recheck the exact deployment
// while holding its node lock, and commit the version and source atomically.
func (s *Store) ImportManagedCertificate(ctx context.Context, agentID string, report core.ManagedCertificateReport) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owner string
	if err = tx.QueryRow(ctx, `SELECT owner_id FROM managed_certificate_sources WHERE id=$1 AND agent_id=$2`, report.SourceID, agentID).Scan(&owner); err != nil {
		return mapError(err)
	}
	ownerCtx := WithConfigScope(ctx, owner, false)
	if err = lockAgentUser(ownerCtx, tx); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID); err != nil {
		return err
	}
	if err = certificateUserActive(ctx, tx, owner); err != nil {
		return err
	}
	var n core.ManagedCertificateSource
	var cid *string
	var present bool
	if err = tx.QueryRow(ctx, `SELECT id,config_id,task_id,domain,certificate_id,status,present FROM managed_certificate_sources WHERE id=$1 AND agent_id=$2 FOR UPDATE`, report.SourceID, agentID).Scan(&n.ID, &n.ConfigID, &n.TaskID, &n.Domain, &cid, &n.Status, &present); err != nil {
		return mapError(err)
	}
	if !present || n.Status == "paused" || n.Status == "deleted" || n.Status == "detached" {
		return ErrForbidden
	}
	candidates, err := s.managedCandidates(ctx, tx, agentID)
	if err != nil {
		return err
	}
	confirmed := false
	for _, c := range candidates {
		if c.ID == n.ID && c.Owner == owner && c.TaskID == n.TaskID {
			confirmed = true
			break
		}
	}
	if !confirmed {
		return fmt.Errorf("%w: 托管来源或部署归属未确认", ErrForbidden)
	}
	fail := func(reason string) error {
		_, e := tx.Exec(ctx, `UPDATE managed_certificate_sources SET status='blocked',error=$2 WHERE id=$1`, n.ID, reason)
		if e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if !s.CertificateEncryptionReady() {
		return fail("面板未配置证书加密密钥，无法保存")
	}
	if report.Error != "" {
		return fail("Agent 未找到安全且匹配的托管证书；请检查来源实例与 Caddy 签发状态")
	}
	pair := certificates.Pair{Certificate: report.Certificate, PrivateKey: report.PrivateKey}
	info, err := certificates.Inspect(pair, time.Now())
	if err != nil {
		return fail("托管证书校验失败：" + err.Error())
	}
	// Caddy issues one subject per configured automation policy. Additional SANs
	// cannot silently import another tenant's certificate into this source.
	if len(info.Domains) != 1 || info.Domains[0] != n.Domain {
		return fail("证书 SAN 与已确认的托管配置不一致，等待核验")
	}
	environment, err := certificates.Environment(pair)
	if err != nil {
		return fail("证书签发来源无法核验")
	}
	sealed, err := s.sealCertificate(pair)
	if err != nil {
		return err
	}
	var item core.Certificate
	if cid == nil {
		item = core.Certificate{Name: n.Domain + " · Caddy", Domains: info.Domains, Source: "caddy", Status: "issued", KeyType: info.KeyType}
		if err = s.createCertificateTx(ownerCtx, tx, &item); err != nil {
			return err
		}
	} else {
		var raw []byte
		if err = tx.QueryRow(ctx, `SELECT metadata,version FROM panel_certificates WHERE id=$1 AND owner_id=$2 FOR UPDATE`, *cid, owner).Scan(&raw, &item.Version); err != nil {
			return mapError(err)
		}
		version := item.Version
		if err = json.Unmarshal(raw, &item); err != nil {
			return err
		}
		item.Version = version
		if item.Source != "caddy" || item.SourceID != n.ID {
			return fail("证书已解除来源关联，自动同步已阻断")
		}
		if item.Fingerprint == info.Fingerprint {
			_, err = tx.Exec(ctx, `UPDATE managed_certificate_sources SET status='synced',error='',last_synced=now() WHERE id=$1`, n.ID)
			if err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		if item.NotBefore != nil && info.NotBefore.Before(*item.NotBefore) || item.NotAfter != nil && !info.NotAfter.After(*item.NotAfter) {
			return fail("忽略早于当前版本的证书上报；保留最新有效版本")
		}
	}
	now := time.Now().UTC()
	item.SourceID = n.ID
	item.SourceAgent = agentID
	item.SourceConfig = n.ConfigID
	item.RenewalManager = "caddy"
	item.Environment = environment
	item.SyncStatus = "synced"
	item.LastSynced = &now
	item.Domains = info.Domains
	item.KeyType = info.KeyType
	item.Fingerprint = info.Fingerprint
	item.NotBefore = &info.NotBefore
	item.NotAfter = &info.NotAfter
	item.AutoRenew = false
	item.Status = "issued"
	item.Error = ""
	item.Version++
	raw, _ := json.Marshal(item)
	if _, err = tx.Exec(ctx, `INSERT INTO certificate_versions(certificate_id,version,secret,metadata) VALUES($1,$2,$3,$4)`, item.ID, item.Version, sealed, raw); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE panel_certificates SET version=$2,metadata=$3,auto_renew=false WHERE id=$1`, item.ID, item.Version, raw); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE managed_certificate_sources SET certificate_id=$2,status='synced',error='',last_synced=now() WHERE id=$1`, n.ID, item.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ManagedCertificateSources(ctx context.Context) ([]core.ManagedCertificateSource, error) {
	rows, err := s.pool.Query(ctx, `SELECT s.id,s.agent_id,a.name,s.config_id,s.task_id,s.engine,s.share_id,s.domain,COALESCE(s.certificate_id,''),
 CASE WHEN s.status IN ('paused','detached','deleted') THEN s.status WHEN a.revoked_at IS NOT NULL OR NOT s.present THEN 'unverified'
 WHEN COALESCE(a.last_seen<now()-interval '1 minute',true) THEN 'offline' ELSE s.status END,CASE WHEN NOT s.present THEN '来源部署关系尚未确认或共享权限已变更；保留现有证书，停止导入新版本' ELSE s.error END,s.last_synced
 FROM managed_certificate_sources s JOIN agents a ON a.id=s.agent_id WHERE s.owner_id=$1 ORDER BY a.name,s.domain`, certificateActor(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.ManagedCertificateSource{}
	for rows.Next() {
		var n core.ManagedCertificateSource
		if err = rows.Scan(&n.ID, &n.AgentID, &n.AgentName, &n.ConfigID, &n.TaskID, &n.Engine, &n.ShareID, &n.Domain, &n.CertificateID, &n.Status, &n.Error, &n.LastSynced); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) SetManagedCertificateSync(ctx context.Context, id, action string) error {
	status := ""
	switch action {
	case "pause":
		status = "paused"
	case "resume":
		status = "pending"
	case "detach":
		status = "detached"
	default:
		return ErrInvalid
	}
	tag, err := s.pool.Exec(ctx, `UPDATE managed_certificate_sources SET status=$3,error='' WHERE id=$1 AND owner_id=$2 AND status<>'deleted'`, id, certificateActor(ctx), status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
