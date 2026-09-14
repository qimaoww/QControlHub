package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"golang.org/x/net/idna"
)

func (s *Store) SubStoreSyncSettings(ctx context.Context) (core.SubStoreSyncSettings, error) {
	return s.subStoreSyncSettings(ctx, s.pool)
}

func (s *Store) subStoreSyncSettings(ctx context.Context, executor storeExecutor) (core.SubStoreSyncSettings, error) {
	var settings core.SubStoreSyncSettings
	var endpoint string
	var err error
	if ownerID := scopeForConfig(ctx).OwnerID; ownerID != "" {
		err = executor.QueryRow(ctx, `SELECT endpoint_ciphertext,updated_at,backend_key FROM user_substore_settings WHERE owner_id=$1`,
			ownerID).Scan(&endpoint, &settings.UpdatedAt, &settings.BackendKey)
	} else {
		err = executor.QueryRow(ctx, `SELECT endpoint_ciphertext,updated_at,backend_key
			FROM substore_sync_settings WHERE id=1`).Scan(&endpoint, &settings.UpdatedAt, &settings.BackendKey)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return core.SubStoreSyncSettings{}, nil
	}
	if err != nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("read Sub-Store sync settings: %w", err)
	}
	settings.EndpointURL, err = s.decryptContent(endpoint)
	if err != nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("decrypt Sub-Store endpoint: %w", err)
	}
	settings.Configured = strings.TrimSpace(settings.EndpointURL) != ""
	return settings, nil
}

func (s *Store) SaveSubStoreSyncSettings(ctx context.Context, endpointURL string) (core.SubStoreSyncSettings, error) {
	ownerID := scopeForConfig(ctx).OwnerID
	endpointURL = strings.TrimSpace(endpointURL)
	if endpointURL == "" || utf8.RuneCountInString(endpointURL) > 1000 {
		return core.SubStoreSyncSettings{}, fmt.Errorf("%w: Sub-Store endpoint is required and must not exceed 1000 characters", ErrInvalid)
	}
	if s.cryptor == nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("%w: QCH_CONFIG_ENCRYPTION_KEY is required for Sub-Store credentials", ErrSecretUnavailable)
	}
	sealed, err := s.encryptContent(endpointURL)
	if err != nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("%w: encrypt Sub-Store endpoint: %v", ErrSecretUnavailable, err)
	}
	integrationID, err := core.NewID("ssi")
	if err != nil {
		return core.SubStoreSyncSettings{}, err
	}
	now := time.Now().UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.SubStoreSyncSettings{}, err
	}
	defer tx.Rollback(ctx)
	backendKey := subStoreBackendKey(endpointURL)
	if ownerID != "" {
		_, err = tx.Exec(ctx, `INSERT INTO user_substore_settings(owner_id,endpoint_ciphertext,backend_key,updated_at)
			VALUES($1,$2,$3,$4) ON CONFLICT(owner_id) DO UPDATE SET endpoint_ciphertext=EXCLUDED.endpoint_ciphertext,
				backend_key=EXCLUDED.backend_key,updated_at=EXCLUDED.updated_at`, ownerID, sealed, backendKey, now)
	} else {
		_, err = tx.Exec(ctx, `
		INSERT INTO substore_sync_settings
			(id,endpoint_ciphertext,subscription_name,integration_id,last_sync_status,last_sync_error,updated_at,backend_key)
		VALUES (1,$1,'QControlHub',$2,'never','',$3,$4)
		ON CONFLICT (id) DO UPDATE SET
			endpoint_ciphertext=EXCLUDED.endpoint_ciphertext,
			backend_key=EXCLUDED.backend_key,updated_at=EXCLUDED.updated_at`, sealed, integrationID, now, backendKey)
	}
	if err != nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("save Sub-Store sync settings: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE substore_sync_targets SET
			last_synced_at=NULL,last_sync_status='never',last_sync_error='',updated_at=$1,backend_key=$3
			WHERE owner_id=$2`, now, ownerID, backendKey); err != nil {
		return core.SubStoreSyncSettings{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return core.SubStoreSyncSettings{}, err
	}
	return s.SubStoreSyncSettings(ctx)
}

func subStoreBackendKey(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if parsed, err := url.Parse(endpoint); err == nil {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
		if address, err := netip.ParseAddr(host); err == nil {
			host = address.String()
		} else if ascii, err := idna.Lookup.ToASCII(host); err == nil {
			host = ascii
		}
		port := parsed.Port()
		if number, err := strconv.Atoi(port); err == nil {
			port = strconv.Itoa(number)
		}
		if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
			port = ""
		}
		parsed.Host = host
		if port != "" {
			parsed.Host = net.JoinHostPort(host, port)
		} else if strings.Contains(host, ":") {
			parsed.Host = "[" + host + "]"
		}
		parsed.Path, parsed.RawPath = path.Clean("/"+strings.Trim(parsed.Path, "/")), ""
		endpoint = parsed.String()
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(endpoint)))
}

func (s *Store) migrateSubStoreBackends(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT '' AS owner_id,endpoint_ciphertext,backend_key FROM substore_sync_settings WHERE id=1
		UNION ALL SELECT owner_id,endpoint_ciphertext,backend_key FROM user_substore_settings`)
	if err != nil {
		return err
	}
	type backend struct{ owner, endpoint, oldKey string }
	var backends []backend
	for rows.Next() {
		var item backend
		if err := rows.Scan(&item.owner, &item.endpoint, &item.oldKey); err != nil {
			rows.Close()
			return err
		}
		backends = append(backends, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range backends {
		endpoint, err := s.decryptContent(item.endpoint)
		if err != nil {
			return fmt.Errorf("read legacy Sub-Store backend: %w", err)
		}
		key := subStoreBackendKey(endpoint)
		if item.owner == "" {
			_, err = tx.Exec(ctx, `UPDATE substore_sync_settings SET backend_key=$1 WHERE id=1`, key)
		} else {
			_, err = tx.Exec(ctx, `UPDATE user_substore_settings SET backend_key=$1 WHERE owner_id=$2`, key, item.owner)
		}
		if err != nil {
			return err
		}
		// Preserve existing claims on aliases and on legacy shared backends,
		// but never copy an administrator's credentials into another account.
		if _, err := tx.Exec(ctx, `UPDATE substore_sync_targets SET backend_key=$1
			WHERE backend_key=$2 AND ($2<>'' OR owner_id=$3 OR $3='')`, key, item.oldKey, item.owner); err != nil {
			return err
		}
	}
	return nil
}
