package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) EnrollAgent(ctx context.Context, request core.EnrollRequest, enrollmentToken string) (core.Agent, error) {
	id, err := core.NewID("agt")
	if err != nil {
		return core.Agent{}, err
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(request.PublicKey)
	if err != nil || len(publicKey) != 32 {
		return core.Agent{}, fmt.Errorf("%w: invalid Ed25519 public key", ErrInvalid)
	}
	supported := append([]core.Engine{}, request.Capabilities...)
	supportedCapabilities, _ := json.Marshal(supported)
	features, _ := json.Marshal(request.Features)
	if len(request.Features) == 0 {
		features = []byte(`[]`)
	}
	labels, _ := json.Marshal(request.Labels)
	runtimeState := []byte(`{}`)
	enrolledAt := time.Now().UTC()
	lastSeen := time.Unix(0, 0).UTC()
	if len(enrollmentToken) < 32 {
		return core.Agent{}, ErrNotFound
	}
	tokenDigest := sha256.Sum256([]byte(enrollmentToken))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Agent{}, err
	}
	defer tx.Rollback(ctx)
	var enrollmentID, enrollmentName, enrollmentOwner string
	var enrollmentAgentID *string
	var reusable, adminHidden bool
	err = tx.QueryRow(ctx, `
		UPDATE enrollment_tokens SET used_count=used_count+1
		WHERE token_hash=$1 AND revoked_at IS NULL
		  AND (reusable OR (expires_at>now() AND used_count<max_uses))
		  AND (owner_id='' OR starts_with(owner_id,'token_') OR EXISTS(
			SELECT 1 FROM panel_users u WHERE u.id=enrollment_tokens.owner_id AND NOT u.disabled))
		RETURNING id,name,reusable,agent_id,owner_id,admin_hidden`, tokenDigest[:]).Scan(&enrollmentID, &enrollmentName, &reusable, &enrollmentAgentID, &enrollmentOwner, &adminHidden)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Agent{}, ErrNotFound
	}
	if err != nil {
		return core.Agent{}, err
	}
	name := strings.TrimSpace(request.Name)
	reinstalled := false
	if reusable {
		if name != enrollmentName {
			return core.Agent{}, ErrNotFound
		}
		boundAgentID := ""
		if enrollmentAgentID != nil {
			boundAgentID = strings.TrimSpace(*enrollmentAgentID)
		}
		if boundAgentID != "" {
			err = tx.QueryRow(ctx, `SELECT id,name,admin_hidden FROM agents WHERE id=$1 AND owner_id=$2 AND revoked_at IS NULL FOR UPDATE`, boundAgentID, enrollmentOwner).Scan(&id, &name, &adminHidden)
		} else {
			err = tx.QueryRow(ctx, `SELECT id,name,admin_hidden FROM agents WHERE enrollment_id=$1 AND owner_id=$2 AND revoked_at IS NULL FOR UPDATE`, enrollmentID, enrollmentOwner).Scan(&id, &name, &adminHidden)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			if boundAgentID != "" {
				return core.Agent{}, ErrNotFound
			}
			err = nil
		} else if err != nil {
			return core.Agent{}, err
		} else {
			reinstalled = true
		}
	}
	if reinstalled {
		// Keep the node's selection through reinstalls, bounded by the newly
		// declared support of the Agent binary/environment.
		var selected []core.Engine
		if err := tx.QueryRow(ctx, `SELECT capabilities FROM agents WHERE id=$1`, id).Scan(&selected); err != nil {
			return core.Agent{}, err
		}
		request.Capabilities = core.IntersectEngines(selected, request.Capabilities)
	} else {
		settings, err := s.panelSettingsForOwner(ctx, tx, enrollmentOwner)
		if err != nil {
			return core.Agent{}, err
		}
		request.Capabilities = core.IntersectEngines(settings.DefaultAgentEngines, request.Capabilities)
	}
	capabilities, _ := json.Marshal(request.Capabilities)
	if reinstalled {
		// The credential's original name authenticates the reinstall above;
		// retain the row-locked panel name instead of reverting a custom rename.
		// The stored metrics snapshot and the observed address describe the
		// previous installation, so a first report from the new one starts from
		// a clean slate rather than inheriting stale probe results.
		_, err = tx.Exec(ctx, `
			UPDATE agents SET name=$2,version=$3,os=$4,arch=$5,capabilities=$6,features=$7,labels=$8,runtime=$9,
				observed_public_ip='',public_key=$10,last_seen=$11,enrolled_at=$12,revoked_at=NULL
			WHERE id=$1`, id, name, strings.TrimSpace(request.Version), strings.TrimSpace(request.OS), strings.TrimSpace(request.Arch),
			capabilities, features, labels, runtimeState, publicKey, lastSeen, enrolledAt)
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM agent_live_state WHERE agent_id=$1`, id)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM agent_nonces WHERE agent_id=$1`, id)
		}
	} else {
		_, err = tx.Exec(ctx, `
			INSERT INTO agents (id,name,version,os,arch,capabilities,features,labels,runtime,public_key,last_seen,enrolled_at,enrollment_id,owner_id,admin_hidden)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			id, name, strings.TrimSpace(request.Version), strings.TrimSpace(request.OS), strings.TrimSpace(request.Arch),
			capabilities, features, labels, runtimeState, publicKey, lastSeen, enrolledAt, nullableEnrollmentID(reusable, enrollmentID), enrollmentOwner, adminHidden)
	}
	if err != nil {
		return core.Agent{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET supported_capabilities=$2 WHERE id=$1`, id, supportedCapabilities); err != nil {
		return core.Agent{}, err
	}
	if reusable {
		result, bindErr := tx.Exec(ctx, `
			UPDATE enrollment_tokens SET agent_id=$2
			WHERE id=$1 AND (agent_id IS NULL OR agent_id=$2)`, enrollmentID, id)
		if bindErr != nil {
			return core.Agent{}, mapError(bindErr)
		}
		if result.RowsAffected() == 0 {
			return core.Agent{}, ErrConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Agent{}, err
	}
	return core.Agent{
		SupportedCapabilities: supported,
		AdminHidden:           adminHidden,
		ID:                    id, Name: name, Version: request.Version,
		OwnerID: enrollmentOwner,
		OS:      request.OS, Arch: request.Arch, Capabilities: append([]core.Engine{}, request.Capabilities...), Features: append([]string(nil), request.Features...),
		Labels: cloneLabels(request.Labels), Runtime: map[core.Engine]core.RuntimeState{},
		LastSeen: lastSeen, EnrolledAt: enrolledAt, Status: "offline", Reinstalled: reinstalled,
	}, nil
}

func nullableEnrollmentID(reusable bool, enrollmentID string) any {
	if reusable {
		return enrollmentID
	}
	return nil
}
