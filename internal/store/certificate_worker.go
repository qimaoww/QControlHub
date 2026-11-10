package store

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// certificateOwnerContext preserves the database owner, including the legacy
// empty owner, without granting administrator or system access.
func certificateOwnerContext(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, configScopeKey{}, configScope{OwnerID: owner})
}

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
	var retentionErr error
	// A certificate with no active order becomes due 30 days before expiry.
	rows, err := s.pool.Query(ctx, `SELECT id,owner_id,metadata FROM panel_certificates c WHERE auto_renew AND next_attempt<=now() AND NOT EXISTS(SELECT 1 FROM certificate_orders o WHERE o.certificate_id=c.id AND o.status IN ('queued','running','waiting_dns','validating','canceling')) LIMIT 20`)
	if err != nil {
		return err
	}
	due := []core.Certificate{}
	for rows.Next() {
		var raw []byte
		var c core.Certificate
		var id, owner string
		if err = rows.Scan(&id, &owner, &raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &c); err != nil {
			rows.Close()
			return err
		}
		c.ID, c.OwnerID = id, owner
		due = append(due, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, c := range due {
		ownerCtx := certificateOwnerContext(ctx, c.OwnerID)
		if delegated, keyErr := s.apiKeyWorkContext(ownerCtx, "certificate-auto-renew", c.ID); keyErr != nil {
			if _, err = s.pool.Exec(ctx, `UPDATE panel_certificates SET next_attempt=now()+interval '1 day' WHERE id=$1`, c.ID); err != nil {
				return err
			}
			continue
		} else {
			ownerCtx = delegated
		}
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
			var secret, status, owner string
			var attempt int
			if err = connection.QueryRow(ctx, `SELECT c.metadata,o.secret,o.status,o.attempt,c.owner_id FROM certificate_orders o JOIN panel_certificates c ON c.id=o.certificate_id WHERE o.id=$1 AND o.next_attempt<=now() AND (o.status IN ('queued','running','validating','canceling') OR (o.status IN ('succeeded','failed','canceled') AND o.stage='cleanup'))`, job.id).Scan(&raw, &secret, &status, &attempt, &owner); err != nil {
				return
			}
			var item core.Certificate
			var state certificateOrderState
			decodeErr := json.Unmarshal(raw, &item)
			if decodeErr == nil {
				decodeErr = s.openCertificate(secret, &state)
			}
			if decodeErr != nil {
				_, _ = s.pool.Exec(ctx, `UPDATE certificate_orders SET error='订单无法解密；请恢复当前及旧加密密钥后重试',next_attempt=now()+interval '10 minutes' WHERE id=$1`, job.id)
				retentionErr = errors.Join(retentionErr, s.retainBackgroundFailure(ctx, owner, "证书订单读取", job.id, "order_decoding", decodeErr))
				return
			}
			item.ID, item.OwnerID = job.cert, owner
			if allowed, authErr := apiKeyWorkAllowed(ctx, s.pool, "certificate-order", job.id); authErr != nil {
				return
			} else if !allowed && status != "succeeded" && status != "failed" && status != "canceled" {
				_, _ = s.pool.Exec(ctx, `UPDATE certificate_orders SET status='canceling',stage='cleanup',error='API Key 授权已失效',updated_at=now() WHERE id=$1`, job.id)
				status = "canceling"
			}
			ownerCtx := certificateOwnerContext(ctx, owner)
			if status != "canceling" && status != "succeeded" && status != "failed" && status != "canceled" {
				delegated, e := s.apiKeyWorkContext(ownerCtx, "certificate-order", job.id)
				if e != nil {
					return
				}
				ownerCtx = delegated
			}

			runCtx, cancel := context.WithTimeout(ownerCtx, 90*time.Second)
			defer cancel()
			if status == "succeeded" || status == "failed" || status == "canceled" {
				retentionErr = errors.Join(retentionErr, s.finishCertificateOrderCleanup(runCtx, item, job.id, &state))
				return
			}
			// Keep the starting status for manual continuation. Cleanup is not
			// another issuance attempt; an in-flight cancellation wins this update.
			if status != "canceling" {
				tag, updateErr := s.pool.Exec(ctx, `UPDATE certificate_orders SET status='running',attempt=attempt+1,error='',stage=CASE WHEN stage IN ('queued','retry') THEN 'preparing' ELSE stage END,updated_at=now() WHERE id=$1 AND status IN ('queued','running','validating')`, job.id)
				if updateErr != nil || tag.RowsAffected() != 1 {
					return
				}
			}
			err = s.processCertificateOrder(runCtx, item, job.id, status, state)
			if err != nil {
				retentionErr = errors.Join(retentionErr, s.recordCertificateOrderFailure(context.WithoutCancel(ctx), job.id, item.Method, status, attempt, err))
				// A queued retry may still be under CA validation. Keep its
				// bounded challenge routes until terminal/cancellation cleanup;
				// Agent reads independently enforce status and current grants.
			}
		}()
	}
	if err := s.syncCertificateBindings(ctx); err != nil {
		return err
	}
	return errors.Join(rekeyErr, retentionErr)
}

