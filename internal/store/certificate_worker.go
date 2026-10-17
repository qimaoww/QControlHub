package store

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func certificatePairFromDER(chain [][]byte, key string) certificates.Pair {
	var cert []byte
	for _, der := range chain {
		cert = append(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return certificates.Pair{Certificate: string(cert), PrivateKey: key}
}

// RunCertificateMaintenance is restart-safe: durable order state, retry times,
// unique active orders and PostgreSQL advisory locks coordinate all panels.
func (s *Store) RunCertificateMaintenance(ctx context.Context) error {
	if !s.CertificateEncryptionReady() {
		return nil
	}
	// Rekeying failure is visible to the caller; do not block independent jobs.
	rekeyErr := s.rewrapCertificateSecrets(ctx)
	// A certificate with no active order becomes due 30 days before expiry.
	rows, err := s.pool.Query(ctx, `SELECT metadata FROM panel_certificates c WHERE auto_renew AND next_attempt<=now() AND NOT EXISTS(SELECT 1 FROM certificate_orders o WHERE o.certificate_id=c.id AND o.status IN ('queued','running','waiting_dns','validating','canceling')) LIMIT 20`)
	if err != nil {
		return err
	}
	due := []core.Certificate{}
	for rows.Next() {
		var raw []byte
		var c core.Certificate
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &c); err != nil {
			rows.Close()
			return err
		}
		due = append(due, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, c := range due {
		ownerCtx := WithConfigScope(ctx, c.OwnerID, false)
		if c.NotAfter != nil && time.Until(*c.NotAfter) < 30*24*time.Hour && c.Method != "dns-manual" && c.Source == "acme" {
			if err = certificateUserActive(ownerCtx, s.pool, c.OwnerID); err == nil {
				_ = s.RenewCertificate(ownerCtx, c.ID)
			}
		}
		_, _ = s.pool.Exec(ctx, `UPDATE panel_certificates SET next_attempt=now()+interval '1 day' WHERE id=$1`, c.ID)
	}
	rows, err = s.pool.Query(ctx, `SELECT o.id,o.certificate_id FROM certificate_orders o WHERE (o.status IN ('queued','running','validating','canceling') OR (o.status IN ('succeeded','failed','canceled') AND o.stage='cleanup')) AND o.next_attempt<=now() ORDER BY o.created_at LIMIT 20`)
	if err != nil {
		return err
	}
	type selected struct{ id, cert string }
	jobs := []selected{}
	for rows.Next() {
		var job selected
		if err = rows.Scan(&job.id, &job.cert); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		connection, err := s.pool.Acquire(ctx)
		if err != nil {
			return err
		}
		var locked bool
		err = connection.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,7501))`, job.cert).Scan(&locked)
		if err != nil || !locked {
			connection.Release()
			continue
		}
		func() {
			defer func() {
				unlockCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer done()
				_, _ = connection.Exec(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1,7501))`, job.cert)
				connection.Release()
			}()
			var raw []byte
			var secret, status string
			var attempt int
			if err = connection.QueryRow(ctx, `SELECT c.metadata,o.secret,o.status,o.attempt FROM certificate_orders o JOIN panel_certificates c ON c.id=o.certificate_id WHERE o.id=$1 AND o.next_attempt<=now()`, job.id).Scan(&raw, &secret, &status, &attempt); err != nil {
				return
			}
			var item core.Certificate
			var state certificateOrderState
			if json.Unmarshal(raw, &item) != nil || s.openCertificate(secret, &state) != nil {
				_, _ = s.pool.Exec(ctx, `UPDATE certificate_orders SET error='订单无法解密；请恢复加密密钥和 previous keys 后重试',next_attempt=now()+interval '10 minutes' WHERE id=$1`, job.id)
				return
			}
			ownerCtx := WithConfigScope(ctx, item.OwnerID, false)
			runCtx, cancel := context.WithTimeout(ownerCtx, 90*time.Second)
			defer cancel()
			if status == "succeeded" || status == "failed" || status == "canceled" {
				err = s.cleanupCertificateOrder(runCtx, item, job.id, &state)
				if err == nil {
					_, _ = s.pool.Exec(ctx, `UPDATE certificate_orders SET stage='complete' WHERE id=$1`, job.id)
				} else {
					_, _ = s.pool.Exec(ctx, `UPDATE certificate_orders SET error='验证资源清理失败，将重试',next_attempt=now()+interval '5 minutes' WHERE id=$1`, job.id)
				}
				return
			}
			// Preserve queued for manual continuation; all state transitions are durable.
			_, _ = s.pool.Exec(ctx, `UPDATE certificate_orders SET attempt=attempt+1,updated_at=now() WHERE id=$1`, job.id)
			err = s.processCertificateOrder(runCtx, item, job.id, status, state)
			if err != nil {
				retry := time.Duration(1<<min(attempt, 6)) * time.Minute
				nextStatus := "queued"
				stage := "retry"
				if item.Method == "dns-manual" {
					nextStatus = "waiting_dns"
					stage = "waiting_dns"
				}
				if attempt >= 7 {
					nextStatus = "failed"
					stage = "cleanup"
				}
				_, _ = s.pool.Exec(context.WithoutCancel(ctx), `UPDATE certificate_orders SET status=CASE WHEN status='canceling' THEN 'canceling' ELSE $2 END,stage=$3,error=$4,next_attempt=now()+$5::interval,updated_at=now() WHERE id=$1 AND status NOT IN ('succeeded','canceled')`, job.id, nextStatus, stage, truncate(err.Error(), 1000), intervalString(retry))
				_, _ = s.pool.Exec(context.WithoutCancel(ctx), `DELETE FROM certificate_http_challenges WHERE order_id=$1`, job.id)
			}
		}()
	}
	if err := s.syncCertificateBindings(ctx); err != nil {
		return err
	}
	return rekeyErr
}
