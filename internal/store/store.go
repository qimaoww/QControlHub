package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrForbidden         = errors.New("forbidden")
	ErrConflict          = errors.New("conflict")
	ErrReplay            = errors.New("replayed request")
	ErrInvalid           = errors.New("invalid input")
	ErrSecretUnavailable = errors.New("protected enrollment credential unavailable")
	ErrAuditUnavailable  = errors.New("audit unavailable")
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

func (s *Store) encryptEnrollmentToken(rawToken string) (string, error) {
	if s.cryptor == nil {
		return "", fmt.Errorf("%w: QCH_CONFIG_ENCRYPTION_KEY is required", ErrSecretUnavailable)
	}
	sealed, err := s.cryptor.encrypt(rawToken)
	if err != nil {
		return "", fmt.Errorf("%w: encrypt enrollment credential: %v", ErrSecretUnavailable, err)
	}
	return sealed, nil
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

func (s *Store) migrate(ctx context.Context) error {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(0x52464f524745)); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer connection.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", int64(0x52464f524745))
	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS qcontrolhub_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("initialize schema migration ledger: %w", err)
	}
	var appliedVersion int
	if err := connection.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM qcontrolhub_schema_migrations`).Scan(&appliedVersion); err != nil {
		return fmt.Errorf("read schema migration version: %w", err)
	}
	if appliedVersion > currentSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than this QControlHub binary supports (%d)", appliedVersion, currentSchemaVersion)
	}
	if appliedVersion == currentSchemaVersion {
		return nil
	}

	tx, err := connection.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}
	defer tx.Rollback(ctx)
	// substore_sync_targets was split out of the legacy single-target settings
	// row in schema v31. Create and populate that table before the rest of the
	// schema so the legacy substore_sync_items rows can receive their target_id.
	// Keep this compatibility work tied to the v31 boundary; running it from
	// schemaSQL would recreate the default group after a later migration finds
	// the local target table empty.
	if appliedVersion < 31 {
		if _, err := tx.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS substore_sync_targets (
				id text PRIMARY KEY,
				display_name varchar(100) NOT NULL,
				subscription_name varchar(100) NOT NULL UNIQUE,
				integration_id text NOT NULL UNIQUE,
				sync_mode varchar(12) NOT NULL DEFAULT 'managed' CHECK (sync_mode IN ('incremental','managed')),
				last_synced_at timestamptz,
				last_sync_status varchar(10) NOT NULL DEFAULT 'never' CHECK (last_sync_status IN ('never','success','failed')),
				last_sync_error varchar(500) NOT NULL DEFAULT '',
				created_at timestamptz NOT NULL,
				updated_at timestamptz NOT NULL
			)`); err != nil {
			return fmt.Errorf("prepare legacy Sub-Store sync target: %w", err)
		}
		var legacySettingsTable bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass('substore_sync_settings') IS NOT NULL`).Scan(&legacySettingsTable); err != nil {
			return fmt.Errorf("inspect legacy Sub-Store settings: %w", err)
		}
		if legacySettingsTable {
			if _, err := tx.Exec(ctx, `
			INSERT INTO substore_sync_targets (
				id,display_name,subscription_name,integration_id,last_synced_at,last_sync_status,last_sync_error,created_at,updated_at
			)
			SELECT
				'sst_default',subscription_name,subscription_name,integration_id,last_synced_at,last_sync_status,last_sync_error,updated_at,updated_at
			FROM substore_sync_settings
			WHERE NOT EXISTS (SELECT 1 FROM substore_sync_targets)
			ON CONFLICT DO NOTHING`); err != nil {
				return fmt.Errorf("migrate legacy Sub-Store sync target: %w", err)
			}
		}
	}
	if appliedVersion < 51 {
		// The unique constraint on (agent_id, port) already serves every lookup
		// this index could: it has the same leading columns, so the planner can
		// use it for any predicate on agent_id alone. A live instance showed the
		// duplicate carrying zero scans while it was maintained on every policy
		// write. schemaSQL no longer creates it either, so this only has to
		// remove the copies that already exist.
		if _, err := tx.Exec(ctx, `DROP INDEX IF EXISTS port_traffic_policies_agent_idx`); err != nil {
			return fmt.Errorf("drop duplicate traffic policy index: %w", err)
		}
	}
	if appliedVersion < 50 {
		// Agent metrics move into a narrow side table and the agents row is
		// squeezed, so that the once-per-second push and the once-per-heartbeat
		// liveness stamp both update on-page instead of rewriting a wide row and
		// every index that points at it.
		if err := splitAgentLiveState(ctx, tx); err != nil {
			return fmt.Errorf("split agent live state: %w", err)
		}
	}
	if appliedVersion < 49 {
		// Kernel logs become a table partitioned by receive day. Converting in
		// place is impossible, and copying millions of retained rows would turn
		// a version upgrade into minutes of blocked writes on the instances this
		// panel targets. Retained logs are time-bounded and already truncated by
		// retention, so the previous rows are set aside under a timestamped
		// legacy name and the empty partitioned table takes over immediately.
		// Janitor reads the age out of that name and drops the copy once the
		// grace period passes.
		//
		// This runs before schemaSQL on purpose: the partitioned table is
		// created with IF NOT EXISTS, so an existing heap table has to move out
		// of the way first, and its indexes have to go with it or the covering
		// index name would already be taken.
		var partitioned bool
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(bool_or(class.relkind = 'p'), false)
			FROM pg_class class
			JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
			WHERE class.relname = 'core_logs' AND namespace.nspname = current_schema()`).Scan(&partitioned); err != nil {
			return fmt.Errorf("inspect core log table kind: %w", err)
		}
		if !partitioned {
			if _, err := tx.Exec(ctx, `DROP INDEX IF EXISTS core_logs_agent_engine_covering_idx`); err != nil {
				return fmt.Errorf("drop previous covering index: %w", err)
			}
			// The single-column recency indexes were only maintained, never
			// read: the store's single read path always filters by engine and
			// orders by id within an agent.
			for _, index := range []string{
				"core_logs_agent_engine_recent_idx",
				"core_logs_agent_recent_idx",
				"core_logs_engine_recent_idx",
			} {
				if _, err := tx.Exec(ctx, "DROP INDEX IF EXISTS "+index); err != nil {
					return fmt.Errorf("drop superseded core log index %s: %w", index, err)
				}
			}
			// Copies left by earlier interrupted attempts are released before
			// this one contributes its own.
			if err := dropLegacyCoreLogTables(ctx, tx); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `ALTER TABLE IF EXISTS core_logs RENAME TO `+
				pgx.Identifier{legacyCoreLogsPrefix + time.Now().UTC().Format("20060102150405")}.Sanitize()); err != nil {
				return fmt.Errorf("set aside previous core logs: %w", err)
			}
		}
	}
	if _, err := tx.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply PostgreSQL schema: %w", err)
	}
	if appliedVersion < 52 {
		// Legacy selections referred only to a node/engine/tag. Pin them to the
		// deployed configuration before independent user workspaces can replace
		// that service with another user's identically named inbound.
		if _, err := tx.Exec(ctx, `UPDATE substore_sync_items item SET config_id=COALESCE((
			SELECT task.config_id FROM tasks task
			WHERE task.agent_id=item.agent_id AND task.engine=item.engine
			  AND task.action IN ('deploy','import-existing') AND task.status='succeeded'
			ORDER BY task.finished_at DESC,task.created_at DESC,task.id DESC LIMIT 1
		),'') WHERE item.config_id=''`); err != nil {
			return fmt.Errorf("pin legacy Sub-Store configurations: %w", err)
		}
	}
	if appliedVersion < 54 {
		// Legacy nodes and add-node credentials have no trustworthy creator
		// identity; keep them with the administrator instead of guessing an
		// owner from whichever user most recently deployed a configuration.
		if _, err := tx.Exec(ctx, `UPDATE panel_users SET agent_isolation=(role<>'admin')`); err != nil {
			return fmt.Errorf("isolate panel accounts: %w", err)
		}
	}
	if appliedVersion < 55 {
		if err := s.migrateSubStoreBackends(ctx, tx); err != nil {
			return err
		}
		// Legacy port display overrides belong to the exact last deployed
		// configuration, not to the next account that reuses its listener.
		if _, err := tx.Exec(ctx, `INSERT INTO config_client_preferences(config_id,label,value)
			SELECT DISTINCT deployed.config_id,entry.key,entry.value
			FROM (`+latestDeploymentsSQL+`) deployed JOIN agents agent ON agent.id=deployed.agent_id
			JOIN configs config ON config.id=deployed.config_id
			CROSS JOIN LATERAL jsonb_each_text(COALESCE(NULLIF(agent.labels,'null'::jsonb),'{}'::jsonb)) entry
			WHERE entry.key ~ '^client_profile_(name|address|family)_[0-9a-f]{64}$'
			ON CONFLICT DO NOTHING`); err != nil {
			return fmt.Errorf("scope client display preferences: %w", err)
		}
	}
	if appliedVersion < 56 {
		// Pre-consent grants become pending, never silently accepted. Preserve
		// accounting and reservations, but invalidate cached allocation forms
		// and work queued under the former immediate-access model.
		if _, err := tx.Exec(ctx, `UPDATE agents SET sharing_revision=sharing_revision+1
			WHERE id IN(SELECT agent_id FROM agent_shares WHERE status<>'accepted');
			UPDATE panel_users SET agent_access_revision=agent_access_revision+1
			WHERE id IN(SELECT user_id FROM agent_shares WHERE status<>'accepted');
			UPDATE tasks t SET status=CASE WHEN t.status='pending' THEN 'canceled' ELSE 'failed' END,
				error='Agent sharing requires recipient acceptance; previous execution may be unknown',
				finished_at=now(),config_content=NULL,lease_id=NULL
			WHERE t.status IN ('pending','running') AND ((`+unauthorizedTaskPrincipalSQL+`)
				OR EXISTS(SELECT 1 FROM agent_shares s WHERE s.id=t.shared_traffic_id AND s.status<>'accepted'))`); err != nil {
			return fmt.Errorf("require Agent sharing consent: %w", err)
		}
	}
	if appliedVersion < 57 {
		// Legacy grants did not name engines. Do not guess or widen them:
		// owners must allocate engines and recipients must consent again.
		// Retain reservations and cumulative usage. Obsolete host snapshots
		// predate dispatch-time read authorization and cannot be trusted.
		if _, err := tx.Exec(ctx, `UPDATE agent_shares SET engines='{}',
				status=CASE WHEN status='rejected' THEN status ELSE 'pending' END,
				invitation_revision=invitation_revision+1,updated_at=now();
			UPDATE agents SET sharing_revision=sharing_revision+1
				WHERE id IN(SELECT agent_id FROM agent_shares);
			UPDATE panel_users SET agent_access_revision=agent_access_revision+1
				WHERE id IN(SELECT user_id FROM agent_shares);
			UPDATE tasks SET config_content=NULL
				WHERE action IN ('read-config','read-managed-config') AND status NOT IN ('pending','running');
			UPDATE tasks t SET status=CASE WHEN status='running' THEN 'failed' ELSE 'canceled' END,
				error='task authorization upgraded; submit a new task; previous execution may be unknown',
				finished_at=now(),config_content=NULL,lease_id=NULL
				WHERE status IN ('pending','running') AND
					(t.shared_traffic_id<>'' OR t.action IN ('read-config','read-managed-config','import-existing','validate','deploy')
						OR (`+unauthorizedTaskPrincipalSQL+`))`); err != nil {
			return fmt.Errorf("scope shared engines and invalidate obsolete tasks: %w", err)
		}
	}
	if appliedVersion < 58 {
		// Owner-hidden nodes stay invisible to every administrator view while
		// they keep running and accounting. The flag is chosen when the node is
		// added and copied from the enrollment credential on first enrollment.
		if _, err := tx.Exec(ctx, `ALTER TABLE IF EXISTS agents ADD COLUMN IF NOT EXISTS admin_hidden boolean NOT NULL DEFAULT false;
			ALTER TABLE IF EXISTS enrollment_tokens ADD COLUMN IF NOT EXISTS admin_hidden boolean NOT NULL DEFAULT false;`); err != nil {
			return fmt.Errorf("add owner-hidden node flag: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO qcontrolhub_schema_migrations (version) VALUES ($1)`, currentSchemaVersion); err != nil {
		return fmt.Errorf("record schema migration version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit schema migration: %w", err)
	}
	// The log table is partitioned by receive day and needs a partition for
	// today before the first upload arrives, so this runs once the migration
	// committed. Creating the partitions inside the transaction would be fine
	// too, but keeping it outside lets the maintenance loop reuse the same path.
	if err := s.EnsureCoreLogPartitions(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("prepare core log partitions: %w", err)
	}
	return nil
}

// Heartbeat records a complete authenticated Agent heartbeat. The advertised
// features are authoritative: an empty, omitted, or [] feature list clears any
// stale value (for example a previous session's mihomo-development-source-v1),
// so a legacy Agent that reconnects cannot inherit a stale capability the
// control plane would otherwise use to dispatch a mirror task. Metrics-only
// refreshes go through UpdateAgentMetrics, which never touches features.
func (s *Store) Heartbeat(ctx context.Context, id string, heartbeat core.HeartbeatRequest) error {
	return s.HeartbeatWithPublicIPProbeTrust(ctx, id, heartbeat, PublicIPProbeTrust{})
}

// HeartbeatWithPublicIPProbeTrust records a complete heartbeat while binding
// managed public-IP provenance to capability and configuration established by
// the current authenticated WSS session.
func (s *Store) HeartbeatWithPublicIPProbeTrust(ctx context.Context, id string, heartbeat core.HeartbeatRequest, trust PublicIPProbeTrust) error {
	receivedAt := time.Now().UTC()
	heartbeat.Version = strings.TrimSpace(heartbeat.Version)
	heartbeat.OS = strings.TrimSpace(heartbeat.OS)
	heartbeat.Arch = strings.TrimSpace(heartbeat.Arch)
	if utf8.RuneCountInString(heartbeat.Version) > 100 {
		return fmt.Errorf("%w: agent version exceeds 100 characters", ErrInvalid)
	}
	if utf8.RuneCountInString(heartbeat.OS) > 50 || utf8.RuneCountInString(heartbeat.Arch) > 50 {
		return fmt.Errorf("%w: agent OS and architecture must not exceed 50 characters", ErrInvalid)
	}
	runtimeState, err := json.Marshal(heartbeat.Runtime)
	if err != nil {
		return err
	}
	if heartbeat.Metrics != nil {
		metrics := *heartbeat.Metrics
		applyPublicIPProbeTrust(&metrics, trust)
		heartbeat.Metrics = &metrics
	}
	metricsState, err := encodeHeartbeatMetrics(heartbeat.Metrics, receivedAt)
	if err != nil {
		return err
	}
	featuresState, err := json.Marshal(heartbeat.Features)
	if err != nil {
		return err
	}
	if len(heartbeat.Features) == 0 {
		featuresState = []byte(`[]`)
	}
	// The observed address is control-plane state that a WSS session supplies
	// with the heartbeat, so it is persisted in its own column rather than left
	// inside the snapshot a later metrics push would replace. A heartbeat
	// without metrics carries no observation and must keep the stored value.
	observedPublicIP := ""
	if heartbeat.Metrics != nil {
		observedPublicIP = heartbeat.Metrics.ObservedPublicIP
	}
	command, err := s.pool.Exec(ctx, `
			UPDATE agents SET last_seen=now(), version=CASE WHEN $2='' THEN version ELSE $2 END, runtime=$3,
			                  features=$4::jsonb,
			                  os=CASE WHEN $5='' THEN os ELSE $5 END,
			                  arch=CASE WHEN $6='' THEN arch ELSE $6 END,
			                  observed_public_ip=CASE WHEN $7='' THEN observed_public_ip ELSE $7 END
			WHERE id=$1 AND revoked_at IS NULL`, id, heartbeat.Version, runtimeState, featuresState, heartbeat.OS, heartbeat.Arch, observedPublicIP)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	// The Agent-reported snapshot is large and changes on every push, so it is
	// written to its own narrow table where the update can stay on-page. A
	// heartbeat without metrics reports that this Agent can no longer probe.
	if metricsState == nil {
		if err := s.clearAgentLiveStateProbes(ctx, id); err != nil {
			return err
		}
	} else if err := s.recordAgentLiveState(ctx, id, metricsState); err != nil {
		return err
	}
	return s.UpdatePortTrafficUsage(ctx, id, heartbeat.TrafficUsage, receivedAt)
}

// UpdateAgentMetrics refreshes only the live metrics snapshot from the
// high-frequency metrics pushes. The push proves liveness, so last_seen is
// refreshed as well, while version, runtime, and features stay untouched.
func (s *Store) UpdateAgentMetrics(ctx context.Context, id string, metrics core.HostMetrics) error {
	return s.UpdateAgentMetricsWithPublicIPProbeTrust(ctx, id, metrics, PublicIPProbeTrust{})
}

// UpdateAgentMetricsWithPublicIPProbeTrust applies the same current-session
// provenance constraint to metrics-only refreshes without changing persisted
// features.
func (s *Store) UpdateAgentMetricsWithPublicIPProbeTrust(ctx context.Context, id string, metrics core.HostMetrics, trust PublicIPProbeTrust) error {
	applyPublicIPProbeTrust(&metrics, trust)
	metricsState, err := encodeHeartbeatMetrics(&metrics, time.Now().UTC())
	if err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `
			UPDATE agents SET last_seen=now()
			WHERE id=$1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return s.recordAgentLiveState(ctx, id, metricsState)
}

// UpdateAgentObservedPublicIP stores the authenticated WSS peer address for the
// client address resolver. It is kept in its own column so that the
// high-frequency metrics snapshot, which never carries this key, cannot
// overwrite it. An empty value removes a stale observation so the resolver
// falls back to the current interface snapshot.
func (s *Store) UpdateAgentObservedPublicIP(ctx context.Context, id, address string) error {
	address = strings.TrimSpace(address)
	if address != "" {
		address = authn.NormalizePublicIP(address)
		if address == "" {
			return fmt.Errorf("%w: invalid observed public agent address", ErrInvalid)
		}
		parsed, err := netip.ParseAddr(address)
		if err != nil || netpolicy.IsCloudflareAddress(parsed) {
			address = ""
		}
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE agents SET observed_public_ip=$2
		WHERE id=$1 AND revoked_at IS NULL`, id, address)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListAgents(ctx context.Context) ([]core.Agent, error) {
	args := []any{}
	where := agentAccessClause(ctx, "agents.id", &args)
	query := scopedAgentsSQL(ctx, &args)
	rows, err := s.pool.Query(ctx, query+where+` ORDER BY enrolled_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	return scanAgents(ctx, rows)
}

// ListAgentsWithEnrollmentCommands pipelines the panel's two independent
// reads on one connection. Only callers authorized to manage enrollment should
// use it; ciphertext is verified locally and never included in the response.
func (s *Store) ListAgentsWithEnrollmentCommands(ctx context.Context) ([]core.Agent, error) {
	args := []any{}
	where := agentAccessClause(ctx, "agents.id", &args)
	query := scopedAgentsSQL(ctx, &args)
	batch := &pgx.Batch{}
	batch.Queue(query+where+` ORDER BY enrolled_at DESC`, args...)
	enrollmentQuery, enrollmentArgs := enrollmentAvailabilityQuery(ctx, nil)
	batch.Queue(enrollmentQuery, enrollmentArgs...)
	results := s.pool.SendBatch(ctx, batch)
	defer results.Close()
	rows, err := results.Query()
	if err != nil {
		return nil, err
	}
	agents, err := scanAgents(ctx, rows)
	if err != nil {
		return nil, err
	}
	rows, err = results.Query()
	if err != nil {
		return nil, err
	}
	available, err := s.scanEnrollmentCommandAvailability(rows)
	if err != nil {
		return nil, err
	}
	if err := results.Close(); err != nil {
		return nil, err
	}
	for index := range agents {
		agents[index].EnrollmentCommandAvailable = available[agents[index].ID]
	}
	return agents, nil
}

const listAgentsSQL = listAgentsSQLBase + ` ORDER BY enrolled_at DESC`

const agentOfflineThresholdSQL = `(CASE WHEN agents.owner_id='' THEN (SELECT agent_offline_threshold_seconds FROM panel_settings WHERE id=1)
	ELSE COALESCE((SELECT (runtime->>'agent_offline_threshold_seconds')::integer FROM user_panel_settings WHERE owner_id=agents.owner_id),45) END)`

const agentColumnsSQL = `
			SELECT id,name,version,os,arch,capabilities,features,labels,runtime,observed_public_ip,
				(SELECT metrics FROM agent_live_state WHERE agent_id=agents.id),
				last_seen,enrolled_at,
				` + agentOfflineThresholdSQL + `,supported_capabilities,` + capabilityTransitionsSQL + `,owner_id,admin_hidden`

const agentSelectFromSQL = ` FROM agents WHERE revoked_at IS NULL`
const listAgentsSQLBase = agentColumnsSQL + `,'{}'::text[]` + agentSelectFromSQL

func scopedAgentsSQL(ctx context.Context, args *[]any) string {
	if scopeForConfig(ctx).Admin {
		return listAgentsSQLBase
	}
	*args = append(*args, scopeForConfig(ctx).OwnerID)
	return agentColumnsSQL + fmt.Sprintf(`,COALESCE((SELECT engines FROM agent_shares
		WHERE agent_id=agents.id AND user_id=$%d AND enabled AND status='accepted'),'{}'::text[])`, len(*args)) + agentSelectFromSQL
}

func scanAgents(ctx context.Context, rows pgx.Rows) ([]core.Agent, error) {
	defer rows.Close()
	agents := make([]core.Agent, 0)
	now := time.Now().UTC()
	for rows.Next() {
		var agent core.Agent
		var capabilities, features, labels, runtimeState, metricsState []byte
		var observedPublicIP string
		var offlineThresholdSeconds int
		if err := rows.Scan(&agent.ID, &agent.Name, &agent.Version, &agent.OS, &agent.Arch, &capabilities, &features, &labels, &runtimeState, &observedPublicIP, &metricsState, &agent.LastSeen, &agent.EnrolledAt, &offlineThresholdSeconds, &agent.SupportedCapabilities, &agent.CapabilityTransitions, &agent.OwnerID, &agent.AdminHidden, &agent.SharedEngines); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(capabilities, &agent.Capabilities); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(features, &agent.Features); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(labels, &agent.Labels); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(runtimeState, &agent.Runtime); err != nil {
			return nil, err
		}
		if err := decodeAgentMetrics(metricsState, observedPublicIP, &agent.Metrics); err != nil {
			return nil, err
		}
		if agent.LastSeen.After(now.Add(-time.Duration(offlineThresholdSeconds) * time.Second)) {
			agent.Status = "online"
		} else {
			agent.Status = "offline"
		}
		scopeAgentPresentation(ctx, &agent)
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	if err := requireAgentDeletion(ctx, s.pool, id); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var enrollmentID *string
	err = tx.QueryRow(ctx, `
		UPDATE agents SET revoked_at=now()
		WHERE id=$1 AND revoked_at IS NULL
		RETURNING enrollment_id`, id).Scan(&enrollmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE tasks SET status='failed', error='agent identity was revoked', finished_at=now(), config_content=NULL, lease_id=NULL
		WHERE agent_id=$1 AND status IN ('pending','running')`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE configs SET deleted_at=now(),content='',updated_at=now()
			WHERE agent_id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM config_revisions WHERE config_id IN (SELECT id FROM configs WHERE agent_id=$1)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_daily_usage WHERE agent_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_daily_accounting WHERE agent_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_accounting_epochs WHERE agent_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_policies WHERE agent_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM substore_sync_items WHERE agent_id=$1`, id); err != nil {
		return err
	}
	// A partitioned core_logs table cannot carry ON DELETE CASCADE, so the
	// node's logs and their deduplication markers are removed here instead. The
	// covering index serves the lookup, so this stays a bounded delete.
	if _, err := tx.Exec(ctx, `DELETE FROM core_logs WHERE agent_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core_log_batches WHERE agent_id=$1`, id); err != nil {
		return err
	}
	legacyEnrollmentID := ""
	if enrollmentID != nil {
		legacyEnrollmentID = strings.TrimSpace(*enrollmentID)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM enrollment_tokens
		WHERE agent_id=$1 OR id=NULLIF($2,'')`, id, legacyEnrollmentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AgentName returns the display name of an active registered agent.
func (s *Store) AgentName(ctx context.Context, id string) (string, error) {
	if err := requireAgentAccess(ctx, s.pool, id); err != nil {
		return "", err
	}
	var name string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM agents WHERE id=$1 AND revoked_at IS NULL`, id).Scan(&name); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Store) CreateConfig(ctx context.Context, input core.Config) (core.Config, error) {
	if input.AgentID != "" {
		return core.Config{}, fmt.Errorf("%w: node-owned configurations must use the agent configuration workflow", ErrInvalid)
	}
	if err := core.ValidateConfig(input.Engine, input.Content); err != nil {
		return core.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	name, description, err := validateConfigMetadata(input.Name, input.Description)
	if err != nil {
		return core.Config{}, err
	}
	id, err := core.NewID("cfg")
	if err != nil {
		return core.Config{}, err
	}
	storedContent, err := s.encryptContent(input.Content)
	if err != nil {
		return core.Config{}, err
	}
	now := time.Now().UTC()
	config := core.Config{
		ID: id, OwnerID: scopeForConfig(ctx).OwnerID, AgentID: input.AgentID, Name: name, Description: description,
		Engine: input.Engine, Content: input.Content, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
			INSERT INTO configs (id,agent_id,name,description,engine,content,version,created_at,updated_at,owner_id)
		VALUES ($1,NULLIF($2,''),$3,$4,$5,$6,$7,$8,$8,$9)`,
		config.ID, config.AgentID, config.Name, config.Description, config.Engine, storedContent, config.Version, now, config.OwnerID)
	if err != nil {
		return core.Config{}, mapError(err)
	}
	if err := s.insertConfigRevision(ctx, tx, config); err != nil {
		return core.Config{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Config{}, err
	}
	return config, nil
}

func (s *Store) UpdateConfig(ctx context.Context, id string, input core.Config) (core.Config, error) {
	if err := core.ValidateConfig(input.Engine, input.Content); err != nil {
		return core.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	name, description, err := validateConfigMetadata(input.Name, input.Description)
	if err != nil {
		return core.Config{}, err
	}
	if input.Version < 1 {
		return core.Config{}, fmt.Errorf("%w: configuration version is required", ErrInvalid)
	}
	storedContent, err := s.encryptContent(input.Content)
	if err != nil {
		return core.Config{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, err
	}
	defer tx.Rollback(ctx)
	var config core.Config
	args := []any{id, name, description, input.Engine, storedContent, input.Version}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	err = tx.QueryRow(ctx, `
		UPDATE configs SET name=$2,description=$3,engine=$4,content=$5,version=version+1,updated_at=now()
		WHERE id=$1 AND deleted_at IS NULL AND agent_id IS NULL AND version=$6`+ownerWhere+`
		RETURNING id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at,owner_id`,
		args...).Scan(
		&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content, &config.Version, &config.CreatedAt, &config.UpdatedAt, &config.OwnerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var exists bool
			existsArgs := []any{id}
			existsWhere := ownerClause(ctx, "owner_id", &existsArgs)
			if existsErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM configs WHERE id=$1 AND deleted_at IS NULL AND agent_id IS NULL`+existsWhere+`)`, existsArgs...).Scan(&exists); existsErr != nil {
				return core.Config{}, existsErr
			}
			if exists {
				return core.Config{}, fmt.Errorf("%w: configuration changed; reload before saving", ErrConflict)
			}
			return core.Config{}, ErrNotFound
		}
		return core.Config{}, mapError(err)
	}
	config.Content, err = s.decryptContent(config.Content)
	if err != nil {
		return core.Config{}, err
	}
	if err := s.insertConfigRevision(ctx, tx, config); err != nil {
		return core.Config{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Config{}, err
	}
	return config, nil
}

func (s *Store) DeleteConfig(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	args := []any{id}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	command, err := tx.Exec(ctx, `UPDATE configs SET deleted_at=now(),content='' WHERE id=$1 AND deleted_at IS NULL AND agent_id IS NULL`+ownerWhere, args...)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM config_revisions WHERE config_id=$1`, id); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE tasks SET status='failed',error='configuration was deleted before dispatch',finished_at=now(),config_content=NULL,lease_id=NULL
		WHERE config_id=$1 AND status='pending'`, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListConfigs(ctx context.Context) ([]core.Config, error) {
	args := []any{}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	rows, err := s.pool.Query(ctx, `
		SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at,owner_id
		FROM configs WHERE deleted_at IS NULL AND agent_id IS NULL`+ownerWhere+` ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configs := make([]core.Config, 0)
	for rows.Next() {
		var config core.Config
		if err := rows.Scan(&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content, &config.Version, &config.CreatedAt, &config.UpdatedAt, &config.OwnerID); err != nil {
			return nil, err
		}
		config.Content, err = s.decryptContent(config.Content)
		if err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	return configs, rows.Err()
}

func (s *Store) ExistingConfigIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	existing := make(map[string]bool)
	if len(ids) == 0 {
		return existing, nil
	}
	args := []any{ids}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	ownerWhere += configAgentAccessClause(ctx, "configs.agent_id", "configs.engine", &args)
	rows, err := s.pool.Query(ctx, `SELECT id FROM configs WHERE deleted_at IS NULL AND id=ANY($1::text[])`+ownerWhere, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		existing[id] = true
	}
	return existing, rows.Err()
}

func (s *Store) CreateTask(ctx context.Context, request core.TaskRequest) (core.Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Task{}, err
	}
	defer tx.Rollback(ctx)
	task, err := s.createTaskTx(ctx, tx, request)
	if err != nil {
		return core.Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Task{}, err
	}
	if !task.Reused {
		s.signalTaskReady(task.AgentID)
	}
	return task, nil
}

// The caller commits before notifying the Agent. This also allows a preset
// revision and its exact task snapshot to be published in one transaction.
func (s *Store) createTaskTx(ctx context.Context, tx pgx.Tx, request core.TaskRequest) (core.Task, error) {
	scope := scopeForConfig(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.Task{}, err
	}
	if err := requireTaskPermission(ctx, tx, request.Action, false); err != nil {
		return core.Task{}, err
	}
	if scope.Admin && request.ConfigID != "" {
		// An administrator may deploy someone else's configuration. Lock its
		// durable owner before the Agent, just like the owner's own request.
		rows, err := tx.Query(ctx, `SELECT id FROM panel_users
			WHERE id=(SELECT owner_id FROM configs WHERE id=$1) FOR SHARE`, request.ConfigID)
		if err != nil {
			return core.Task{}, err
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return core.Task{}, err
		}
	}
	if err := requireAgentAccess(ctx, tx, request.AgentID); err != nil {
		return core.Task{}, err
	}
	if request.InstallIfMissing || (request.Action != core.ActionDeploy && request.Action != core.ActionValidate && request.Action != core.ActionStatus) {
		if err := requireAgentAdministration(ctx, tx, request.AgentID); err != nil {
			return core.Task{}, err
		}
	}
	if request.InstallIfMissing && (request.Action != core.ActionValidate && request.Action != core.ActionDeploy ||
		request.ExpectedConfigVersion < 1 || request.CoreVersion != "" || request.CoreSource != "") {
		return core.Task{}, fmt.Errorf("%w: automatic installation requires an exact configuration revision and validate or deploy intent", ErrInvalid)
	}
	if request.ExpectedConfigVersion < 0 || (request.ExpectedConfigVersion != 0 && request.Action != core.ActionDeploy && request.Action != core.ActionValidate && request.Action != core.ActionImportExisting) {
		return core.Task{}, fmt.Errorf("%w: expected configuration version requires a configuration task", ErrInvalid)
	}
	if request.Action == core.ActionConfigureTCP {
		settings, err := core.NormalizeTCPSettings(request.TCPSettings)
		if err != nil {
			return core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		request.TCPSettings = settings
	} else if len(request.TCPSettings) != 0 {
		return core.Task{}, fmt.Errorf("%w: TCP settings are only accepted by configure-tcp", ErrInvalid)
	}
	if !request.Action.Valid() {
		return core.Task{}, fmt.Errorf("%w: unsupported action %q", ErrInvalid, request.Action)
	}
	if request.Action.AgentLevel() {
		if request.Engine != "" || request.ConfigID != "" || request.CoreVersion != "" {
			return core.Task{}, fmt.Errorf("%w: agent-level tasks cannot reference an engine, configuration, or core version", ErrInvalid)
		}
	} else if !request.Engine.Valid() {
		return core.Task{}, fmt.Errorf("%w: unsupported engine %q", ErrInvalid, request.Engine)
	}
	if !request.Action.AgentLevel() {
		if err := requireAgentEngineAccess(ctx, tx, request.AgentID, request.Engine); err != nil {
			return core.Task{}, err
		}
	}
	if request.Action == core.ActionInstall {
		normalizedVersion, versionErr := core.NormalizeCoreVersionSelector(request.CoreVersion)
		if versionErr != nil {
			return core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, versionErr)
		}
		request.CoreVersion = normalizedVersion
		source, sourceErr := core.NormalizeCoreSource(request.Engine, normalizedVersion, request.CoreSource)
		if sourceErr != nil {
			return core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, sourceErr)
		}
		request.CoreSource = source
		if request.ConfigID != "" {
			return core.Task{}, fmt.Errorf("%w: install tasks cannot reference a configuration", ErrInvalid)
		}
	} else {
		if request.CoreSource != "" {
			return core.Task{}, fmt.Errorf("%w: core source is only applicable to Mihomo development installs", ErrInvalid)
		}
		request.CoreSource = ""
		request.CoreVersion = ""
	}
	var capabilitiesJSON, featuresJSON, runtimeJSON []byte
	if err := tx.QueryRow(ctx, `SELECT capabilities,features,runtime FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, request.AgentID).Scan(&capabilitiesJSON, &featuresJSON, &runtimeJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return core.Task{}, fmt.Errorf("agent: %w", ErrNotFound)
		}
		return core.Task{}, err
	}
	var capabilities []core.Engine
	if err := json.Unmarshal(capabilitiesJSON, &capabilities); err != nil {
		return core.Task{}, err
	}
	var features []string
	if err := json.Unmarshal(featuresJSON, &features); err != nil {
		return core.Task{}, err
	}
	var runtime map[core.Engine]core.RuntimeState
	if err := json.Unmarshal(runtimeJSON, &runtime); err != nil {
		return core.Task{}, err
	}
	if request.Action == core.ActionReadConfig || request.Action == core.ActionReadManagedConfig || request.Action == core.ActionImportExisting {
		if err := requireHostConfigRead(ctx, tx, request.AgentID, request.Engine); err != nil {
			return core.Task{}, err
		}
	}
	if request.Action == core.ActionUpgradeAgent && !containsFeature(features, core.AgentFeatureSelfUpgrade) {
		return core.Task{}, fmt.Errorf("%w: this Agent does not support remote upgrades; run the current one-click installation once", ErrConflict)
	}
	if request.Action.SystemBBR() && !containsFeature(features, core.AgentFeatureSystemBBR) {
		return core.Task{}, fmt.Errorf("%w: upgrade this Agent before managing system BBR", ErrConflict)
	}
	if request.Action.SystemBBR() {
		// TCP settings affect the whole host. Keep the shared exclusion even
		// though task reuse and visibility are scoped to their submitter.
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks
			WHERE agent_id=$1 AND action IN ('enable-bbr','disable-bbr','configure-tcp')
			  AND status IN ('pending','running') AND owner_id<>$2)`, request.AgentID, scope.OwnerID).Scan(&busy); err != nil {
			return core.Task{}, err
		}
		if busy {
			return core.Task{}, fmt.Errorf("%w: another system TCP task is pending or running", ErrConflict)
		}
	}
	if !request.Action.AgentLevel() {
		if err := rejectPendingCapabilityTransition(ctx, tx, request.AgentID, request.Engine); err != nil {
			return core.Task{}, err
		}
		if !containsEngine(capabilities, request.Engine) {
			return core.Task{}, fmt.Errorf("%w: agent does not advertise the requested engine", ErrInvalid)
		}
		if reason := strings.TrimSpace(runtime[request.Engine].ExistingConfigUnsupportedReason); reason != "" {
			return core.Task{}, fmt.Errorf("%w: %s core tasks are disabled because an existing service could not be mapped safely: %s", ErrConflict, request.Engine, reason)
		}
	}
	if (request.Action == core.ActionValidate || request.Action == core.ActionDeploy) &&
		!containsFeature(features, core.AgentFeatureIndependentEgress) {
		return core.Task{}, fmt.Errorf("%w: upgrade the Agent before validating or deploying independent exits", ErrConflict)
	}
	if request.InstallIfMissing && !containsFeature(features, core.AgentFeaturePresetAutoInstall) {
		return core.Task{}, fmt.Errorf("%w: upgrade the Agent before automatically installing a stable core with an inbound", ErrConflict)
	}
	if request.Action == core.ActionReadManagedConfig && !containsFeature(features, core.AgentFeatureManagedConfigRead) {
		return core.Task{}, fmt.Errorf("%w: this Agent cannot read the managed configuration independently; upgrade the Agent through the panel first", ErrConflict)
	}
	if request.Action == core.ActionStart || request.Action == core.ActionRestart {
		if err := requireSafeEngineStart(ctx, tx, request.AgentID, request.Engine); err != nil {
			return core.Task{}, err
		}
	}
	if request.Action == core.ActionInstall && request.Engine == core.EngineMihomo &&
		request.CoreVersion == core.CoreVersionDevelopment &&
		request.CoreSource == string(core.CoreSourceMirror) &&
		!containsFeature(features, core.AgentFeatureMihomoDevelopmentSource) {
		return core.Task{}, fmt.Errorf("%w: this Agent does not support the Mihomo Alpha mirror source; upgrade the Agent through the panel first", ErrConflict)
	}

	task := core.Task{
		InstallIfMissing: request.InstallIfMissing,
		TCPSettings:      request.TCPSettings,
		AgentID:          request.AgentID, Action: request.Action, Engine: request.Engine,
		ConfigID: request.ConfigID, CoreVersion: request.CoreVersion, CoreSource: request.CoreSource,
		Status: core.TaskPending, CreatedAt: time.Now().UTC(),
	}
	if task.TCPSettings == nil {
		task.TCPSettings = core.TCPSettings{}
	}
	if request.Action == core.ActionDeploy || request.Action == core.ActionValidate || request.Action == core.ActionImportExisting {
		var configEngine core.Engine
		var configAgentID, configOwnerID string
		configArgs := []any{request.ConfigID}
		configWhere := ownerClause(ctx, "owner_id", &configArgs)
		err := tx.QueryRow(ctx, `SELECT engine,content,version,COALESCE(agent_id,''),owner_id FROM configs WHERE id=$1 AND deleted_at IS NULL`+configWhere+` FOR UPDATE`, configArgs...).Scan(&configEngine, &task.ConfigContent, &task.ConfigVersion, &configAgentID, &configOwnerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return core.Task{}, fmt.Errorf("configuration: %w", ErrNotFound)
		}
		if err != nil {
			return core.Task{}, err
		}
		if configEngine != request.Engine {
			return core.Task{}, fmt.Errorf("%w: task engine does not match configuration engine", ErrInvalid)
		}
		if request.ExpectedConfigVersion != 0 && task.ConfigVersion != request.ExpectedConfigVersion {
			return core.Task{}, fmt.Errorf("%w: configuration changed after saving; no task was submitted, reload before deployment", ErrConflict)
		}
		if request.Action == core.ActionImportExisting && configAgentID != request.AgentID {
			return core.Task{}, fmt.Errorf("%w: existing service migration requires this agent's saved snapshot", ErrInvalid)
		}
		if configAgentID != "" && configAgentID != request.AgentID {
			return core.Task{}, fmt.Errorf("%w: node-owned configuration cannot be deployed to another agent", ErrInvalid)
		}
		task.ConfigContent, err = s.decryptContent(task.ConfigContent)
		if err != nil {
			return core.Task{}, err
		}
		settings, settingsErr := s.panelSettingsForOwner(ctx, tx, scopeForConfig(ctx).OwnerID)
		if settingsErr != nil {
			return core.Task{}, settingsErr
		}
		if settings.CNIPSource != nil && settings.CNIPSource.Custom() && request.Action != core.ActionImportExisting {
			if !containsFeature(features, core.AgentFeatureCNIPSource) {
				return core.Task{}, fmt.Errorf("%w: 请先升级 Agent，以支持自定义 CN IP 数据源", ErrConflict)
			}
			source := *settings.CNIPSource
			task.CNIPSource = &source
		}
		if request.Action == core.ActionValidate || request.Action == core.ActionDeploy {
			if err := serverconfig.ValidateIndependentEgress(task.Engine, task.ConfigContent); err != nil {
				return core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, err)
			}
		}
		if err := s.prepareSharedTaskTx(ctx, tx, &task, configOwnerID, features, runtime[request.Engine]); err != nil {
			return core.Task{}, err
		}
		if request.Engine == core.EngineShadowsocksRust {
			rows, policyErr := tx.Query(ctx, `SELECT agent_id,engine,tag,kind,port,config_version,block_mainland_destination,block_mainland_source
				FROM mainland_access_policies WHERE config_id=$1 AND config_version=$2 ORDER BY port,tag`, request.ConfigID, task.ConfigVersion)
			if policyErr != nil {
				return core.Task{}, policyErr
			}
			for rows.Next() {
				var policy core.MainlandAccessPolicy
				if policyErr = rows.Scan(&policy.AgentID, &policy.Engine, &policy.Tag, &policy.Kind, &policy.Port, &policy.ConfigVersion,
					&policy.BlockMainlandDestination, &policy.BlockMainlandSource); policyErr != nil {
					rows.Close()
					return core.Task{}, policyErr
				}
				task.MainlandAccessPolicies = append(task.MainlandAccessPolicies, policy)
			}
			policyErr = rows.Err()
			rows.Close()
			if policyErr != nil {
				return core.Task{}, policyErr
			}
		}
	} else {
		task.ConfigID = ""
	}
	existing, existingErr := scanTask(tx.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at,tcp_settings,install_if_missing
		FROM tasks
		WHERE agent_id=$1 AND (action=$2 OR ($2 IN ('enable-bbr','disable-bbr','configure-tcp') AND action IN ('enable-bbr','disable-bbr','configure-tcp'))) AND engine=$3
		  AND COALESCE(config_id,'')=$4 AND COALESCE(config_version,0)=$5 AND COALESCE(core_version,'')=$6
		  AND (CASE WHEN $2='install' AND $3='mihomo' AND $6='development' AND COALESCE($7,'') IN ('','official')
		            THEN 'official' ELSE COALESCE($7,'') END)
		    = (CASE WHEN action='install' AND engine='mihomo' AND core_version='development' AND COALESCE(core_source,'') IN ('','official')
		            THEN 'official' ELSE COALESCE(core_source,'') END)
		  AND owner_id=$8 AND install_if_missing=$9 AND status IN ('pending','running')
		ORDER BY created_at DESC LIMIT 1`,
		task.AgentID, task.Action, task.Engine, task.ConfigID, task.ConfigVersion, task.CoreVersion, task.CoreSource, scope.OwnerID, task.InstallIfMissing), false)
	if existingErr == nil {
		if task.Action.SystemBBR() && (existing.Action != task.Action || !maps.Equal(existing.TCPSettings, task.TCPSettings)) {
			return core.Task{}, fmt.Errorf("%w: another system TCP task is pending or running", ErrConflict)
		}
		existing.Reused = true
		return existing, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return core.Task{}, existingErr
	}
	var err error
	task.ID, err = core.NewID("tsk")
	if err != nil {
		return core.Task{}, err
	}
	storedConfigContent, err := s.encryptContent(task.ConfigContent)
	if err != nil {
		return core.Task{}, err
	}
	mainlandPoliciesJSON, err := json.Marshal(task.MainlandAccessPolicies)
	if err != nil {
		return core.Task{}, err
	}
	_, err = tx.Exec(ctx, `
			INSERT INTO tasks (id,agent_id,action,engine,config_id,config_version,config_content,mainland_access_policies,core_version,core_source,status,attempt,created_at,tcp_settings,owner_id,shared_traffic_id,cnip_source,install_if_missing)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,0),NULLIF($7,''),$8,NULLIF($9,''),NULLIF($10,''),$11,0,$12,$13,$14,$15,$16,$17)`,
		task.ID, task.AgentID, task.Action, task.Engine, task.ConfigID, task.ConfigVersion, storedConfigContent, mainlandPoliciesJSON, task.CoreVersion, task.CoreSource, task.Status, task.CreatedAt, task.TCPSettings, scope.OwnerID, task.SharedTrafficID, task.CNIPSource, task.InstallIfMissing)
	if err != nil {
		return core.Task{}, mapError(err)
	}
	return task, nil
}

// TaskReady returns a coalescing signal for newly created tasks assigned to an agent.
func (s *Store) TaskReady(agentID string) <-chan struct{} {
	return s.taskReadyChannel(agentID)
}

func (s *Store) taskReadyChannel(agentID string) chan struct{} {
	s.taskWakeMu.Lock()
	defer s.taskWakeMu.Unlock()
	if s.taskWakes == nil {
		s.taskWakes = make(map[string]chan struct{})
	}
	wake := s.taskWakes[agentID]
	if wake == nil {
		wake = make(chan struct{}, 1)
		s.taskWakes[agentID] = wake
	}
	return wake
}

func (s *Store) signalTaskReady(agentID string) {
	wake := s.taskReadyChannel(agentID)
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (s *Store) ListTasks(ctx context.Context, agentID string, limit int) ([]core.Task, error) {
	return s.ListTasksFiltered(ctx, agentID, "", "", limit)
}

func (s *Store) ListTasksFiltered(ctx context.Context, agentID string, status core.TaskStatus, action core.Action, limit int) ([]core.Task, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	// Keep optional filters out of the SQL entirely when unused. An OR against
	// a parameter can hide selective indexes once pgx reuses a generic plan.
	where := "true"
	args := make([]any, 0, 4)
	for _, filter := range []struct{ column, value string }{
		{"agent_id", agentID}, {"status", string(status)}, {"action", string(action)},
	} {
		if filter.value != "" {
			args = append(args, filter.value)
			where += fmt.Sprintf(" AND %s=$%d", filter.column, len(args))
		}
	}
	where += ownerClause(ctx, "owner_id", &args)
	where += agentEngineAccessClause(ctx, "tasks.agent_id", "tasks.engine", &args)
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at,tcp_settings,install_if_missing
		FROM tasks
		WHERE `+where+fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]core.Task, 0)
	for rows.Next() {
		task, err := scanTask(rows, false)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *Store) GetTask(ctx context.Context, id string) (core.Task, error) {
	return s.getTask(ctx, id, false)
}

