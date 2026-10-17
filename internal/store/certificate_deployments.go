package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

var panelCertificateFile = regexp.MustCompile(`^/etc/qcontrolhub/panel-certificates/(crt_[0-9a-f]{16})/(cert|key)\.pem$`)

func certificateReferences(engine core.Engine, content string) ([]string, error) {
	ids := map[string]bool{}
	_, err := serverconfig.MapCertificateFiles(engine, content, func(path string) (string, error) {
		if strings.HasPrefix(path, core.PanelCertificatePrefix) {
			m := panelCertificateFile.FindStringSubmatch(path)
			if m == nil {
				return "", fmt.Errorf("%w: 面板证书引用无效", ErrInvalid)
			}
			ids[m[1]] = true
		}
		return path, nil
	})
	out := []string{}
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, err
}

// Called in the same transaction as config/task creation. The config's owner,
// rather than a client-provided owner or administrator role, determines access.
func (s *Store) bindTaskCertificatesTx(ctx context.Context, tx pgx.Tx, task core.Task, owner string, features []string) error {
	ids, err := certificateReferences(task.Engine, task.ConfigContent)
	if err != nil {
		return err
	}
	if len(ids) > 0 && !containsFeature(features, core.AgentFeatureCertificates) {
		return fmt.Errorf("%w: 请先升级部署节点 Agent（panel-certificates-v1）", ErrConflict)
	}
	for _, id := range ids {
		if err = certificateUseTx(ctx, tx, id, owner); err != nil {
			return err
		}
		var version int
		var raw []byte
		if err = tx.QueryRow(ctx, `SELECT version,metadata FROM panel_certificates WHERE id=$1 FOR SHARE`, id).Scan(&version, &raw); err != nil {
			return mapError(err)
		}
		var item core.Certificate
		if err = json.Unmarshal(raw, &item); err != nil {
			return err
		}
		if version < 1 || item.NotAfter == nil || !time.Now().Before(*item.NotAfter) {
			return fmt.Errorf("%w: 证书未签发或已过期", ErrInvalid)
		}
		_, err = tx.Exec(ctx, `INSERT INTO certificate_bindings(certificate_id,config_id,owner_id) VALUES($1,$2,$3) ON CONFLICT(certificate_id,config_id) DO NOTHING`, id, task.ConfigID, owner)
		if err != nil {
			return err
		}
	}
	return err
}
func (s *Store) prepareCertificateExecutionTx(ctx context.Context, tx pgx.Tx, task *core.Task, features []string) error {
	if task.ConfigContent == "" {
		return nil
	}
	ids, err := certificateReferences(task.Engine, task.ConfigContent)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if !containsFeature(features, core.AgentFeatureCertificates) {
		return errors.New("Agent 不再支持证书中心，请升级后重新部署")
	}
	var owner string
	if err = tx.QueryRow(ctx, `SELECT owner_id FROM configs WHERE id=$1 AND deleted_at IS NULL`, task.ConfigID).Scan(&owner); err != nil {
		return err
	}
	for _, id := range ids {
		if err = certificateUseTx(ctx, tx, id, owner); err != nil {
			return err
		}
		var secret string
		var version int
		if err = tx.QueryRow(ctx, `SELECT v.secret,v.version FROM panel_certificates c LEFT JOIN certificate_task_versions pinned ON pinned.task_id=$2 AND pinned.certificate_id=c.id JOIN certificate_versions v ON v.certificate_id=c.id AND v.version=COALESCE(pinned.version,c.version) WHERE c.id=$1 FOR SHARE OF c`, id, task.ID).Scan(&secret, &version); err != nil {
			return mapError(err)
		}
		if err = certificateUseTx(ctx, tx, id, owner); err != nil {
			return err
		}
		var pair certificates.Pair
		if err = s.openCertificate(secret, &pair); err != nil {
			return err
		}
		if _, err = certificates.Inspect(pair, time.Now()); err != nil {
			return fmt.Errorf("证书 %s 已失效或损坏", id)
		}
		if err := s.validateCertificateConfigNamesTx(ctx, tx, task, id, pair); err != nil {
			return err
		}
		task.Certificates = append(task.Certificates, core.DeploymentCertificate{ID: id, Version: version, Certificate: pair.Certificate, PrivateKey: pair.PrivateKey})
		_, err = tx.Exec(ctx, `INSERT INTO certificate_task_versions(task_id,certificate_id,version) VALUES($1,$2,$3) ON CONFLICT(task_id,certificate_id) DO UPDATE SET version=certificate_task_versions.version`, task.ID, id, version)
		if err != nil {
			return err
		}
		if task.Action == core.ActionDeploy {
			_, err = tx.Exec(ctx, `UPDATE certificate_bindings SET task_id=$3,status='running',error='',retry_at=now()+interval '5 minutes' WHERE certificate_id=$1 AND config_id=$2`, id, task.ConfigID, task.ID)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) completeCertificateBindingsTx(ctx context.Context, tx pgx.Tx, id string, success bool, message string) error {
	status := "failed"
	if success {
		status = "synced"
		message = ""
	}
	_, err := tx.Exec(ctx, `UPDATE certificate_bindings b SET version=CASE WHEN $2 THEN GREATEST(b.version,v.version) ELSE b.version END,enabled=CASE WHEN $2 THEN true ELSE b.enabled END,config_version=CASE WHEN $2 THEN t.config_version ELSE b.config_version END,status=$3,error=$4,retry_at=CASE WHEN $2 THEN now() ELSE now()+interval '5 minutes' END FROM certificate_task_versions v,tasks t WHERE v.task_id=$1 AND t.id=v.task_id AND t.action='deploy' AND b.config_id=t.config_id AND b.certificate_id=v.certificate_id AND b.task_id=t.id`, id, success, status, truncate(message, 1000))
	if err != nil {
		return err
	}
	if success {
		_, err = tx.Exec(ctx, `DELETE FROM certificate_bindings b USING tasks t WHERE t.id=$1 AND t.action='deploy' AND b.config_id=t.config_id AND NOT EXISTS(SELECT 1 FROM certificate_task_versions v WHERE v.task_id=t.id AND v.certificate_id=b.certificate_id)`, id)
	}
	return err
}
func (s *Store) syncCertificateBindings(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT b.certificate_id,b.config_id,b.owner_id,c.agent_id,c.engine,c.version FROM certificate_bindings b JOIN configs c ON c.id=b.config_id JOIN panel_certificates p ON p.id=b.certificate_id WHERE c.deleted_at IS NULL AND c.agent_id IS NOT NULL AND b.enabled AND b.config_version=c.version AND b.version<p.version AND b.retry_at<=now() AND NOT EXISTS(SELECT 1 FROM tasks t WHERE t.config_id=c.id AND t.status IN ('pending','running')) ORDER BY b.retry_at LIMIT 20`)
	if err != nil {
		return err
	}
	type binding struct {
		cert, config, owner, agent string
		engine                     core.Engine
		version                    int
	}
	items := []binding{}
	for rows.Next() {
		var b binding
		if err = rows.Scan(&b.cert, &b.config, &b.owner, &b.agent, &b.engine, &b.version); err != nil {
			rows.Close()
			return err
		}
		items = append(items, b)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, b := range items {
		ownerCtx := WithConfigScope(ctx, b.owner, false)
		if err = certificateUseTx(ownerCtx, s.pool, b.cert, b.owner); err == nil {
			_, err = s.CreateTask(ownerCtx, core.TaskRequest{AgentID: b.agent, Engine: b.engine, Action: core.ActionDeploy, ConfigID: b.config, ExpectedConfigVersion: b.version})
		}
		status := "pending"
		message := ""
		if err != nil {
			status = "blocked"
			message = truncate(err.Error(), 1000)
		}
		_, _ = s.pool.Exec(ctx, `UPDATE certificate_bindings SET status=$3,error=$4,retry_at=now()+interval '5 minutes' WHERE certificate_id=$1 AND config_id=$2 AND status<>'running'`, b.cert, b.config, status, message)
	}
	return nil
}

func (s *Store) openCertificateExecution(ctx context.Context, tx pgx.Tx, task *core.Task, features []string) error {
	if err := s.openExecutionConfig(task, features); err != nil {
		return err
	}
	return s.prepareCertificateExecutionTx(ctx, tx, task, features)
}

func (s *Store) ResolveCertificatePlan(ctx context.Context, id string, engine core.Engine, input *serverconfig.Input) error {
	protocol, ok := serverconfig.FindProtocol(engine, input.Protocol)
	if !ok || (!protocol.SupportsTLS && !protocol.RequiresTLS) || protocol.UsesReality || protocol.UsesWireGuard {
		return fmt.Errorf("%w: 此协议没有可使用面板证书的原生 TLS 入口", ErrInvalid)
	}
	if err := certificateUseTx(ctx, s.pool, id, certificateActor(ctx)); err != nil {
		return err
	}
	var secret string
	if err := s.pool.QueryRow(ctx, `SELECT v.secret FROM panel_certificates c JOIN certificate_versions v ON v.certificate_id=c.id AND v.version=c.version WHERE c.id=$1`, id).Scan(&secret); err != nil {
		return mapError(err)
	}
	var pair certificates.Pair
	if err := s.openCertificate(secret, &pair); err != nil {
		return err
	}
	if _, err := certificates.Inspect(pair, time.Now()); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	names, err := certificates.Domains([]string{input.TLSServerName})
	if err != nil || strings.HasPrefix(input.TLSServerName, "*.") {
		return fmt.Errorf("%w: 使用面板证书需填写有效 TLS SNI", ErrInvalid)
	}
	if err = certificates.Covers(pair, names); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	input.TLSServerName = names[0]
	input.CertificatePath = core.PanelCertificatePrefix + id + "/cert.pem"
	input.PrivateKeyPath = core.PanelCertificatePrefix + id + "/key.pem"
	input.TLSMode = "native"
	input.TLSEnabled = true
	return nil
}
func (s *Store) validateCertificateConfigNamesTx(ctx context.Context, tx pgx.Tx, task *core.Task, id string, pair certificates.Pair) error {
	rows, err := tx.Query(ctx, `SELECT profile_tag,content FROM config_client_metadata WHERE config_id=$1 AND config_version=$2`, task.ConfigID, task.ConfigVersion)
	if err != nil {
		return err
	}
	metadata := map[string]string{}
	for rows.Next() {
		var tag, secret string
		if err = rows.Scan(&tag, &secret); err != nil {
			rows.Close()
			return err
		}
		value, err := s.decryptContent(secret)
		if err != nil {
			rows.Close()
			return err
		}
		metadata[tag] = value
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	found := false
	for _, plan := range serverconfig.ParseAll(task.Engine, task.ConfigContent) {
		if value := metadata[plan.Tag]; value != "" {
			if err := serverconfig.ApplyClientMetadata(&plan, value); err != nil {
				return err
			}
		}
		if plan.CertificatePath == core.PanelCertificatePrefix+id+"/cert.pem" {
			if plan.PrivateKeyPath != core.PanelCertificatePrefix+id+"/key.pem" || plan.TLSServerName == "" {
				return fmt.Errorf("%w: 面板证书必须成对引用并指定 TLS SNI", ErrInvalid)
			}
			if err := certificates.Covers(pair, []string{plan.TLSServerName}); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalid, err)
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w: 面板证书仅支持可识别的原生 TLS 入站", ErrInvalid)
	}
	return nil
}
