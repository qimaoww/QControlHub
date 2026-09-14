package store

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TryLockSubStoreOperation serializes remote ownership/content changes with
// local target mutations across control-plane processes. A busy operation
// fails immediately instead of occupying pool connections waiting for a lock
// while the holder needs those connections to finish its API workflow.
func (s *Store) TryLockSubStoreOperation(ctx context.Context, newEndpoints ...string) (func(), error) {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	discard := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = connection.Hijack().Close(cleanup)
	}
	keys := []string{}
	lock := func(key string) error {
		var locked bool
		if err := connection.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended(current_schema() || ':qcontrolhub:substore:' || $1,0))`, key).Scan(&locked); err != nil {
			return err
		}
		if !locked {
			return fmt.Errorf("%w: 另有 Sub-Store 操作正在执行，请稍后重试", ErrConflict)
		}
		keys = append(keys, key)
		return nil
	}
	// A session lock leaves no MVCC transaction open during remote HTTP
	// requests, so frequent traffic updates can still prune their HOT chains.
	if err := lock("owner:" + scopeForConfig(ctx).OwnerID); err != nil {
		discard() // A canceled reply may have acquired the server-side lock.
		return nil, err
	}
	settings, err := s.subStoreSyncSettings(ctx, connection)
	if err != nil {
		discard()
		return nil, err
	}
	backends := map[string]bool{}
	if settings.BackendKey != "" {
		backends[settings.BackendKey] = true
	}
	for _, endpoint := range newEndpoints {
		if key := subStoreBackendKey(endpoint); key != "" {
			backends[key] = true
		}
	}
	for key := range backends {
		if err := lock("backend:" + key); err != nil {
			discard() // Closing also releases the owner lock.
			return nil, err
		}
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for i := len(keys) - 1; i >= 0; i-- {
				var unlocked bool
				if err := connection.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtextextended(current_schema() || ':qcontrolhub:substore:' || $1,0))`, keys[i]).Scan(&unlocked); err != nil || !unlocked {
					discard() // Never return a possibly locked connection to the pool.
					return
				}
			}
			connection.Release()
		})
	}
	return release, nil
}