// GetTaskState omits the potentially large execution log from frequent
// status polls. The full task endpoint still returns that log on demand.
func (s *Store) GetTaskState(ctx context.Context, id string) (core.Task, error) {
	return s.getTask(ctx, id, true)
}

func (s *Store) getTask(ctx context.Context, id string, stateOnly bool) (core.Task, error) {
	output := "COALESCE(output,'')"
	if stateOnly {
		output = "''"
	}
	args := []any{id}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	ownerWhere += agentEngineAccessClause(ctx, "tasks.agent_id", "tasks.engine", &args)
	row := s.pool.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,
		       `+output+`,COALESCE(error,''),created_at,started_at,finished_at,tcp_settings,install_if_missing
		FROM tasks WHERE id=$1`+ownerWhere, args...)
	task, err := scanTask(row, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Task{}, ErrNotFound
	}
	return task, err
}

func (s *Store) CancelTask(ctx context.Context, id string) error {
	args := []any{id}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	ownerWhere += agentEngineAccessClause(ctx, "tasks.agent_id", "tasks.engine", &args)
	command, err := s.pool.Exec(ctx, `
		UPDATE tasks SET status='canceled',error='canceled by administrator',finished_at=now(),config_content=NULL,lease_id=NULL
		WHERE id=$1 AND status='pending'`+ownerWhere, args...)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1`+ownerWhere+`)`, args...).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return fmt.Errorf("%w: only pending tasks can be canceled", ErrConflict)
}

