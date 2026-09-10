package store

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Kernel logs live in a table partitioned by receive day. Partitions are named
// core_logs_pYYYYMMDD and cover [day, day+1).
const (
	coreLogPartitionPrefix = "core_logs_p"
	legacyCoreLogsPrefix   = "core_logs_legacy_"
	// A partition is created this far ahead so an upload that arrives around
	// midnight (or from a node with a skewed clock) still finds a target.
	coreLogPartitionAheadDays = 3
	// The superseded heap table is kept only as a short-lived escape hatch
	// after the v49 upgrade; once this passes it is dropped for good.
	legacyCoreLogsGraceDays = 7
)

func coreLogPartitionName(day time.Time) string {
	return coreLogPartitionPrefix + day.UTC().Format("20060102")
}

// coreLogPartitionBoundPattern matches the FOR VALUES FROM (...) TO (...)
// rendering produced by pg_get_expr for a range partition.
var coreLogPartitionBoundPattern = regexp.MustCompile(`FROM \('([^']+)'\) TO \('([^']+)'\)`)

// EnsureCoreLogPartitions creates the daily partitions needed to accept logs at
// the given reference time and in the near future. Inserting into a partitioned
// table without a matching partition is an error, so this runs at startup, from
// the maintenance loop, and before a test writes dated rows.
func (s *Store) EnsureCoreLogPartitions(ctx context.Context, now time.Time) error {
	return s.ensureCoreLogPartitionsFrom(ctx, now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -1))
}

// ensureCoreLogPartitionsFrom creates partitions from the given day through the
// current window plus the upload look-ahead.
func (s *Store) ensureCoreLogPartitionsFrom(ctx context.Context, firstDay time.Time) error {
	if _, err := s.pool.Exec(ctx, `CREATE SEQUENCE IF NOT EXISTS core_logs_id_seq`); err != nil {
		return fmt.Errorf("ensure core log id sequence: %w", err)
	}
	partitioned, err := s.coreLogsIsPartitioned(ctx)
	if err != nil {
		return err
	}
	if !partitioned {
		// Older schema: the log table has not been migrated yet. Startup order
		// guarantees the migration ran first, so this is only reachable when a
		// migration failed; skip quietly instead of failing every write.
		return nil
	}
	lastDay := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, coreLogPartitionAheadDays)
	if firstDay.After(lastDay) {
		lastDay = firstDay
	}
	for day := firstDay; !day.After(lastDay); day = day.AddDate(0, 0, 1) {
		name := coreLogPartitionName(day)
		statement := fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s PARTITION OF core_logs FOR VALUES FROM (%s) TO (%s)`,
			pgx.Identifier{name}.Sanitize(),
			quoteCoreLogTimestamp(day),
			quoteCoreLogTimestamp(day.AddDate(0, 0, 1)))
		if _, err := s.pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("create core log partition %s: %w", name, err)
		}
	}
	return nil
}

// PruneCoreLogPartitions drops whole partitions whose entire range precedes the
// retention cutoff. This replaces the row-by-row DELETE that had to walk every
// index of the retained window.
func (s *Store) PruneCoreLogPartitions(ctx context.Context, olderThan time.Time) (int, error) {
	if err := s.EnsureCoreLogPartitions(ctx, time.Now().UTC()); err != nil {
		return 0, err
	}
	partitions, err := s.coreLogPartitions(ctx)
	if err != nil {
		return 0, err
	}
	dropped := 0
	cutoff := olderThan.UTC()
	for _, partition := range partitions {
		// Keep a partition when any part of its day is still inside retention.
		upper, err := coreLogPartitionUpperBound(ctx, s, partition)
		if err != nil {
			return dropped, err
		}
		if upper.After(cutoff) {
			continue
		}
		if _, err := s.pool.Exec(ctx, "DROP TABLE "+pgx.Identifier{partition}.Sanitize()); err != nil {
			return dropped, fmt.Errorf("drop core log partition %s: %w", partition, err)
		}
		dropped++
	}
	return dropped, nil
}

// DropLegacyCoreLogs removes the pre-partition heap table once its grace period
// has passed, releasing the disk its heap and indexes still occupy.
func (s *Store) DropLegacyCoreLogs(ctx context.Context, now time.Time) (int, error) {
	legacy, err := legacyCoreLogTables(ctx, s.pool)
	if err != nil {
		return 0, err
	}
	dropped := 0
	for _, name := range legacy {
		renamedAt, err := legacyCoreLogRenamedAt(name)
		if err != nil {
			continue
		}
		if now.UTC().Sub(renamedAt) < legacyCoreLogsGraceDays*24*time.Hour {
			continue
		}
		if _, err := s.pool.Exec(ctx, "DROP TABLE "+pgx.Identifier{name}.Sanitize()); err != nil {
			return dropped, fmt.Errorf("drop legacy core logs %s: %w", name, err)
		}
		dropped++
	}
	return dropped, nil
}

// dropLegacyCoreLogTables releases every set-aside copy immediately. It runs
// inside the migration transaction, where a previous interrupted attempt may
// have left one behind and the rename cannot overwrite an existing relation.
func dropLegacyCoreLogTables(ctx context.Context, tx pgx.Tx) error {
	legacy, err := legacyCoreLogTables(ctx, tx)
	if err != nil {
		return err
	}
	for _, name := range legacy {
		if _, err := tx.Exec(ctx, "DROP TABLE "+pgx.Identifier{name}.Sanitize()); err != nil {
			return fmt.Errorf("drop previous legacy core logs %s: %w", name, err)
		}
	}
	return nil
}

// legacyCoreLogTables lists the set-aside copies. Both the migration and the
// maintenance loop address them the same way, so they share one query.
func legacyCoreLogTables(ctx context.Context, queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) ([]string, error) {
	rows, err := queryer.Query(ctx, `
		SELECT class.relname
		FROM pg_class class
		JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
		WHERE class.relname LIKE $1 AND class.relkind = 'r'
		  AND namespace.nspname = current_schema()`, legacyCoreLogsPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("list legacy core logs: %w", err)
	}
	defer rows.Close()
	var legacy []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		legacy = append(legacy, name)
	}
	return legacy, rows.Err()
}

// legacyCoreLogRenamedAt reads the age of a set-aside copy out of its name. A
// relation that does not follow the naming scheme is not ours to drop.
func legacyCoreLogRenamedAt(name string) (time.Time, error) {
	suffix, found := strings.CutPrefix(name, legacyCoreLogsPrefix)
	if !found {
		return time.Time{}, fmt.Errorf("unexpected legacy core log name %q", name)
	}
	return time.Parse("20060102150405", suffix)
}

func (s *Store) coreLogsIsPartitioned(ctx context.Context) (bool, error) {
	var partitioned bool
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(bool_or(class.relkind = 'p'), false)
		FROM pg_class class
		JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
		WHERE class.relname = 'core_logs' AND namespace.nspname = current_schema()`).Scan(&partitioned); err != nil {
		return false, fmt.Errorf("inspect core log table kind: %w", err)
	}
	return partitioned, nil
}

