package store

// Increment this whenever schemaSQL changes. migrate skips schemaSQL when the
// database already reports this version, so leaving the version unchanged can
// strand upgraded installations without newly added columns or constraints.
const currentSchemaVersion = 66

const schemaSQL = `
CREATE TABLE IF NOT EXISTS agents (
    id text PRIMARY KEY,
    name varchar(100) NOT NULL,
    version varchar(100) NOT NULL DEFAULT '',
    os varchar(50) NOT NULL,
    arch varchar(50) NOT NULL,
    capabilities jsonb NOT NULL,
	features jsonb NOT NULL DEFAULT '[]'::jsonb,
    labels jsonb NOT NULL DEFAULT '{}'::jsonb,
	    runtime jsonb NOT NULL DEFAULT '{}'::jsonb,
    public_key bytea NOT NULL CHECK (octet_length(public_key) = 32),
    last_seen timestamptz NOT NULL,
    enrolled_at timestamptz NOT NULL,
    revoked_at timestamptz,
    admin_hidden boolean NOT NULL DEFAULT false
	);

	-- Agent-reported metrics live in agent_live_state. Keeping that snapshot in
	-- a narrow side table, and keeping the agents row free of it, is what lets
	-- both the per-second metrics push and the per-heartbeat liveness stamp
	-- update on-page: the previous layout rewrote a row of roughly 3 kB plus
	-- every index pointing at it, once per second per node, and never once
	-- qualified as a heap-only update.
	--
	-- observed_public_ip is assigned by the control plane from the
	-- authenticated WSS peer rather than reported by the Agent, so it is
	-- updated on its own schedule and cannot live inside the Agent's snapshot:
	-- a later push would have overwritten it.
	ALTER TABLE agents ADD COLUMN IF NOT EXISTS observed_public_ip text NOT NULL DEFAULT '';
	ALTER TABLE agents ADD COLUMN IF NOT EXISTS owner_id text NOT NULL DEFAULT '';
	ALTER TABLE agents ADD COLUMN IF NOT EXISTS sharing_revision bigint NOT NULL DEFAULT 1;
	ALTER TABLE agents ADD COLUMN IF NOT EXISTS admin_hidden boolean NOT NULL DEFAULT false;
	CREATE INDEX IF NOT EXISTS agents_owner_idx ON agents(owner_id);
	COMMENT ON COLUMN agents.observed_public_ip IS 'Public address the control plane observed for this Agent''s authenticated WSS session; written from the socket, never reported by the Agent.';
	ALTER TABLE agents SET (fillfactor = 70);

-- Persist deletion cleanup independently of the HTTP request and process lifetime.
CREATE TABLE IF NOT EXISTS agent_deletion_jobs (
    agent_id text PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    retry_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_deletion_jobs_retry_idx ON agent_deletion_jobs(retry_at,agent_id);

CREATE TABLE IF NOT EXISTS agent_live_state (
    agent_id text PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    metrics jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
) WITH (fillfactor = 70);

CREATE TABLE IF NOT EXISTS core_log_batches (
    id text PRIMARY KEY,
    agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    received_at timestamptz NOT NULL
);

-- Kernel logs are partitioned by receive day. The retention window is
-- time-bounded, so dropping a partition replaces a bulk DELETE that has to
-- walk every index, and each partition's indexes stay small enough to remain
-- cached on the small instances this panel targets. The sequence is created
-- separately so it survives a table rebuild.
CREATE SEQUENCE IF NOT EXISTS core_logs_id_seq;

CREATE TABLE IF NOT EXISTS core_logs (
    id bigint NOT NULL DEFAULT nextval('core_logs_id_seq'),
    batch_id text NOT NULL REFERENCES core_log_batches(id),
    entry_index smallint NOT NULL CHECK (entry_index >= 0),
    agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
    level varchar(10) NOT NULL CHECK (level IN ('debug','info','warning','error','critical')),
    message text NOT NULL CHECK (octet_length(message) BETWEEN 1 AND 4096),
    logged_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    -- A partitioned table's unique constraints must include the partition key.
    PRIMARY KEY (id,received_at),
    UNIQUE (batch_id,entry_index,received_at)
) PARTITION BY RANGE (received_at);

-- Core log reads have exactly one shape: a bounded newest-first window per
-- (selected engines × optional agent). The covering index below serves every
-- variant of it with index-only reads, so the older single-column recency
-- indexes only added write amplification: every inserted row had to maintain
-- them while no query used them. See docs/performance.md.
CREATE INDEX IF NOT EXISTS core_logs_agent_engine_covering_idx ON core_logs(agent_id,engine,id DESC)
    INCLUDE (level,message,logged_at,received_at);
CREATE INDEX IF NOT EXISTS core_logs_received_idx ON core_logs(received_at);
CREATE INDEX IF NOT EXISTS core_log_batches_received_idx ON core_log_batches(received_at);

CREATE TABLE IF NOT EXISTS configs (
    id text PRIMARY KEY,
    agent_id text REFERENCES agents(id),
    name varchar(100) NOT NULL,
    description varchar(300) NOT NULL DEFAULT '',
    engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
    content text NOT NULL CHECK (octet_length(content) <= 4194304),
    version integer NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz
);
ALTER TABLE configs DROP CONSTRAINT IF EXISTS configs_content_check;
ALTER TABLE configs ADD CONSTRAINT configs_content_check CHECK (octet_length(content) <= 4194304);

	ALTER TABLE configs ADD COLUMN IF NOT EXISTS agent_id text REFERENCES agents(id);
	ALTER TABLE configs ADD COLUMN IF NOT EXISTS owner_id text NOT NULL DEFAULT '';

	CREATE TABLE IF NOT EXISTS config_revisions (
	    config_id text NOT NULL REFERENCES configs(id),
	    version integer NOT NULL CHECK (version > 0),
	    agent_id text REFERENCES agents(id),
	    name varchar(100) NOT NULL,
	    description varchar(300) NOT NULL DEFAULT '',
	    engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
	    content text NOT NULL CHECK (octet_length(content) <= 4194304),
	    created_at timestamptz NOT NULL,
	    PRIMARY KEY (config_id,version)
	);
	ALTER TABLE config_revisions DROP CONSTRAINT IF EXISTS config_revisions_content_check;
	ALTER TABLE config_revisions ADD CONSTRAINT config_revisions_content_check CHECK (octet_length(content) <= 4194304);

	CREATE TABLE IF NOT EXISTS config_client_metadata (
	    config_id text NOT NULL,
	    config_version integer NOT NULL CHECK (config_version > 0),
	    profile_tag varchar(64) NOT NULL,
	    content text NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 65536),
	    created_at timestamptz NOT NULL DEFAULT now(),
	    PRIMARY KEY (config_id,config_version,profile_tag),
	    FOREIGN KEY (config_id,config_version) REFERENCES config_revisions(config_id,version) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS config_client_metadata_config_idx
	    ON config_client_metadata(config_id,config_version);
	CREATE TABLE IF NOT EXISTS config_client_preferences (
	    config_id text NOT NULL REFERENCES configs(id) ON DELETE CASCADE,
	    label text NOT NULL,
	    value text NOT NULL,
	    PRIMARY KEY(config_id,label)
	);

	INSERT INTO config_revisions (config_id,version,agent_id,name,description,engine,content,created_at)
	SELECT id,version,agent_id,name,description,engine,content,updated_at FROM configs
	WHERE deleted_at IS NULL
	ON CONFLICT (config_id,version) DO NOTHING;

-- Shadowsocks Rust has no routing section. Keep its per-inbound mainland
-- policy in the control plane and deliver it to the Agent for a native ACL
-- plus nftables source enforcement, rather than polluting ssserver JSON.
CREATE TABLE IF NOT EXISTS mainland_access_policies (
    agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    engine varchar(20) NOT NULL CHECK (engine = 'ss-rust'),
    tag varchar(64) NOT NULL,
    kind varchar(64) NOT NULL DEFAULT '',
	    port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
	    config_version integer NOT NULL CHECK (config_version > 0),
    block_mainland_destination boolean NOT NULL DEFAULT false,
    block_mainland_source boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id,engine,tag,port)
);
CREATE INDEX IF NOT EXISTS mainland_access_policies_agent_idx
    ON mainland_access_policies(agent_id);
	ALTER TABLE mainland_access_policies ADD COLUMN IF NOT EXISTS config_version integer;
	UPDATE mainland_access_policies p SET config_version=c.version FROM configs c
	WHERE p.config_version IS NULL AND c.agent_id=p.agent_id AND c.engine=p.engine AND c.deleted_at IS NULL;
	DELETE FROM mainland_access_policies WHERE config_version IS NULL;
	ALTER TABLE mainland_access_policies ALTER COLUMN config_version SET NOT NULL;
	ALTER TABLE mainland_access_policies ADD COLUMN IF NOT EXISTS config_id text REFERENCES configs(id);
	UPDATE mainland_access_policies p SET config_id=c.id FROM configs c
	WHERE p.config_id IS NULL AND c.agent_id=p.agent_id AND c.engine=p.engine AND c.deleted_at IS NULL;
	DELETE FROM mainland_access_policies WHERE config_id IS NULL;
	ALTER TABLE mainland_access_policies ALTER COLUMN config_id SET NOT NULL;
	ALTER TABLE mainland_access_policies DROP CONSTRAINT IF EXISTS mainland_access_policies_pkey;
	ALTER TABLE mainland_access_policies ADD CONSTRAINT mainland_access_policies_pkey PRIMARY KEY (config_id,tag,port);

CREATE TABLE IF NOT EXISTS tasks (
    id text PRIMARY KEY,
    agent_id text NOT NULL REFERENCES agents(id),
    action varchar(20) NOT NULL CHECK (action IN ('validate','deploy','import-existing','read-config','read-managed-config','start','stop','restart','status','install','upgrade-agent','enable-bbr','disable-bbr','configure-tcp','ip-quality')),
	    engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust') OR (action IN ('upgrade-agent','enable-bbr','disable-bbr','configure-tcp','ip-quality') AND engine='')),
    config_id text REFERENCES configs(id),
    config_version integer,
	    config_content text,
	    mainland_access_policies jsonb NOT NULL DEFAULT '[]'::jsonb,
	    core_version varchar(64),
	    core_source varchar(32),
	    status varchar(20) NOT NULL CHECK (status IN ('pending','running','succeeded','failed','canceled')),
	    attempt integer NOT NULL DEFAULT 0,
    output text,
    error text,
    created_at timestamptz NOT NULL,
    started_at timestamptz,
    finished_at timestamptz
);

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS lease_id text;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS install_if_missing boolean NOT NULL DEFAULT false;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_install_if_missing_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_install_if_missing_check CHECK (
	NOT install_if_missing OR (action IN ('validate','deploy') AND config_id IS NOT NULL AND config_version IS NOT NULL AND config_version > 0)
);
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS capability_transition boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS tasks_capability_transition_idx ON tasks(agent_id,engine,created_at DESC,id DESC) WHERE capability_transition;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS tcp_settings jsonb NOT NULL DEFAULT '{}'::jsonb;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS core_version varchar(64);
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS core_source varchar(32);
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS mainland_access_policies jsonb NOT NULL DEFAULT '[]'::jsonb;
	DROP INDEX IF EXISTS tasks_latest_deployment_idx;
	ALTER TABLE tasks DROP COLUMN IF EXISTS simulated;
	ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_action_check;
	ALTER TABLE tasks ADD CONSTRAINT tasks_action_check CHECK (action IN ('validate','deploy','import-existing','read-config','read-managed-config','start','stop','restart','status','install','upgrade-agent','enable-bbr','disable-bbr','configure-tcp','ip-quality'));
	ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;
	ALTER TABLE tasks ADD CONSTRAINT tasks_status_check CHECK (status IN ('pending','running','succeeded','failed','canceled'));
	ALTER TABLE configs DROP CONSTRAINT IF EXISTS configs_engine_check;
	ALTER TABLE configs ADD CONSTRAINT configs_engine_check CHECK (engine IN ('mihomo','xray','sing-box','ss-rust'));
	ALTER TABLE config_revisions DROP CONSTRAINT IF EXISTS config_revisions_engine_check;
	ALTER TABLE config_revisions ADD CONSTRAINT config_revisions_engine_check CHECK (engine IN ('mihomo','xray','sing-box','ss-rust'));
	ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_engine_check;
	ALTER TABLE tasks ADD CONSTRAINT tasks_engine_check CHECK (engine IN ('mihomo','xray','sing-box','ss-rust') OR (action IN ('upgrade-agent','enable-bbr','disable-bbr','configure-tcp','ip-quality') AND engine=''));

-- Reports inherit the task's durable ownership, lifecycle and retention.
-- Large provider payloads stay out of ordinary task/status polling.
CREATE TABLE IF NOT EXISTS ip_quality_reports (
    task_id text PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    result jsonb NOT NULL CHECK (jsonb_typeof(result)='object' AND octet_length(result::text)<=262144)
);
CREATE TABLE IF NOT EXISTS ip_quality_archives (
    task_id text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    family integer NOT NULL CHECK (family IN (4,6)),
    sha256 text NOT NULL CHECK (length(sha256)=64),
    rendered_at timestamptz NOT NULL,
    content bytea NOT NULL CHECK (octet_length(content)>0 AND octet_length(content)<=2097152),
    PRIMARY KEY(task_id,family)
);
-- v65: the panel renders the archived image from the stored JSON, so an archive
-- no longer records an upstream report link and its timestamp is a render time.
DO $ip_quality_render$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema=current_schema() AND table_name='ip_quality_archives' AND column_name='downloaded_at'
    ) THEN
        ALTER TABLE ip_quality_archives RENAME COLUMN downloaded_at TO rendered_at;
    END IF;
    ALTER TABLE ip_quality_archives ADD COLUMN IF NOT EXISTS rendered_at timestamptz NOT NULL DEFAULT now();
    ALTER TABLE ip_quality_archives DROP COLUMN IF EXISTS source_url;
END
$ip_quality_render$;
CREATE INDEX IF NOT EXISTS tasks_ip_quality_history_idx
    ON tasks(created_at DESC,agent_id,id DESC) WHERE action='ip-quality';
CREATE TABLE IF NOT EXISTS ip_quality_schedules (
    agent_id text PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    owner_id text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    next_run_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ip_quality_schedules_due_idx
    ON ip_quality_schedules(next_run_at) WHERE enabled;

CREATE TABLE IF NOT EXISTS enrollment_tokens (
    id text PRIMARY KEY,
    name varchar(100) NOT NULL,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
	    token_ciphertext text,
	    expires_at timestamptz,
	    max_uses integer NOT NULL CHECK (max_uses BETWEEN 0 AND 50),
	    used_count integer NOT NULL DEFAULT 0 CHECK (used_count >= 0),
	    reusable boolean NOT NULL DEFAULT false,
	    created_at timestamptz NOT NULL,
	    revoked_at timestamptz,
	    admin_hidden boolean NOT NULL DEFAULT false
);

ALTER TABLE enrollment_tokens ADD COLUMN IF NOT EXISTS reusable boolean NOT NULL DEFAULT false;
ALTER TABLE enrollment_tokens ADD COLUMN IF NOT EXISTS token_ciphertext text;
ALTER TABLE enrollment_tokens ALTER COLUMN expires_at DROP NOT NULL;
ALTER TABLE enrollment_tokens DROP CONSTRAINT IF EXISTS enrollment_tokens_max_uses_check;
ALTER TABLE enrollment_tokens ADD CONSTRAINT enrollment_tokens_max_uses_check CHECK (max_uses BETWEEN 0 AND 50);
ALTER TABLE enrollment_tokens ADD COLUMN IF NOT EXISTS agent_id text;
ALTER TABLE enrollment_tokens ADD COLUMN IF NOT EXISTS owner_id text NOT NULL DEFAULT '';
ALTER TABLE enrollment_tokens ADD COLUMN IF NOT EXISTS admin_hidden boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS enrollment_tokens_owner_idx ON enrollment_tokens(owner_id);
DROP INDEX IF EXISTS enrollment_tokens_reusable_name_unique_idx;

ALTER TABLE agents ADD COLUMN IF NOT EXISTS enrollment_id text;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS presence_notification_state varchar(10) NOT NULL DEFAULT 'unknown';
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_presence_notification_state_check;
ALTER TABLE agents ADD CONSTRAINT agents_presence_notification_state_check CHECK (presence_notification_state IN ('unknown','online','offline'));
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_enrollment_id_fkey;
ALTER TABLE agents ADD CONSTRAINT agents_enrollment_id_fkey FOREIGN KEY (enrollment_id) REFERENCES enrollment_tokens(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS agents_enrollment_id_unique_idx ON agents(enrollment_id) WHERE enrollment_id IS NOT NULL;

UPDATE enrollment_tokens AS token
SET agent_id=agent.id
FROM agents AS agent
WHERE agent.enrollment_id=token.id AND token.agent_id IS NULL;
ALTER TABLE enrollment_tokens DROP CONSTRAINT IF EXISTS enrollment_tokens_agent_id_fkey;
ALTER TABLE enrollment_tokens ADD CONSTRAINT enrollment_tokens_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS enrollment_tokens_agent_id_idx ON enrollment_tokens(agent_id,created_at DESC);
DROP INDEX IF EXISTS enrollment_tokens_reusable_unbound_name_unique_idx;
CREATE UNIQUE INDEX IF NOT EXISTS enrollment_tokens_owner_unbound_name_unique_idx ON enrollment_tokens(owner_id,lower(name)) WHERE reusable AND agent_id IS NULL;

CREATE TABLE IF NOT EXISTS agent_nonces (
    agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    nonce varchar(100) NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (agent_id, nonce)
);

CREATE TABLE IF NOT EXISTS panel_settings (
    id smallint PRIMARY KEY CHECK (id = 1),
    panel_name varchar(40) NOT NULL,
    panel_description varchar(120) NOT NULL DEFAULT '',
    task_page_size integer NOT NULL CHECK (task_page_size IN (50,100,500)),
    task_poll_interval_ms integer NOT NULL CHECK (task_poll_interval_ms IN (600,1000,2000,5000)),
	core_log_minimum_level varchar(10) NOT NULL DEFAULT 'debug' CHECK (core_log_minimum_level IN ('debug','info','warning','error','critical','off')),
    webhook_url varchar(500) NOT NULL DEFAULT '',
    komari_url varchar(500) NOT NULL DEFAULT '',
    komari_api_key varchar(500) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL
);
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS webhook_url varchar(500) NOT NULL DEFAULT '';
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS komari_url varchar(500) NOT NULL DEFAULT '';
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS default_agent_engines jsonb;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS supported_capabilities jsonb;
UPDATE agents SET supported_capabilities=capabilities WHERE supported_capabilities IS NULL;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS komari_api_key varchar(500) NOT NULL DEFAULT '';
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS core_log_minimum_level varchar(10) NOT NULL DEFAULT 'debug';
ALTER TABLE panel_settings DROP CONSTRAINT IF EXISTS panel_settings_core_log_minimum_level_check;
ALTER TABLE panel_settings ADD CONSTRAINT panel_settings_core_log_minimum_level_check CHECK (core_log_minimum_level IN ('debug','info','warning','error','critical','off'));
ALTER TABLE panel_settings DROP COLUMN IF EXISTS enrollment_ttl_minutes;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0);
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS time_zone varchar(32) NOT NULL DEFAULT 'browser';
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS time_display varchar(32) NOT NULL DEFAULT 'absolute-relative';
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS ui_font_scale integer NOT NULL DEFAULT 100;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS default_config_editor varchar(16) NOT NULL DEFAULT 'structured';
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS agent_heartbeat_interval_seconds integer NOT NULL DEFAULT 15;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS agent_metrics_interval_seconds integer NOT NULL DEFAULT 1;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS agent_offline_threshold_seconds integer NOT NULL DEFAULT 45;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS task_stale_timeout_seconds integer NOT NULL DEFAULT 120;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS install_task_stale_timeout_seconds integer NOT NULL DEFAULT 360;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS task_max_attempts integer NOT NULL DEFAULT 3;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS public_ip_probe_interval_seconds integer NOT NULL DEFAULT 300;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS core_log_retention_days integer NOT NULL DEFAULT 7;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS cnip_source jsonb;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS cnip_source jsonb;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS agent_core_log_max_mib integer NOT NULL DEFAULT 16;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS agent_core_log_rotate_count integer NOT NULL DEFAULT 1;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS metric_retention_days integer NOT NULL DEFAULT 7;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS audit_retention_days integer NOT NULL DEFAULT 90;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS task_retention_days integer NOT NULL DEFAULT 0;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS config_revision_retention integer NOT NULL DEFAULT 0;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS notify_task_failed boolean NOT NULL DEFAULT true;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS notify_agent_offline boolean NOT NULL DEFAULT true;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS notify_agent_online boolean NOT NULL DEFAULT true;
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS notify_traffic_quota boolean NOT NULL DEFAULT true;
ALTER TABLE panel_settings DROP CONSTRAINT IF EXISTS panel_settings_operational_values_check;
ALTER TABLE panel_settings ADD CONSTRAINT panel_settings_operational_values_check CHECK (
    time_zone IN ('browser','Asia/Shanghai','UTC') AND
    time_display IN ('absolute-relative','absolute') AND
    ui_font_scale IN (90,100,110) AND
    default_config_editor IN ('structured','source') AND
    task_page_size IN (50,100,500) AND task_poll_interval_ms IN (600,1000,2000,5000) AND
    agent_heartbeat_interval_seconds IN (10,15,30) AND agent_metrics_interval_seconds IN (1,5,15,30) AND
    agent_offline_threshold_seconds IN (45,60,90,180) AND agent_offline_threshold_seconds >= agent_heartbeat_interval_seconds*3 AND
    task_stale_timeout_seconds IN (60,120,300,600) AND install_task_stale_timeout_seconds IN (180,360,600,900) AND
    task_max_attempts IN (1,3,5) AND public_ip_probe_interval_seconds IN (300,900,3600) AND
    core_log_retention_days IN (1,3,7,14,30) AND agent_core_log_max_mib IN (1,2,4,8,16,32,64,128) AND
    agent_core_log_rotate_count IN (0,1,2,3,5) AND metric_retention_days IN (7,14,30) AND
    audit_retention_days IN (0,30,90,180) AND task_retention_days IN (0,30,90,180) AND
    config_revision_retention IN (0,50,100)
);

CREATE TABLE IF NOT EXISTS panel_users (
    id text PRIMARY KEY,
    username varchar(64) NOT NULL,
    display_name varchar(100) NOT NULL DEFAULT '',
    role varchar(20) NOT NULL CHECK (role IN ('admin','user')),
    permissions jsonb NOT NULL DEFAULT '[]'::jsonb,
    password_hash varchar(100) NOT NULL,
    disabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    last_login_at timestamptz
);
ALTER TABLE panel_users DROP CONSTRAINT IF EXISTS panel_users_role_check;
ALTER TABLE panel_users ADD COLUMN IF NOT EXISTS permissions jsonb NOT NULL DEFAULT '[]'::jsonb;
UPDATE panel_users SET permissions = CASE role
    WHEN 'admin' THEN '[]'::jsonb
    WHEN 'operator' THEN '["overview.read","agents.read","deployments.read","client-access.read","catalogs.read","agent-config.read","agent-config.write","configs.read","configs.write","tasks.read","tasks.execute","settings.read","audit.read","metrics.read","core-logs.read","templates.read","templates.write"]'::jsonb
    WHEN 'auditor' THEN '["overview.read","agents.read","deployments.read","tasks.read","audit.read","metrics.read","core-logs.read"]'::jsonb
    WHEN 'readonly' THEN '["overview.read","agents.read","deployments.read","client-access.read","catalogs.read","agent-config.read","configs.read","tasks.read","settings.read","audit.read","metrics.read","core-logs.read","templates.read"]'::jsonb
    ELSE permissions END
    WHERE role IN ('operator','auditor','readonly');
UPDATE panel_users SET role='user' WHERE role IN ('operator','auditor','readonly');
ALTER TABLE panel_users ADD CONSTRAINT panel_users_role_check CHECK (role IN ('admin','user'));
CREATE UNIQUE INDEX IF NOT EXISTS panel_users_username_unique_idx ON panel_users(lower(username));
CREATE INDEX IF NOT EXISTS panel_users_status_idx ON panel_users(disabled,username);
ALTER TABLE panel_users ADD COLUMN IF NOT EXISTS agent_isolation boolean NOT NULL DEFAULT false;
ALTER TABLE panel_users ADD COLUMN IF NOT EXISTS auth_revision bigint NOT NULL DEFAULT 1;
CREATE TABLE IF NOT EXISTS user_panel_settings (
    owner_id text PRIMARY KEY,
    content text NOT NULL,
    runtime jsonb NOT NULL DEFAULT '{}'::jsonb,
    revision bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS user_substore_settings (
    owner_id text PRIMARY KEY,
    endpoint_ciphertext text NOT NULL,
    backend_key text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE panel_users ADD COLUMN IF NOT EXISTS agent_access_revision bigint NOT NULL DEFAULT 1;
CREATE TABLE IF NOT EXISTS agent_shares (
	id text PRIMARY KEY,
	user_id text NOT NULL REFERENCES panel_users(id),
	agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	enabled boolean NOT NULL DEFAULT true,
	limit_bytes bigint NOT NULL DEFAULT 0 CHECK (limit_bytes>=0),
	used_bytes bigint NOT NULL DEFAULT 0 CHECK (used_bytes>=0),
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (user_id,agent_id)
);
CREATE INDEX IF NOT EXISTS agent_shares_agent_idx ON agent_shares(agent_id);
ALTER TABLE agent_shares ADD COLUMN IF NOT EXISTS status varchar(10) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected'));
ALTER TABLE agent_shares ADD COLUMN IF NOT EXISTS invitation_revision bigint NOT NULL DEFAULT 1 CHECK (invitation_revision>0);
ALTER TABLE agent_shares ADD COLUMN IF NOT EXISTS engines text[] NOT NULL DEFAULT '{}'
	CHECK (engines <@ ARRAY['mihomo','xray','sing-box','ss-rust']::text[] AND cardinality(engines)<=4 AND array_position(engines,NULL) IS NULL);
ALTER TABLE agent_shares ADD COLUMN IF NOT EXISTS ports_unrestricted boolean NOT NULL DEFAULT false;
CREATE TABLE IF NOT EXISTS agent_share_ports (
	agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
	share_id text NOT NULL REFERENCES agent_shares(id) ON DELETE CASCADE,
	PRIMARY KEY (agent_id,port)
);
CREATE INDEX IF NOT EXISTS agent_share_ports_share_idx ON agent_share_ports(share_id);

INSERT INTO panel_settings (
    id,panel_name,panel_description,task_page_size,task_poll_interval_ms,updated_at
) VALUES (1,'QControlHub','可信远程编排',100,600,now())
ON CONFLICT (id) DO NOTHING;

-- agents_active_seen_idx was retired in v50. It was built on last_seen, which
-- changes on every heartbeat, so it forced every liveness update to rewrite an
-- index entry it could never benefit from: no update touching last_seen can be
-- a heap-only update while such an index exists. The panel reads it for an
-- online count over a table with one row per node, which a sequential scan
-- answers at least as cheaply.
CREATE UNIQUE INDEX IF NOT EXISTS agents_public_key_unique_idx ON agents(public_key);
	CREATE INDEX IF NOT EXISTS configs_active_updated_idx ON configs(updated_at DESC) WHERE deleted_at IS NULL;
	DROP INDEX IF EXISTS configs_agent_engine_unique_idx;
	CREATE UNIQUE INDEX IF NOT EXISTS configs_agent_engine_owner_unique_idx ON configs(agent_id,engine,owner_id) WHERE agent_id IS NOT NULL AND deleted_at IS NULL;
	CREATE INDEX IF NOT EXISTS configs_owner_updated_idx ON configs(owner_id,updated_at DESC) WHERE deleted_at IS NULL;
	CREATE INDEX IF NOT EXISTS config_revisions_recent_idx ON config_revisions(config_id,version DESC);
CREATE INDEX IF NOT EXISTS tasks_agent_queue_idx ON tasks(agent_id, status, created_at);
CREATE INDEX IF NOT EXISTS tasks_created_idx ON tasks(created_at DESC);
CREATE INDEX IF NOT EXISTS tasks_status_created_idx ON tasks(status,created_at DESC);
CREATE INDEX IF NOT EXISTS tasks_agent_created_idx ON tasks(agent_id,created_at DESC);
CREATE INDEX IF NOT EXISTS tasks_system_tcp_recent_idx ON tasks(agent_id,created_at DESC,id DESC) WHERE action IN ('enable-bbr','disable-bbr','configure-tcp');
CREATE INDEX IF NOT EXISTS tasks_retention_idx ON tasks((COALESCE(finished_at,created_at))) WHERE status IN ('succeeded','failed','canceled');
CREATE INDEX IF NOT EXISTS tasks_latest_deployment_idx ON tasks(agent_id,engine,finished_at DESC) WHERE action IN ('deploy','import-existing') AND status='succeeded';
CREATE UNIQUE INDEX IF NOT EXISTS tasks_one_running_per_agent_idx ON tasks(agent_id) WHERE status='running';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS owner_id text NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS shared_traffic_id text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS agent_engine_ownership (
	agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	engine varchar(20) NOT NULL,
	owner_id text NOT NULL,
	config_id text NOT NULL,
	config_version integer NOT NULL,
	running boolean NOT NULL DEFAULT true,
	updated_at timestamptz NOT NULL,
	PRIMARY KEY (agent_id,engine)
);
ALTER TABLE agent_engine_ownership ADD COLUMN IF NOT EXISTS uncertain boolean NOT NULL DEFAULT false;
ALTER TABLE agent_engine_ownership ADD COLUMN IF NOT EXISTS config_uncertain boolean NOT NULL DEFAULT false;
ALTER TABLE agent_engine_ownership ADD COLUMN IF NOT EXISTS traffic_settled boolean NOT NULL DEFAULT false;
INSERT INTO agent_engine_ownership(agent_id,engine,owner_id,config_id,config_version,running,uncertain,config_uncertain,updated_at)
SELECT latest.agent_id,latest.engine,COALESCE(c.owner_id,deployed.owner_id,latest.owner_id),
	COALESCE(deployed.config_id,latest.config_id,''),COALESCE(deployed.config_version,latest.config_version,0),
	NOT(latest.action='stop' AND latest.status='succeeded'),
	latest.status<>'succeeded',
	EXISTS(
		SELECT 1 FROM tasks failed WHERE failed.agent_id=latest.agent_id AND failed.engine=latest.engine
			AND failed.action IN ('deploy','import-existing') AND failed.status<>'succeeded'
			AND failed.started_at IS NOT NULL AND failed.started_at>COALESCE(deployed.finished_at,'-infinity'::timestamptz)),
	COALESCE(latest.finished_at,latest.started_at,latest.created_at)
FROM (SELECT DISTINCT ON(agent_id,engine) *
	FROM tasks WHERE action IN ('deploy','import-existing','start','restart','stop')
		AND (started_at IS NOT NULL OR status='succeeded')
	ORDER BY agent_id,engine,COALESCE(started_at,finished_at,created_at) DESC,created_at DESC,id DESC) latest
LEFT JOIN LATERAL (
	SELECT owner_id,config_id,config_version,finished_at FROM tasks
	WHERE agent_id=latest.agent_id AND engine=latest.engine AND action IN ('deploy','import-existing')
		AND status='succeeded' AND config_id IS NOT NULL AND finished_at IS NOT NULL
	ORDER BY finished_at DESC,id DESC LIMIT 1
) deployed ON true
LEFT JOIN configs c ON c.id=COALESCE(deployed.config_id,latest.config_id)
ON CONFLICT(agent_id,engine) DO NOTHING;
CREATE INDEX IF NOT EXISTS tasks_owner_created_idx ON tasks(owner_id,created_at DESC);
CREATE TABLE IF NOT EXISTS metric_samples (
    agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    sampled_at timestamptz NOT NULL,
    cpu_percent real NOT NULL,
    memory_percent real NOT NULL DEFAULT 0,
    rx_rate_bps bigint NOT NULL DEFAULT 0 CHECK (rx_rate_bps >= 0),
    tx_rate_bps bigint NOT NULL DEFAULT 0 CHECK (tx_rate_bps >= 0),
    PRIMARY KEY (agent_id, sampled_at)
);
CREATE INDEX IF NOT EXISTS metric_samples_recent_idx ON metric_samples(agent_id, sampled_at DESC);
CREATE INDEX IF NOT EXISTS metric_samples_retention_idx ON metric_samples(sampled_at);

CREATE TABLE IF NOT EXISTS port_traffic_policies (
    id text PRIMARY KEY,
    agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    name varchar(100) NOT NULL,
    engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
    port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    protocol varchar(8) NOT NULL CHECK (protocol IN ('tcp','udp','both')),
    cycle varchar(8) NOT NULL CHECK (cycle IN ('monthly','yearly')),
    cycle_anchor date NOT NULL,
	limit_bytes bigint NOT NULL CHECK (limit_bytes >= 0),
	auto_block boolean NOT NULL DEFAULT true,
	quota_enabled boolean NOT NULL DEFAULT true,
	monitoring_enabled boolean NOT NULL DEFAULT true,
	discovered boolean NOT NULL DEFAULT false,
	traffic_history_initialized boolean NOT NULL DEFAULT false,
    reset_generation bigint NOT NULL DEFAULT 1 CHECK (reset_generation > 0),
    received_bytes bigint NOT NULL DEFAULT 0 CHECK (received_bytes >= 0),
    sent_bytes bigint NOT NULL DEFAULT 0 CHECK (sent_bytes >= 0),
	reported_received_bytes bigint NOT NULL DEFAULT 0 CHECK (reported_received_bytes >= 0),
	reported_sent_bytes bigint NOT NULL DEFAULT 0 CHECK (reported_sent_bytes >= 0),
    used_bytes bigint NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
    receive_bps bigint NOT NULL DEFAULT 0 CHECK (receive_bps >= 0),
    send_bps bigint NOT NULL DEFAULT 0 CHECK (send_bps >= 0),
    period_start timestamptz,
    period_end timestamptz,
    blocked boolean NOT NULL DEFAULT false,
    enforcement_available boolean NOT NULL DEFAULT false,
    enforcement_error varchar(500) NOT NULL DEFAULT '',
    last_reported_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (agent_id,port)
);
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS auto_block boolean NOT NULL DEFAULT true;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS quota_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS monitoring_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS discovered boolean NOT NULL DEFAULT false;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS metadata_managed boolean NOT NULL DEFAULT false;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS share_id text REFERENCES agent_shares(id);
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS share_used_bytes bigint NOT NULL DEFAULT 0 CHECK (share_used_bytes >= 0);
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS share_generation bigint NOT NULL DEFAULT 0 CHECK (share_generation >= 0);
CREATE INDEX IF NOT EXISTS port_traffic_share_idx ON port_traffic_policies(share_id) WHERE share_id IS NOT NULL;
ALTER TABLE port_traffic_policies DROP CONSTRAINT IF EXISTS port_traffic_policies_limit_bytes_check;
ALTER TABLE port_traffic_policies ADD CONSTRAINT port_traffic_policies_limit_bytes_check CHECK (limit_bytes >= 0);
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS traffic_history_initialized boolean NOT NULL DEFAULT false;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS last_collected_at timestamptz;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS counter_epoch varchar(32) NOT NULL DEFAULT '';
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS reported_lifetime_received_bytes bigint NOT NULL DEFAULT 0 CHECK (reported_lifetime_received_bytes >= 0);
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS reported_lifetime_sent_bytes bigint NOT NULL DEFAULT 0 CHECK (reported_lifetime_sent_bytes >= 0);
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS accounting jsonb;
CREATE TABLE IF NOT EXISTS port_traffic_daily_accounting (
  policy_id text NOT NULL REFERENCES port_traffic_policies(id) ON DELETE CASCADE,
  reset_generation bigint NOT NULL,
  usage_date date NOT NULL,
  source varchar(16) NOT NULL CHECK (source IN ('listener','core-api','nft-dual')),
  received_bytes bigint NOT NULL CHECK (received_bytes >= 0),
  sent_bytes bigint NOT NULL CHECK (sent_bytes >= 0),
  PRIMARY KEY (policy_id,reset_generation,usage_date,source)
);
CREATE TABLE IF NOT EXISTS port_traffic_accounting_epochs (
  policy_id text NOT NULL REFERENCES port_traffic_policies(id) ON DELETE CASCADE,
  reset_generation bigint NOT NULL,
  counter_epoch varchar(32) NOT NULL,
  accounting jsonb NOT NULL,
  lifetime_received_bytes bigint NOT NULL CHECK (lifetime_received_bytes >= 0),
  lifetime_sent_bytes bigint NOT NULL CHECK (lifetime_sent_bytes >= 0),
  first_collected_at timestamptz NOT NULL,
  last_collected_at timestamptz NOT NULL,
  PRIMARY KEY (policy_id,reset_generation,counter_epoch)
);
-- Attribution history survives port removal, just like daily_usage. Keep
-- node ownership independently so explicit node deletion still clears it.
ALTER TABLE port_traffic_daily_accounting ADD COLUMN IF NOT EXISTS agent_id text REFERENCES agents(id) ON DELETE CASCADE;
ALTER TABLE port_traffic_accounting_epochs ADD COLUMN IF NOT EXISTS agent_id text REFERENCES agents(id) ON DELETE CASCADE;
UPDATE port_traffic_daily_accounting h SET agent_id=p.agent_id FROM port_traffic_policies p WHERE h.policy_id=p.id AND h.agent_id IS NULL;
UPDATE port_traffic_accounting_epochs h SET agent_id=p.agent_id FROM port_traffic_policies p WHERE h.policy_id=p.id AND h.agent_id IS NULL;
ALTER TABLE port_traffic_daily_accounting ALTER COLUMN agent_id SET NOT NULL;
ALTER TABLE port_traffic_accounting_epochs ALTER COLUMN agent_id SET NOT NULL;
ALTER TABLE port_traffic_daily_accounting DROP CONSTRAINT IF EXISTS port_traffic_daily_accounting_policy_id_fkey;
ALTER TABLE port_traffic_accounting_epochs DROP CONSTRAINT IF EXISTS port_traffic_accounting_epochs_policy_id_fkey;
CREATE INDEX IF NOT EXISTS port_traffic_daily_accounting_agent_idx ON port_traffic_daily_accounting(agent_id);
CREATE INDEX IF NOT EXISTS port_traffic_accounting_epochs_agent_idx ON port_traffic_accounting_epochs(agent_id);
-- Version 44 did not retain listener-only epochs. Seed its current confirmed
-- baseline before another epoch can replace the policy's latest pointer.
INSERT INTO port_traffic_accounting_epochs(policy_id,agent_id,reset_generation,counter_epoch,accounting,
  lifetime_received_bytes,lifetime_sent_bytes,first_collected_at,last_collected_at)
SELECT id,agent_id,reset_generation,counter_epoch,COALESCE(accounting,'{"source":"listener"}'::jsonb),
  reported_lifetime_received_bytes,reported_lifetime_sent_bytes,last_collected_at,last_collected_at
FROM port_traffic_policies WHERE counter_epoch<>'' AND last_collected_at IS NOT NULL
ON CONFLICT (policy_id,reset_generation,counter_epoch) DO NOTHING;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS quota_notification_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE port_traffic_policies DROP CONSTRAINT IF EXISTS port_traffic_policies_quota_notification_generation_check;
ALTER TABLE port_traffic_policies ADD CONSTRAINT port_traffic_policies_quota_notification_generation_check CHECK (quota_notification_generation >= 0);
DO $traffic_baselines$
DECLARE
    add_reported_received boolean;
    add_reported_sent boolean;
BEGIN
    SELECT NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema=current_schema() AND table_name='port_traffic_policies' AND column_name='reported_received_bytes'
    ) INTO add_reported_received;
    SELECT NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema=current_schema() AND table_name='port_traffic_policies' AND column_name='reported_sent_bytes'
    ) INTO add_reported_sent;

    ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS reported_received_bytes bigint NOT NULL DEFAULT 0;
    ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS reported_sent_bytes bigint NOT NULL DEFAULT 0;
    IF add_reported_received THEN
        UPDATE port_traffic_policies SET reported_received_bytes=received_bytes;
    END IF;
    IF add_reported_sent THEN
        UPDATE port_traffic_policies SET reported_sent_bytes=sent_bytes;
    END IF;
END
$traffic_baselines$;
ALTER TABLE port_traffic_policies DROP CONSTRAINT IF EXISTS port_traffic_policies_reported_received_bytes_check;
ALTER TABLE port_traffic_policies ADD CONSTRAINT port_traffic_policies_reported_received_bytes_check CHECK (reported_received_bytes >= 0);
ALTER TABLE port_traffic_policies DROP CONSTRAINT IF EXISTS port_traffic_policies_reported_sent_bytes_check;
ALTER TABLE port_traffic_policies ADD CONSTRAINT port_traffic_policies_reported_sent_bytes_check CHECK (reported_sent_bytes >= 0);
-- port_traffic_policies_agent_idx was retired in v51: it duplicated the
-- leading columns of the (agent_id, port) unique constraint, which serves the
-- same lookups, while being maintained on every policy write for zero scans.

CREATE TABLE IF NOT EXISTS port_traffic_daily_usage (
	policy_id text NOT NULL,
	reset_generation bigint NOT NULL CHECK (reset_generation > 0),
	usage_date date NOT NULL,
	agent_id text NOT NULL,
	name varchar(100) NOT NULL,
	engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
	port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
	protocol varchar(8) NOT NULL CHECK (protocol IN ('tcp','udp','both')),
	received_bytes bigint NOT NULL DEFAULT 0 CHECK (received_bytes >= 0),
	sent_bytes bigint NOT NULL DEFAULT 0 CHECK (sent_bytes >= 0),
	used_bytes bigint NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
	peak_receive_bps bigint NOT NULL DEFAULT 0 CHECK (peak_receive_bps >= 0),
	peak_send_bps bigint NOT NULL DEFAULT 0 CHECK (peak_send_bps >= 0),
	sample_count bigint NOT NULL DEFAULT 0 CHECK (sample_count >= 0),
	first_reported_at timestamptz NOT NULL,
	last_reported_at timestamptz NOT NULL,
	PRIMARY KEY (policy_id,reset_generation,usage_date)
);
CREATE INDEX IF NOT EXISTS port_traffic_daily_agent_date_idx ON port_traffic_daily_usage(agent_id,usage_date,port);
CREATE INDEX IF NOT EXISTS port_traffic_daily_policy_date_idx ON port_traffic_daily_usage(policy_id,usage_date);
CREATE INDEX IF NOT EXISTS port_traffic_daily_date_idx ON port_traffic_daily_usage(usage_date,agent_id);

CREATE TABLE IF NOT EXISTS substore_sync_settings (
	id smallint PRIMARY KEY CHECK (id = 1),
	endpoint_ciphertext text NOT NULL,
	subscription_name varchar(100) NOT NULL,
	integration_id text NOT NULL,
	last_synced_at timestamptz,
	last_sync_status varchar(10) NOT NULL DEFAULT 'never' CHECK (last_sync_status IN ('never','success','failed')),
	last_sync_error varchar(500) NOT NULL DEFAULT '',
	updated_at timestamptz NOT NULL
);
ALTER TABLE substore_sync_settings ADD COLUMN IF NOT EXISTS backend_key text NOT NULL DEFAULT '';
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
);
ALTER TABLE substore_sync_targets ADD COLUMN IF NOT EXISTS display_name varchar(100);
ALTER TABLE substore_sync_targets ADD COLUMN IF NOT EXISTS sync_mode varchar(12) NOT NULL DEFAULT 'managed';
ALTER TABLE substore_sync_targets ADD COLUMN IF NOT EXISTS owner_id text NOT NULL DEFAULT '';
ALTER TABLE substore_sync_targets ADD COLUMN IF NOT EXISTS backend_key text NOT NULL DEFAULT '';
ALTER TABLE substore_sync_targets DROP CONSTRAINT IF EXISTS substore_sync_targets_subscription_name_key;
ALTER TABLE substore_sync_targets DROP CONSTRAINT IF EXISTS substore_sync_targets_integration_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS substore_sync_targets_owner_name_idx ON substore_sync_targets(owner_id,subscription_name);
CREATE UNIQUE INDEX IF NOT EXISTS substore_sync_targets_backend_name_idx ON substore_sync_targets(backend_key,subscription_name) WHERE backend_key<>'';
CREATE UNIQUE INDEX IF NOT EXISTS substore_sync_targets_owner_integration_idx ON substore_sync_targets(owner_id,integration_id);
CREATE UNIQUE INDEX IF NOT EXISTS substore_sync_targets_backend_integration_idx ON substore_sync_targets(backend_key,integration_id) WHERE backend_key<>'';
ALTER TABLE substore_sync_targets ADD COLUMN IF NOT EXISTS sync_format varchar(8) NOT NULL DEFAULT 'url' CHECK (sync_format IN ('url','mihomo'));
ALTER TABLE substore_sync_targets DROP CONSTRAINT IF EXISTS substore_sync_targets_sync_mode_check;
ALTER TABLE substore_sync_targets ADD CONSTRAINT substore_sync_targets_sync_mode_check CHECK (sync_mode IN ('incremental','managed'));
UPDATE substore_sync_targets SET display_name=subscription_name WHERE display_name IS NULL;
ALTER TABLE substore_sync_targets ALTER COLUMN display_name SET NOT NULL;
DROP INDEX IF EXISTS substore_sync_targets_display_name_idx;
CREATE UNIQUE INDEX IF NOT EXISTS substore_sync_targets_owner_display_name_idx ON substore_sync_targets(owner_id,display_name);
CREATE TABLE IF NOT EXISTS substore_sync_items (
	target_id text NOT NULL REFERENCES substore_sync_targets(id) ON DELETE CASCADE,
	agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
	profile_tag text NOT NULL CHECK (octet_length(profile_tag) BETWEEN 1 AND 800),
	custom_name text NOT NULL CHECK (octet_length(custom_name) BETWEEN 1 AND 400),
	address_mode varchar(8) NOT NULL DEFAULT 'auto' CHECK (address_mode IN ('auto','ipv4','ipv6','both')),
	created_at timestamptz NOT NULL,
	updated_at timestamptz NOT NULL,
	PRIMARY KEY (target_id,agent_id,engine,profile_tag)
);
ALTER TABLE substore_sync_items ADD COLUMN IF NOT EXISTS target_id text;
ALTER TABLE substore_sync_items ADD COLUMN IF NOT EXISTS config_id text NOT NULL DEFAULT '';
ALTER TABLE substore_sync_items ADD COLUMN IF NOT EXISTS address_mode varchar(8) NOT NULL DEFAULT 'auto';
ALTER TABLE substore_sync_items DROP CONSTRAINT IF EXISTS substore_sync_items_address_mode_check;
ALTER TABLE substore_sync_items ADD CONSTRAINT substore_sync_items_address_mode_check CHECK (address_mode IN ('auto','ipv4','ipv6','both'));
UPDATE substore_sync_items
SET target_id=(SELECT id FROM substore_sync_targets ORDER BY created_at,id LIMIT 1)
WHERE target_id IS NULL;
ALTER TABLE substore_sync_items ALTER COLUMN target_id SET NOT NULL;
ALTER TABLE substore_sync_items DROP CONSTRAINT IF EXISTS substore_sync_items_pkey;
ALTER TABLE substore_sync_items ADD CONSTRAINT substore_sync_items_pkey PRIMARY KEY (target_id,agent_id,engine,profile_tag);
DO $substore_target_fk$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conrelid='substore_sync_items'::regclass AND conname='substore_sync_items_target_id_fkey'
	) THEN
		ALTER TABLE substore_sync_items ADD CONSTRAINT substore_sync_items_target_id_fkey
			FOREIGN KEY (target_id) REFERENCES substore_sync_targets(id) ON DELETE CASCADE;
	END IF;
END
$substore_target_fk$;
DROP INDEX IF EXISTS substore_sync_items_created_idx;
CREATE INDEX substore_sync_items_created_idx ON substore_sync_items(target_id,created_at,agent_id);

-- Agent deletion is intentionally a soft revocation, so foreign-key cascades
-- do not run. Remove traffic rows left by versions that did not clean them in
-- DeleteAgent; otherwise revoked nodes survive as orphan cards indefinitely.
DELETE FROM port_traffic_daily_usage
WHERE agent_id IN (SELECT id FROM agents WHERE revoked_at IS NOT NULL);
DELETE FROM port_traffic_daily_accounting
WHERE agent_id IN (SELECT id FROM agents WHERE revoked_at IS NOT NULL);
DELETE FROM port_traffic_accounting_epochs
WHERE agent_id IN (SELECT id FROM agents WHERE revoked_at IS NOT NULL);
DELETE FROM port_traffic_policies
WHERE agent_id IN (SELECT id FROM agents WHERE revoked_at IS NOT NULL);
DELETE FROM substore_sync_items
WHERE agent_id IN (SELECT id FROM agents WHERE revoked_at IS NOT NULL);

CREATE TABLE IF NOT EXISTS audit_logs (
    id bigserial PRIMARY KEY,
    acted_at timestamptz NOT NULL DEFAULT now(),
    actor varchar(40) NOT NULL DEFAULT 'admin',
    action varchar(40) NOT NULL,
    target text NOT NULL DEFAULT '',
    detail text NOT NULL DEFAULT '',
    remote_ip varchar(64) NOT NULL DEFAULT ''
);
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS owner_id text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS audit_logs_owner_recent_idx ON audit_logs(owner_id,acted_at DESC);
CREATE INDEX IF NOT EXISTS audit_logs_recent_idx ON audit_logs(acted_at DESC);CREATE TABLE IF NOT EXISTS config_templates ( id text PRIMARY KEY, name varchar(100) NOT NULL, engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')), content text NOT NULL CHECK (octet_length(content) <= 4194304), created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL ); CREATE INDEX IF NOT EXISTS config_templates_recent_idx ON config_templates(updated_at DESC);
ALTER TABLE config_templates ADD COLUMN IF NOT EXISTS owner_id text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS config_templates_owner_updated_idx ON config_templates(owner_id,updated_at DESC);
CREATE INDEX IF NOT EXISTS enrollment_tokens_active_idx ON enrollment_tokens(expires_at) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS agent_nonces_expiry_idx ON agent_nonces(expires_at);

-- Traffic accounting rewrites the same few rows on every agent report (roughly
-- once per second per policy), and the daily rollups are upserted far more often
-- than they are read. At the default fillfactor of 100 every new row version
-- lands at the end of a page, so the page splits as soon as it fills, dead
-- versions are only marked reusable, and the tables grow without bound. A live
-- instance showed 25 policy rows occupying 2.4 MB with 94.6% of that free space.
-- Reserving room per page lets these hot updates stay on-page as heap-only
-- tuples, which is what keeps the storage bounded.
ALTER TABLE port_traffic_policies SET (fillfactor = 75);
ALTER TABLE port_traffic_daily_usage SET (fillfactor = 85);
ALTER TABLE port_traffic_daily_accounting SET (fillfactor = 85);
ALTER TABLE port_traffic_accounting_epochs SET (fillfactor = 85);

-- Connection observations are durable on the panel only. Minute buckets
-- coalesce repeated heartbeat samples without pretending they are new sessions.
CREATE TABLE IF NOT EXISTS client_connection_sources (
 agent_id text PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
 updated_at timestamptz NOT NULL,
 status text NOT NULL CHECK(status IN ('ok','partial','unavailable')),
 detail text NOT NULL DEFAULT '',
 truncated boolean NOT NULL DEFAULT false
);
CREATE TABLE IF NOT EXISTS client_connections (
 id bigserial PRIMARY KEY,
 agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
 bucket timestamptz NOT NULL,
 engine text NOT NULL,
 protocol text NOT NULL,
 inbound text NOT NULL,
 transport text NOT NULL CHECK(transport IN ('tcp','udp')),
 client_ip inet NOT NULL,
 client_port integer NOT NULL CHECK(client_port BETWEEN 1 AND 65535),
 local_ip inet NOT NULL,
 local_port integer NOT NULL CHECK(local_port BETWEEN 1 AND 65535),
 first_seen timestamptz NOT NULL,
 last_seen timestamptz NOT NULL,
 UNIQUE(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port)
);
CREATE INDEX IF NOT EXISTS client_connections_time_idx ON client_connections(bucket);
CREATE INDEX IF NOT EXISTS client_connections_agent_time_idx ON client_connections(agent_id,bucket DESC);
CREATE INDEX IF NOT EXISTS client_connections_ip_time_idx ON client_connections(client_ip,bucket DESC);

CREATE TABLE IF NOT EXISTS client_connection_locations (
 client_ip inet PRIMARY KEY,
 country_code varchar(2) NOT NULL DEFAULT '',
 country varchar(200) NOT NULL DEFAULT '',
 province varchar(100) NOT NULL DEFAULT '',
 retry_after timestamptz NOT NULL
);
`