func (s *Store) RetryTask(ctx context.Context, id string) (core.Task, error) {
	previous, err := s.GetTask(ctx, id)
	if err != nil {
		return core.Task{}, err
	}
	if previous.Status != core.TaskFailed && previous.Status != core.TaskCanceled {
		return core.Task{}, fmt.Errorf("%w: only failed or canceled tasks can be retried", ErrConflict)
	}
	var transition bool
	if err := s.pool.QueryRow(ctx, `SELECT capability_transition FROM tasks WHERE id=$1`, id).Scan(&transition); err != nil {
		return core.Task{}, err
	}
	if transition {
		change, err := s.ChangeAgentEngineCapability(ctx, previous.AgentID, previous.Engine, previous.Action == core.ActionStart)
		if err != nil {
			return core.Task{}, err
		}
		if change.TaskID == "" {
			return core.Task{}, fmt.Errorf("%w: 节点能力已更新，无需重试启停任务", ErrConflict)
		}
		return s.GetTask(ctx, change.TaskID)
	}
	expectedVersion := 0
	if previous.InstallIfMissing {
		// Never silently deploy a newer draft when retrying a partially
		// completed install + configuration operation.
		expectedVersion = previous.ConfigVersion
	}
	return s.CreateTask(ctx, core.TaskRequest{
		InstallIfMissing: previous.InstallIfMissing, ExpectedConfigVersion: expectedVersion,
		TCPSettings: previous.TCPSettings,
		AgentID:     previous.AgentID, Action: previous.Action, Engine: previous.Engine,
		ConfigID: previous.ConfigID, CoreVersion: previous.CoreVersion, CoreSource: previous.CoreSource,
	})
}

