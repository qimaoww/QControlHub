package store

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const agentPublicKeyCacheTTL = 15 * time.Second

type cachedAgentPublicKey struct {
	key       []byte
	expiresAt time.Time
}

func (s *Store) invalidateAgentPublicKeyAfterCommit(ctx context.Context, id string) {
	runAfterCommit(ctx, func() {
		s.pubKeyCacheMu.Lock()
		if s.pubKeyCacheGen == nil {
			s.pubKeyCacheGen = make(map[string]uint64)
		}
		s.pubKeyCacheGen[id]++
		delete(s.pubKeyCache, id)
		s.pubKeyCacheMu.Unlock()
	})
}

func (s *Store) AgentPublicKey(ctx context.Context, id string) ([]byte, error) {
	useCache := !HasRequestTransaction(ctx)
	var loadGen uint64
	if useCache {
		now := time.Now()
		s.pubKeyCacheMu.RLock()
		entry, ok := s.pubKeyCache[id]
		loadGen = s.pubKeyCacheGen[id]
		s.pubKeyCacheMu.RUnlock()
		if ok && now.Before(entry.expiresAt) {
			return bytes.Clone(entry.key), nil
		}
	}
	var publicKey []byte
	err := s.pool.QueryRow(ctx, `SELECT public_key FROM agents WHERE id=$1 AND revoked_at IS NULL`, id).Scan(&publicKey)
	if hook := s.afterAgentPublicKeyQuery; hook != nil {
		hook(ctx, id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if useCache {
			s.pubKeyCacheMu.Lock()
			delete(s.pubKeyCache, id)
			s.pubKeyCacheMu.Unlock()
		}
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if useCache {
		s.pubKeyCacheMu.Lock()
		if s.pubKeyCacheGen[id] == loadGen {
			if s.pubKeyCache == nil {
				s.pubKeyCache = make(map[string]cachedAgentPublicKey)
			}
			s.pubKeyCache[id] = cachedAgentPublicKey{
				key:       bytes.Clone(publicKey),
				expiresAt: time.Now().Add(agentPublicKeyCacheTTL),
			}
		}
		s.pubKeyCacheMu.Unlock()
	}
	return publicKey, nil
}

func (s *Store) RecordNonce(ctx context.Context, agentID, nonce string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO agent_nonces (agent_id, nonce, expires_at) VALUES ($1,$2,$3)`, agentID, nonce, expiresAt)
	if isUniqueViolation(err) {
		return ErrReplay
	}
	return err
}

func (s *Store) CleanupNonces(ctx context.Context) error {
	const batchSize = 10000
	for {
		tag, err := s.pool.Exec(ctx, `
			WITH expired AS (
				SELECT ctid FROM agent_nonces WHERE expires_at < now() LIMIT $1
			)
			DELETE FROM agent_nonces n USING expired WHERE n.ctid = expired.ctid`, batchSize)
		if err != nil || tag.RowsAffected() < batchSize {
			return err
		}
	}
}
