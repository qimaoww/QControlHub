package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

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