// RunningTask returns the task lease currently owned by an agent. A reconnecting
// Agent can resume result delivery without waiting for the stale-lease janitor.
// If the Agent no longer advertises the protocol required by the running task
// (for example a Mihomo development mirror install after the Agent was
// downgraded), the task is failed atomically instead of being delivered to an
// Agent that would silently fall back to the official repository.
func (s *Store) RunningTask(ctx context.Context, agentID string) (*core.Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var featuresJSON []byte
	if err := tx.QueryRow(ctx, `SELECT features FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&featuresJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var features []string
	if err := json.Unmarshal(featuresJSON, &features); err != nil {
		return nil, err
	}
	if err := cancelUnauthorizedAgentTasksTx(ctx, tx, agentID, features); err != nil {
		return nil, err
	}
	row := tx.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),
		       COALESCE(config_content,''),COALESCE(mainland_access_policies,'[]'::jsonb),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,COALESCE(lease_id,''),
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at,tcp_settings,shared_traffic_id,cnip_source,install_if_missing
		FROM tasks WHERE agent_id=$1 AND status='running'
		ORDER BY started_at DESC LIMIT 1`, agentID)
	task, err := scanTask(row, true)
	if errors.Is(err, pgx.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, commitErr
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var unauthorized bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t WHERE t.id=$1
		AND ((`+unauthorizedTaskPrincipalSQL+`) OR (`+unauthorizedHostConfigTaskSQL+`)))`,
		task.ID).Scan(&unauthorized); err != nil {
		return nil, err
	}
	if unauthorized {
		if _, err := tx.Exec(ctx, `UPDATE tasks SET status='failed',error='user authorization changed; execution before disconnect is unknown',
			finished_at=now(),config_content=NULL,lease_id=NULL WHERE id=$1 AND status='running'`, task.ID); err != nil {
			return nil, err
		}
		return nil, tx.Commit(ctx)
	}
	if task.CNIPSource != nil && !containsFeature(features, core.AgentFeatureCNIPSource) {
		return nil, fmt.Errorf("%w: Agent no longer supports CN IP sources", ErrConflict)
	}
	if task.SharedTrafficID != "" {
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT $2::boolean AND EXISTS(SELECT 1 FROM agent_shares s JOIN panel_users u ON u.id=s.user_id
			WHERE s.id=$1 AND s.agent_id=$3 AND $4=ANY(s.engines) AND NOT u.disabled
				AND s.enabled AND s.status='accepted' AND (s.limit_bytes=0 OR s.used_bytes<s.limit_bytes))`,
			task.SharedTrafficID, supportsSharedEngines(features), agentID, task.Engine).Scan(&allowed); err != nil {
			return nil, err
		}
		if !allowed {
			if _, err := tx.Exec(ctx, `UPDATE tasks SET status='failed',error='shared Agent authorization changed; execution before disconnect is unknown',
				finished_at=now(),config_content=NULL,lease_id=NULL WHERE id=$1 AND status='running'`, task.ID); err != nil {
				return nil, err
			}
			return nil, tx.Commit(ctx)
		}
	}
	if (isMihomoMirrorTask(task) && !containsFeature(features, core.AgentFeatureMihomoDevelopmentSource)) ||
		(task.Action.SystemBBR() && !containsFeature(features, core.AgentFeatureSystemBBR)) ||
		(task.InstallIfMissing && !containsFeature(features, core.AgentFeaturePresetAutoInstall)) {
		message := "Agent no longer advertises mihomo-development-source-v1; the mirror development task cannot be safely resumed and it is unknown whether the previous Agent executed it before the connection was lost"
		if task.Action.SystemBBR() {
			message = "Agent no longer advertises system-bbr-v1; TCP tuning cannot safely resume and previous execution before disconnect is unknown"
		}
		if task.InstallIfMissing {
			message = "Agent no longer advertises preset-auto-install-v1; automatic installation cannot safely resume and previous execution before disconnect is unknown"
		}
		if _, updateErr := tx.Exec(ctx, `
			UPDATE tasks SET status='failed', error=$2, finished_at=now(), config_content=NULL, lease_id=NULL
			WHERE id=$1 AND status='running'`, task.ID,
			message); updateErr != nil {
			return nil, updateErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, commitErr
		}
		return nil, nil
	}
	if err := s.openExecutionConfig(&task); err != nil {
		if _, updateErr := tx.Exec(ctx, `UPDATE tasks SET status='failed',error=$2,finished_at=now(),
			config_content=NULL,lease_id=NULL WHERE id=$1`, task.ID, truncate(err.Error(), 8<<10)); updateErr != nil {
			return nil, updateErr
		}
		return nil, tx.Commit(ctx)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *Store) ClaimTask(ctx context.Context, agentID string) (*core.Task, error) {
	// Most fallback polls find no work. Avoid opening a transaction and locking
	// the Agent (blocking live metrics) for these idle polls. This is only a
	// hint: the transactional checks below remain authoritative when work exists.
	var pending bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM tasks WHERE agent_id=$1 AND status='pending'
	)`, agentID).Scan(&pending); err != nil {
		return nil, err
	}
	if !pending {
		return nil, nil
	}
	leaseID, err := core.NewToken()
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var featuresJSON []byte
	if err := tx.QueryRow(ctx, `SELECT features FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&featuresJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var features []string
	if err := json.Unmarshal(featuresJSON, &features); err != nil {
		return nil, err
	}
	if err := cancelUnauthorizedAgentTasksTx(ctx, tx, agentID, features); err != nil {
		return nil, err
	}
	mirrorSupported := containsFeature(features, core.AgentFeatureMihomoDevelopmentSource)
	row := tx.QueryRow(ctx, `
		WITH next_task AS (
			SELECT t.id FROM tasks t
			WHERE t.agent_id=$1 AND t.status='pending'
			  AND NOT EXISTS (SELECT 1 FROM tasks running WHERE running.agent_id=$1 AND running.status='running')
			  AND ($3::boolean OR NOT (t.action='install' AND t.engine='mihomo' AND t.core_version='development' AND COALESCE(t.core_source,'')='mirror'))
			  AND ($4::boolean OR t.action NOT IN ('enable-bbr','disable-bbr','configure-tcp'))
              AND ($5::boolean OR t.cnip_source IS NULL)
			  AND ($6::boolean OR NOT t.install_if_missing)
			ORDER BY t.created_at ASC FOR UPDATE OF t SKIP LOCKED LIMIT 1
		)
		UPDATE tasks t SET status='running',started_at=now(),attempt=attempt+1,lease_id=$2
		FROM next_task n WHERE t.id=n.id
		RETURNING t.id,t.agent_id,t.action,t.engine,COALESCE(t.config_id,''),COALESCE(t.config_version,0),
		          COALESCE(t.config_content,''),COALESCE(t.mainland_access_policies,'[]'::jsonb),COALESCE(t.core_version,''),COALESCE(t.core_source,''),t.status,t.attempt,COALESCE(t.lease_id,''),COALESCE(t.output,''),COALESCE(t.error,''),
		          t.created_at,t.started_at,t.finished_at,t.tcp_settings,t.shared_traffic_id,t.cnip_source,t.install_if_missing`, agentID, leaseID, mirrorSupported, containsFeature(features, core.AgentFeatureSystemBBR), containsFeature(features, core.AgentFeatureCNIPSource), containsFeature(features, core.AgentFeaturePresetAutoInstall))
	task, err := scanTask(row, true)
	if err == nil {
		if configErr := s.openExecutionConfig(&task); configErr != nil {
			if _, updateErr := tx.Exec(ctx, `UPDATE tasks SET status='failed',error=$2,finished_at=now(),
				config_content=NULL,lease_id=NULL WHERE id=$1`, task.ID, truncate(configErr.Error(), 8<<10)); updateErr != nil {
				return nil, updateErr
			}
			return nil, tx.Commit(ctx)
		}
		if markErr := markEngineExecutionTx(ctx, tx, task); markErr != nil {
			return nil, markErr
		}
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return nil, commitErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// isMihomoMirrorTask reports whether a task is the explicit third-party
// vernesong/mihomo mirror install that requires mihomo-development-source-v1.
func isMihomoMirrorTask(task core.Task) bool {
	return task.Action == core.ActionInstall &&
		task.Engine == core.EngineMihomo &&
		task.CoreVersion == core.CoreVersionDevelopment &&
		task.CoreSource == string(core.CoreSourceMirror)
}

func (s *Store) CompleteTask(ctx context.Context, agentID, taskID string, result core.TaskResultRequest) error {
	if len(result.LeaseID) < 32 {
		return fmt.Errorf("%w: invalid task lease", ErrConflict)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Match creation/claim lock order: node first, then task. A successful
	// transition updates eligibility atomically with the task acknowledgement.
	var selected, supported []core.Engine
	if err := tx.QueryRow(ctx, `SELECT capabilities,COALESCE(supported_capabilities,capabilities) FROM agents WHERE id=$1 FOR UPDATE`, agentID).Scan(&selected, &supported); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var action core.Action
	var engine core.Engine
	var transition bool
	if err := tx.QueryRow(ctx, `
		SELECT action,engine,capability_transition FROM tasks
		WHERE id=$1 AND agent_id=$2 AND lease_id=$3 AND status='running'
		FOR UPDATE`, taskID, agentID, result.LeaseID).Scan(&action, &engine, &transition); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1 AND agent_id=$2)`, taskID, agentID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return fmt.Errorf("%w: task is not running", ErrConflict)
	}
	var unauthorized bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t WHERE t.id=$1
		AND ((`+unauthorizedTaskPrincipalSQL+`) OR (`+unauthorizedHostConfigTaskSQL+`)))`, taskID).Scan(&unauthorized); err != nil {
		return err
	}
	if unauthorized {
		// Never retain an unauthorized read, including partial content in an
		// error. A later owner takeover must not make this result readable.
		if _, err := tx.Exec(ctx, `UPDATE tasks SET status='failed',output=NULL,
			error='task authorization changed; previous execution is unknown',
			finished_at=now(),config_content=NULL,lease_id=NULL WHERE id=$1`, taskID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	status := core.TaskFailed
	if result.Success {
		status = core.TaskSucceeded
	}
	// Re-enrollment can narrow declared support while an offline transition
	// is queued. Never restore a capability outside that current support set.
	if transition && result.Success && action == core.ActionStart && !containsEngine(supported, engine) {
		status = core.TaskFailed
		result.Error = "Agent no longer declares support for this engine; capability was not enabled"
	}
	if transition && status == core.TaskSucceeded {
		if err := setEngineCapability(ctx, tx, agentID, engine, action == core.ActionStart, selected); err != nil {
			return err
		}
	}
	storedContent := ""
	storedOutput := truncate(result.Output, 64<<10)
	storedError := truncate(result.Error, 8<<10)
	if (action == core.ActionReadConfig || action == core.ActionReadManagedConfig) && result.Success {
		content := result.Output
		if !utf8.ValidString(content) {
			status = core.TaskFailed
			storedOutput = ""
			storedError = "agent returned a current configuration that is not valid UTF-8"
		} else if len(content) > core.MaxConfigBytes {
			status = core.TaskFailed
			storedOutput = ""
			storedError = "agent returned a current configuration larger than the supported limit"
		} else if validationErr := core.ValidateConfig(engine, content); validationErr != nil {
			status = core.TaskFailed
			storedOutput = ""
			storedError = "agent returned an invalid current configuration: " + validationErr.Error()
		} else {
			storedContent = content
			storedOutput = "current configuration read and validated"
			storedError = ""
		}
	}
	storedContent, err = s.encryptContent(storedContent)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE tasks SET status=$4,output=$5,error=$6,finished_at=now(),config_content=NULLIF($7,''),lease_id=NULL
		WHERE id=$1 AND agent_id=$2 AND lease_id=$3 AND status='running'`,
		taskID, agentID, result.LeaseID, status, storedOutput, storedError, storedContent)
	if err != nil {
		return err
	}
	if (action == core.ActionReadConfig || action == core.ActionReadManagedConfig) && status == core.TaskSucceeded {
		if _, err := tx.Exec(ctx, `
			UPDATE tasks SET config_content=NULL
			WHERE agent_id=$1 AND engine=$2 AND action=$3 AND id<>$4 AND config_content IS NOT NULL`,
			agentID, engine, action, taskID); err != nil {
			return err
		}
	}
	if status == core.TaskSucceeded {
		if err := recordEngineOwnershipTx(ctx, tx, taskID, action, result.TrafficSettled); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ReadTaskConfigSnapshot(ctx context.Context, taskID, agentID string, engine core.Engine) (string, error) {
	if err := requireHostConfigRead(ctx, s.pool, agentID, engine); err != nil {
		return "", err
	}
	var content string
	args := []any{taskID, agentID, engine, core.ActionReadConfig, core.ActionReadManagedConfig}
	where := ownerClause(ctx, "owner_id", &args)
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(config_content,'') FROM tasks
		WHERE id=$1 AND agent_id=$2 AND engine=$3 AND action IN ($4,$5) AND status='succeeded'`+where,
		args...).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && content == "") {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.decryptContent(content)
}

func (s *Store) RecentReadTask(ctx context.Context, agentID string, engine core.Engine, maxAge time.Duration) (core.Task, error) {
	if err := requireHostConfigRead(ctx, s.pool, agentID, engine); err != nil {
		return core.Task{}, ErrNotFound
	}
	if maxAge <= 0 {
		return core.Task{}, ErrNotFound
	}
	args := []any{agentID, engine, core.ActionReadConfig, intervalString(maxAge)}
	where := ownerClause(ctx, "owner_id", &args)
	row := s.pool.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at,tcp_settings,install_if_missing
		FROM tasks
		WHERE agent_id=$1 AND engine=$2 AND action=$3 AND status='succeeded'
		  AND config_content IS NOT NULL AND finished_at > now()-$4::interval`+where+`
		ORDER BY finished_at DESC LIMIT 1`, args...)
	task, err := scanTask(row, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Task{}, ErrNotFound
	}
	return task, err
}

func (s *Store) RequeueStaleTasks(ctx context.Context, age, installAge time.Duration, maxAttempts int) error {
	if installAge < age {
		installAge = age
	}
	args := []any{intervalString(age), intervalString(installAge), maxAttempts}
	where := workspaceOwnerClause(ctx, "owner_id", &args)
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks SET
			status=CASE WHEN attempt >= $3 THEN 'failed' ELSE 'pending' END,
			error=CASE WHEN attempt >= $3 THEN 'agent did not report a result before the execution lease expired' ELSE error END,
			finished_at=CASE WHEN attempt >= $3 THEN now() ELSE NULL END,
			started_at=CASE WHEN attempt >= $3 THEN started_at ELSE NULL END,
			config_content=CASE WHEN attempt >= $3 THEN NULL ELSE config_content END,
			lease_id=NULL
		WHERE status='running' AND started_at < now() - CASE WHEN action='install' OR install_if_missing THEN $2::interval ELSE $1::interval END`+where,
		args...)
	return err
}

