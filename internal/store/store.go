package store

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool       *pgxpool.Pool
	cryptor    *configCryptor
	taskWakeMu sync.Mutex
	taskWakes  map[string]chan struct{}
}

type storeExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func Open(ctx context.Context, databaseURL string, allowInsecureRemote bool) (*Store, error) {
	return OpenWithConfigKey(ctx, databaseURL, allowInsecureRemote, "")
}

// OpenWithConfigKey opens the store and enables at-rest configuration
// encryption when a non-empty key is supplied. Existing plaintext rows keep
// working transparently; new writes are sealed with AES-256-GCM.
func OpenWithConfigKey(ctx context.Context, databaseURL string, allowInsecureRemote bool, configKey string) (*Store, error) {
	return OpenWithConfigKeyring(ctx, databaseURL, allowInsecureRemote, configKey, nil)
}

// OpenWithConfigKeyring uses configKey for new encrypted writes and previous
// keys only to decrypt data written before an intentional key rotation.
func OpenWithConfigKeyring(ctx context.Context, databaseURL string, allowInsecureRemote bool, configKey string, previousKeys []string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("QCH_DATABASE_URL is required")
	}
	configKey = strings.TrimSpace(configKey)
	if configKey != "" && len([]byte(configKey)) < minConfigEncryptionKeyBytes {
		return nil, fmt.Errorf("QCH_CONFIG_ENCRYPTION_KEY must be at least %d bytes", minConfigEncryptionKeyBytes)
	}
	if strings.TrimSpace(configKey) == "" {
		for _, previousKey := range previousKeys {
			if strings.TrimSpace(previousKey) != "" {
				return nil, errors.New("QCH_CONFIG_ENCRYPTION_KEY is required when previous encryption keys are configured")
			}
		}
	}
	config, err := databasePoolConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL URL: %w", err)
	}
	if !localDatabaseHost(config.ConnConfig.Host) && !allowInsecureRemote {
		tlsConfig := config.ConnConfig.TLSConfig
		verifyFull := tlsConfig != nil && !tlsConfig.InsecureSkipVerify && tlsConfig.ServerName != "" && len(config.ConnConfig.Fallbacks) == 0
		if !verifyFull {
			return nil, errors.New("remote PostgreSQL connections must use sslmode=verify-full without cleartext fallback; set QCH_ALLOW_INSECURE_DATABASE=true only on a trusted development network")
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	cryptor, err := newConfigCryptorKeyring(append([]string{configKey}, previousKeys...))
	if err != nil {
		pool.Close()
		return nil, err
	}
	if err := cryptor.verify(); err != nil {
		pool.Close()
		return nil, err
	}
	result := &Store{pool: pool, cryptor: cryptor}
	if err := result.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	// A partitioned log table rejects inserts that match no partition, so the
	// current window has to exist before this store serves any upload. migrate
	// only runs on a version change, hence the explicit call on every open; the
	// maintenance loop keeps it extended while the process runs.
	if err := result.EnsureCoreLogPartitions(ctx, time.Now().UTC()); err != nil {
		pool.Close()
		return nil, err
	}
	return result, nil
}

func localDatabaseHost(host string) bool {
	if strings.HasPrefix(host, "/") || strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}
