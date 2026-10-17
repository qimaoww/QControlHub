package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const coreLogRetention = 7 * 24 * time.Hour

type CoreLogQuery struct {
	AgentID    string
	InstanceID string
	UserID     string
	Engine     core.Engine
	Level      string
	Search     string
	Before     int64
	// Limit bounds each engine independently within the selected node scope.
	Limit int
}

func (s *Store) StoreCoreLogs(ctx context.Context, agentID string, batch core.CoreLogBatch) error {
	if !validCoreLogBatchID(batch.ID) || len(batch.Entries) == 0 || len(batch.Entries) > core.MaxCoreLogBatchEntries {
		return fmt.Errorf("%w: invalid core log batch", ErrInvalid)
	}
	receivedAt := time.Now().UTC()
	entries := make([]core.CoreLogEntry, 0, len(batch.Entries))
	for _, entry := range batch.Entries {
		entry.Message = sanitizeCoreLogMessage(entry.Message)
		entry.Level = normalizeCoreLogLevel(entry.Level)
		if !entry.Engine.Valid() || entry.Level == "" || entry.Message == "" ||
			!utf8.ValidString(entry.Message) || len([]byte(entry.Message)) > core.MaxCoreLogMessageBytes || strings.ContainsRune(entry.Message, '\x00') {
			return fmt.Errorf("%w: invalid core log entry", ErrInvalid)
		}
		if entry.LoggedAt.IsZero() || entry.LoggedAt.After(receivedAt.Add(5*time.Minute)) {
			entry.LoggedAt = receivedAt
		}
		if entry.LoggedAt.Before(receivedAt.Add(-coreLogRetention)) {
			continue
		}
		entries = append(entries, entry)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize uploads with revocation so an already authenticated request
	// cannot recreate logs after the deletion worker has drained them.
	var minimumLevel, nodeOwner string
	if err := tx.QueryRow(ctx, `SELECT `+agentRuntimeSettingSQL("agents", "core_log_minimum_level", "'debug'")+`
		,agents.owner_id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR SHARE OF agents`, agentID).Scan(&minimumLevel, &nodeOwner); err != nil {
		return fmt.Errorf("read core log minimum level: %w", mapError(err))
	}
	minimumLevel = normalizeCoreLogMinimumLevel(minimumLevel)
	if minimumLevel == "" {
		return errors.New("invalid stored core log minimum level")
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO core_log_batches (id,agent_id,received_at) VALUES ($1,$2,$3)
		ON CONFLICT (id) DO NOTHING`, batch.ID, agentID, receivedAt)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		var existingAgentID string
		if err := tx.QueryRow(ctx, `SELECT agent_id FROM core_log_batches WHERE id=$1`, batch.ID).Scan(&existingAgentID); err != nil {
			return mapError(err)
		}
		if existingAgentID != agentID {
			return fmt.Errorf("%w: core log batch belongs to another agent", ErrInvalid)
		}
		return tx.Commit(ctx)
	}
	// Only shared batches need the single ownership lookup. Main/legacy batches
	// retain their existing round-trip budget.
	shared := false
	for _, entry := range entries {
		shared = shared || (entry.InstanceID != "" && entry.InstanceID != "main")
	}
	owners := map[string]string{}
	if shared {
		sourceRows, err := tx.Query(ctx, `SELECT o.engine,o.share_id,o.owner_id FROM agent_shared_instance_ownership o
 JOIN agent_shares sh ON sh.id=o.share_id AND sh.agent_id=o.agent_id AND sh.user_id=o.owner_id
 WHERE o.agent_id=$1`, agentID)
		if err != nil {
			return err
		}
		for sourceRows.Next() {
			var engine, instance, owner string
			if err := sourceRows.Scan(&engine, &instance, &owner); err != nil {
				sourceRows.Close()
				return err
			}
			owners[engine+"/"+instance] = owner
		}
		sourceRows.Close()
		if err := sourceRows.Err(); err != nil {
			return err
		}
	}
	instances, services, ownerIDs, nodeOwners := []string{}, []string{}, []string{}, []string{}
	indexes := make([]int32, 0, len(entries))
	engines := make([]string, 0, len(entries))
	levels := make([]string, 0, len(entries))
	messages := make([]string, 0, len(entries))
	loggedAt := make([]time.Time, 0, len(entries))
	accepted := make([]core.CoreLogEntry, 0, len(entries))
	for index, entry := range entries {
		if !coreLogLevelAtLeast(entry.Level, minimumLevel) {
			continue
		}
		entry.OwnerID = nodeOwner
		entry.NodeOwnerID = nodeOwner
		if entry.InstanceID != "" {
			instance, valid := core.CoreLogInstance(entry.Engine, entry.Service)
			if !valid || instance != entry.InstanceID {
				continue
			}
			if instance != "main" {
				owner, known := owners[string(entry.Engine)+"/"+instance]
				if !known {
					continue
				}
				entry.OwnerID = owner
			}
		} else {
			entry.Service = ""
		} // Legacy data is node-scoped and never recipient-scoped.
		instances = append(instances, entry.InstanceID)
		services = append(services, entry.Service)
		ownerIDs = append(ownerIDs, entry.OwnerID)
		nodeOwners = append(nodeOwners, nodeOwner)
		entry.ReceivedAt = receivedAt
		if entry.InstanceID == "" || entry.InstanceID == "main" {
			accepted = append(accepted, entry)
		}
		indexes = append(indexes, int32(index))
		engines = append(engines, string(entry.Engine))
		levels = append(levels, entry.Level)
		messages = append(messages, entry.Message)
		loggedAt = append(loggedAt, entry.LoggedAt.UTC())
	}
	// Keep the deduplication marker and all accepted entries in the same
	// transaction, but pay only one remote round trip for the entire batch.
	if len(indexes) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO core_logs (batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at,instance_id,service,owner_id,node_owner_id)
			SELECT $1,entry_index,$2,engine,level,message,logged_at,$3,instance,service,owner_id,node_owner_id
			FROM unnest($4::integer[],$5::text[],$6::text[],$7::text[],$8::timestamptz[],$9::text[],$10::text[],$11::text[],$12::text[])
			AS entries(entry_index,engine,level,message,logged_at,instance,service,owner_id,node_owner_id)`,
			batch.ID, agentID, receivedAt, indexes, engines, levels, messages, loggedAt, instances, services, ownerIDs, nodeOwners); err != nil {
			return mapError(err)
		}
	}
	if err := s.storeClientConnectionLogs(ctx, tx, agentID, accepted); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PruneCoreLogBatches removes deduplication markers that no longer guard any
// retained log row. Log rows live in daily partitions and are dropped a whole
// day at a time by PruneCoreLogPartitions, and a partitioned table cannot carry
// ON DELETE CASCADE, so this only retires markers whose rows are already gone.
//
// The planner turns the anti-join into a hash right anti join: one indexed scan
// of the expired markers against one scan of the retained log rows, with no
// per-marker probe. Measured on a live instance at 89 ms once the pages are
// cached. The first pass after a PostgreSQL restart read 7551 blocks and took
// 7.1 s, which is a cold-cache cost rather than a property of the statement, so
// the shape below is left as the planner prefers it and only pinned by a test
// that forbids a nested-loop probe from creeping back in.
func (s *Store) PruneCoreLogBatches(ctx context.Context, olderThan time.Time) (int64, error) {
	command, err := s.pool.Exec(ctx, coreLogBatchPruneSQL, olderThan.UTC())
	if err != nil {
		return 0, fmt.Errorf("prune core log batches: %w", err)
	}
	return command.RowsAffected(), nil
}

// coreLogBatchPruneSQL is split out so the plan can be inspected against the
// production statement itself rather than a copy, which keeps the two from
// drifting apart.
const coreLogBatchPruneSQL = `
		DELETE FROM core_log_batches batch
		WHERE batch.received_at < $1
		  AND NOT EXISTS (SELECT 1 FROM core_logs log WHERE log.batch_id = batch.id)`

// coreLogWindowStatement renders the window query and its arguments.
//
// It is split out so the query plan can be inspected against the production
// statement itself rather than a copy: the covering index only helps while the
// projection and the predicate are exactly these, and a copy would let the two
// drift apart silently.
func coreLogWindowStatement(query CoreLogQuery, engines []string) (string, []any) {
	return scopedCoreLogWindowStatement(context.Background(), query, engines)
}

func scopedCoreLogWindowStatement(ctx context.Context, query CoreLogQuery, engines []string) (string, []any) {
	if _, scoped := requestConfigScope(ctx); scoped {
		return authorizedCoreLogWindowStatement(ctx, query, engines)
	}
	// Bound each engine's indexed scan before merging the results. A busy
	// engine must not consume the other engines' slots, and we should not rank
	// the entire retained log history just to display a small recent window.
	where := "engine=selected.engine"
	args := []any{engines, query.Limit}
	for _, filter := range []struct{ column, value string }{{"agent_id", query.AgentID}, {"level", query.Level}} {
		if filter.value != "" {
			args = append(args, filter.value)
			where += fmt.Sprintf(" AND %s=$%d", filter.column, len(args))
		}
	}
	if query.Search != "" {
		args = append(args, query.Search)
		where += fmt.Sprintf(" AND position(lower($%d) in lower(message)) > 0", len(args))
	}
	if query.Before > 0 {
		args = append(args, query.Before)
		where += fmt.Sprintf(" AND id<$%d", len(args))
	}
	if query.InstanceID != "" {
		args = append(args, query.InstanceID)
		where += fmt.Sprintf(" AND instance_id=$%d", len(args))
	}
	if query.UserID != "" {
		args = append(args, query.UserID)
		where += fmt.Sprintf(" AND owner_id=$%d", len(args))
	}
	return fmt.Sprintf(coreLogWindowSQL, where), args
}

// coreLogWindowSQL bounds each engine's indexed scan before merging the
// results. A busy engine must not consume the other engines' slots, and we
// should not rank the entire retained log history just to display a small
// recent window. The %s placeholder receives the predicate built by
// ListCoreLogs.
//
// The node and fleet covering indexes carry every selected column, so either
// scope can use an index-only scan. Falling back to heap fetches turns a bounded
// window into hundreds of random page reads per request.
const coreLogWindowSQL = `
		SELECT logs.id,logs.agent_id,logs.engine,logs.level,logs.message,logs.logged_at,logs.received_at,logs.instance_id,logs.service,logs.owner_id
		FROM unnest($1::text[]) AS selected(engine)
		CROSS JOIN LATERAL (
			SELECT id,agent_id,engine,level,message,logged_at,received_at,instance_id,service,owner_id
			FROM core_logs
			WHERE %s
			ORDER BY id DESC LIMIT $2
		) AS logs
		ORDER BY logs.id DESC`

func (s *Store) ListCoreLogs(ctx context.Context, query CoreLogQuery) ([]core.CoreLogEntry, error) {
	if _, scoped := requestConfigScope(ctx); scoped && (query.AgentID != "" || query.InstanceID != "" || query.UserID != "") {
		args := []any{}
		resources := coreLogResourcesSQL(ctx, &args)
		where := "true"
		for _, filter := range []struct{ column, value string }{{"agent_id", query.AgentID}, {"instance_id", query.InstanceID}, {"owner_id", query.UserID}} {
			if filter.value != "" {
				args = append(args, filter.value)
				where += fmt.Sprintf(" AND %s=$%d", filter.column, len(args))
			}
		}
		var allowed bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM (`+resources+`) resource WHERE `+where+`)`, args...).Scan(&allowed); err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrNotFound
		}
	}
	if query.Limit == 0 {
		query.Limit = 1000
	}
	if query.Limit < 1 || query.Limit > 2000 || query.Before < 0 {
		return nil, fmt.Errorf("%w: invalid core log query", ErrInvalid)
	}
	query.AgentID = strings.TrimSpace(query.AgentID)
	query.Level = normalizeCoreLogLevel(query.Level)
	query.Search = strings.TrimSpace(query.Search)
	if len(query.Search) > 120 || (query.Engine != "" && !query.Engine.Valid()) {
		return nil, fmt.Errorf("%w: invalid core log query", ErrInvalid)
	}
	engines := []string{string(core.EngineMihomo), string(core.EngineXray), string(core.EngineSingBox), string(core.EngineShadowsocksRust)}
	if query.Engine != "" {
		engines = []string{string(query.Engine)}
	}
	statement, args := scopedCoreLogWindowStatement(ctx, query, engines)
	rows, err := s.pool.Query(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.CoreLogEntry, 0, query.Limit*len(engines))
	for rows.Next() {
		var entry core.CoreLogEntry
		if err := rows.Scan(&entry.ID, &entry.AgentID, &entry.Engine, &entry.Level, &entry.Message, &entry.LoggedAt, &entry.ReceivedAt, &entry.InstanceID, &entry.Service, &entry.OwnerID); err != nil {
			return nil, err
		}
		entry.Message = sanitizeCoreLogMessage(entry.Message)
		result = append(result, entry)
	}
	return result, rows.Err()
}

func validCoreLogBatchID(value string) bool {
	if len(value) != 20 || !strings.HasPrefix(value, "log_") {
		return false
	}
	for _, character := range value[4:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// ansiControlPattern matches ANSI escape sequences (CSI) such as the color
// codes proxy cores emit. They must not reach the panel as visible `[36m`
// style text, even when an older Agent uploads them unchanged.
var ansiControlPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func sanitizeCoreLogMessage(raw string) string {
	message := strings.TrimSpace(strings.ToValidUTF8(raw, "�"))
	message = strings.ReplaceAll(message, "\x00", "�")
	// Stored rows have already been sanitized on upload. The regexp replacement
	// copies even a clean message, so a full window of long logs used to allocate
	// another two message-sized buffers per row on every poll. Legacy/colorized
	// rows still take the same cleanup path when an escape byte is present.
	if strings.ContainsRune(message, '\x1b') {
		message = ansiControlPattern.ReplaceAllString(message, "")
		message = strings.ReplaceAll(message, "\x1b", "")
	}
	return message
}

func normalizeCoreLogLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return "debug"
	case "info", "notice":
		return "info"
	case "warn", "warning":
		return "warning"
	case "err", "error":
		return "error"
	case "crit", "critical", "alert", "emerg", "emergency", "fatal", "panic":
		return "critical"
	case "":
		return ""
	default:
		return ""
	}
}

func normalizeCoreLogMinimumLevel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "off" {
		return value
	}
	return normalizeCoreLogLevel(value)
}

func coreLogLevelAtLeast(level, minimum string) bool {
	if minimum == "off" {
		return false
	}
	levelRank, levelOK := coreLogLevelRank(level)
	minimumRank, minimumOK := coreLogLevelRank(minimum)
	return levelOK && minimumOK && levelRank >= minimumRank
}

func coreLogLevelRank(level string) (int, bool) {
	switch level {
	case "debug":
		return 0, true
	case "info":
		return 1, true
	case "warning":
		return 2, true
	case "error":
		return 3, true
	case "critical":
		return 4, true
	default:
		return 0, false
	}
}