func (s *Store) Overview(ctx context.Context) (core.Overview, error) {
	var result core.Overview
	args := []any{}
	configWhere := ownerClause(ctx, "owner_id", &args)
	configWhere += configAgentAccessClause(ctx, "configs.agent_id", "configs.engine", &args)
	taskWhere := ownerClause(ctx, "owner_id", &args)
	taskWhere += agentEngineAccessClause(ctx, "tasks.agent_id", "tasks.engine", &args)
	agentWhere := agentAccessClause(ctx, "agents.id", &args)
	err := s.pool.QueryRow(ctx, `
		SELECT agents.total,agents.online,configs.archived,configs.node,
		       tasks.queued+tasks.running,tasks.queued,tasks.running,tasks.failed
		FROM (
			SELECT count(*) AS total,
			       count(*) FILTER (WHERE last_seen > now() - make_interval(secs => `+agentOfflineThresholdSQL+`)) AS online
			FROM agents WHERE revoked_at IS NULL`+agentWhere+`
		) agents CROSS JOIN (
			SELECT count(*) FILTER (WHERE agent_id IS NULL) AS archived,
			       count(*) FILTER (WHERE agent_id IS NOT NULL) AS node
			FROM configs WHERE deleted_at IS NULL`+configWhere+`
		) configs CROSS JOIN (
			SELECT count(*) FILTER (WHERE status='pending') AS queued,
			       count(*) FILTER (WHERE status='running') AS running,
			       count(*) FILTER (WHERE status='failed') AS failed
			FROM tasks WHERE status IN ('pending','running','failed')`+taskWhere+`
		) tasks`, args...).Scan(
		&result.Agents, &result.AgentsOnline, &result.Configs, &result.NodeConfigs,
		&result.TasksPending, &result.TasksQueued, &result.TasksRunning, &result.TasksFailed)
	return result, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner, includeContent bool) (core.Task, error) {
	var task core.Task
	var err error
	var tcpSettingsJSON []byte
	if includeContent {
		var mainlandPoliciesJSON []byte
		err = row.Scan(&task.ID, &task.AgentID, &task.Action, &task.Engine, &task.ConfigID, &task.ConfigVersion,
			&task.ConfigContent, &mainlandPoliciesJSON, &task.CoreVersion, &task.CoreSource, &task.Status, &task.Attempt, &task.LeaseID, &task.Output, &task.Error,
			&task.CreatedAt, &task.StartedAt, &task.FinishedAt, &tcpSettingsJSON, &task.SharedTrafficID, &task.CNIPSource, &task.InstallIfMissing)
		if err == nil && len(mainlandPoliciesJSON) > 0 {
			err = json.Unmarshal(mainlandPoliciesJSON, &task.MainlandAccessPolicies)
		}
	} else {
		err = row.Scan(&task.ID, &task.AgentID, &task.Action, &task.Engine, &task.ConfigID, &task.ConfigVersion,
			&task.CoreVersion, &task.CoreSource, &task.Status, &task.Attempt, &task.Output, &task.Error,
			&task.CreatedAt, &task.StartedAt, &task.FinishedAt, &tcpSettingsJSON, &task.InstallIfMissing)
	}
	if err == nil && len(tcpSettingsJSON) > 0 {
		err = json.Unmarshal(tcpSettingsJSON, &task.TCPSettings)
	}
	return task, err
}

func cloneLabels(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func validateConfigMetadata(rawName, rawDescription string) (string, string, error) {
	name := strings.TrimSpace(rawName)
	description := strings.TrimSpace(rawDescription)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return "", "", fmt.Errorf("%w: configuration name is required and must not exceed 100 characters", ErrInvalid)
	}
	if utf8.RuneCountInString(description) > 300 {
		return "", "", fmt.Errorf("%w: configuration description exceeds 300 characters", ErrInvalid)
	}
	return name, description, nil
}

func containsEngine(values []core.Engine, expected core.Engine) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsFeature(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: duplicate value", ErrConflict)
	}
	return err
}

func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23503"
}

func intervalString(duration time.Duration) string {
	seconds := int64(duration.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d seconds", seconds)
}

func truncate(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "�") + "\n… output truncated by QControlHub"
}