func (s *Store) coreLogPartitions(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT child.relname
		FROM pg_inherits
		JOIN pg_class parent ON parent.oid = pg_inherits.inhparent
		JOIN pg_class child ON child.oid = pg_inherits.inhrelid
		JOIN pg_namespace namespace ON namespace.oid = parent.relnamespace
		WHERE parent.relname = 'core_logs' AND namespace.nspname = current_schema()
		ORDER BY child.relname`)
	if err != nil {
		return nil, fmt.Errorf("list core log partitions: %w", err)
	}
	defer rows.Close()
	var partitions []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		partitions = append(partitions, name)
	}
	return partitions, rows.Err()
}

// coreLogPartitionUpperBound reads the exclusive upper bound recorded for a
// partition, so pruning never has to guess from the partition name.
func coreLogPartitionUpperBound(ctx context.Context, s *Store, partition string) (time.Time, error) {
	var expression string
	if err := s.pool.QueryRow(ctx, `
		SELECT pg_get_expr(relpartbound, oid)
		FROM pg_class
		WHERE oid = to_regclass($1) AND relispartition`, partition).Scan(&expression); err != nil {
		return time.Time{}, fmt.Errorf("read partition bound for %s: %w", partition, err)
	}
	match := coreLogPartitionBoundPattern.FindStringSubmatch(expression)
	if match == nil {
		return time.Time{}, fmt.Errorf("unexpected partition bound %q for %s", expression, partition)
	}
	value := match[2]
	for _, layout := range []string{
		"2006-01-02 15:04:05-07",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse partition bound %q for %s", value, partition)
}

// quoteCoreLogTimestamp renders a partition bound. Partition DDL cannot take
// bind parameters, so the value is formatted in UTC with an explicit zone and
// wrapped in quotes.
func quoteCoreLogTimestamp(value time.Time) string {
	return "'" + value.UTC().Format("2006-01-02 15:04:05+00") + "'"
}