func (s *Store) recordCertificateOrderFailure(ctx context.Context, id, method, startedStatus string, attempt int, cause error) error {
	retry := time.Duration(1<<min(attempt, 6)) * time.Minute
	status, stage := "queued", "retry"
	var secret, failedStage, owner string
	var state certificateOrderState
	if err := s.pool.QueryRow(ctx, `SELECT secret,stage,owner_id FROM certificate_orders WHERE id=$1`, id).Scan(&secret, &failedStage, &owner); err != nil {
		return err
	}
	if err := s.openCertificate(secret, &state); err != nil {
		return err
	}
	state.FailureStage = failedStage
	secret, err := s.sealCertificate(state)
	if err != nil {
		return err
	}
	if method == "dns-manual" {
		for _, c := range state.Challenges {
			if c.Public.Type == "dns-01" && c.Public.Status != "valid" && c.Public.Name != "" && c.Public.Value != "" {
				status, stage = "waiting_dns", "waiting_dns"
			}
		}
	}
	var accountErr *certificateAccountReadError
	terminal := (errors.As(cause, &accountErr) && accountErr.permanent) || errors.Is(cause, ErrForbidden) || errors.Is(cause, errCertificateOrderInvalid) || (attempt >= 7 && startedStatus != "canceling")
	if terminal {
		status, stage = "failed", "cleanup"
	}
	// A cancellation received during validation must not inherit its long retry
	// delay. Once cleanup itself fails, retain backoff to protect the provider.
	_, err = s.pool.Exec(ctx, `UPDATE certificate_orders SET secret=$8,status=CASE WHEN status='canceling' THEN 'canceling' ELSE $2 END,stage=CASE WHEN status='canceling' THEN 'cleanup' ELSE $3 END,error=$4,next_attempt=CASE WHEN (status='canceling' AND $6::text<>'canceling') OR $7::boolean THEN now() ELSE now()+$5::interval END,updated_at=now() WHERE id=$1 AND status NOT IN ('succeeded','canceled')`, id, status, stage, truncate(SafeFailureDiagnostic(cause), 1000), intervalString(retry), startedStatus, terminal, secret)
	if err == nil {
		err = s.retainBackgroundFailure(ctx, owner, "证书签发", id, fmt.Sprintf("%s/attempt_%d", failedStage, attempt+1), cause)
		if err != nil {
			persist, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer done()
			_, markerErr := s.pool.Exec(persist, `UPDATE certificate_orders SET error=$2 WHERE id=$1`, id, truncate(SafeFailureDiagnostic(cause), 750)+"\n未能保存到任务："+truncate(SafeFailureDiagnostic(err), 200))
			err = errors.Join(err, markerErr)
		}
	}
	return err
}
