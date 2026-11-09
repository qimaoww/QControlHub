package store

import (
	"context"
	"fmt"
)

// Start with authorized instances, then take bounded index windows. Scanning a
// fleet index until an authorized line happens to appear would read all of a
// busy foreign node's history for a quiet recipient.
func authorizedCoreLogWindowStatement(ctx context.Context, query CoreLogQuery, engines []string) (string, []any) {
	args := []any{engines, query.Limit}
	resources := coreLogResourcesSQL(ctx, &args)
	resourceWhere := "r.engine=selected.engine"
	for _, filter := range []struct{ column, value string }{{"agent_id", query.AgentID}, {"instance_id", query.InstanceID}, {"owner_id", query.UserID}} {
		if filter.value != "" {
			args = append(args, filter.value)
			resourceWhere += fmt.Sprintf(" AND r.%s=$%d", filter.column, len(args))
		}
	}
	where := `log.agent_id=r.agent_id AND log.engine=r.engine
 AND log.node_owner_id=r.node_owner_id AND log.owner_id=r.owner_id`
	if query.Level != "" {
		args = append(args, query.Level)
		where += fmt.Sprintf(" AND log.level=$%d", len(args))
	}
	if query.Search != "" {
		args = append(args, query.Search)
		where += fmt.Sprintf(" AND position(lower($%d) in lower(log.message))>0", len(args))
	}
	if query.Before > 0 {
		args = append(args, query.Before)
		where += fmt.Sprintf(" AND log.id<$%d", len(args))
	}
	// Read main and legacy lines in one bounded stream. An independent empty
	// legacy scan used the dense main stream's statistics and could walk all
	// node/engine history. Keep shared lines separate with an explicit exclusion
	// of those dense identities, so empty shares can use the instance index.
	columns := "log.id,log.agent_id,log.engine,log.level,log.message,log.logged_at,log.received_at,log.instance_id,log.service,log.owner_id"
	window := func(identity string) string {
		return "(SELECT " + columns + " FROM core_logs log WHERE " + where + " AND " + identity + " ORDER BY log.id DESC LIMIT $2)"
	}
	mainIdentity := "log.instance_id='main'"
	if query.InstanceID == "" {
		mainIdentity = "log.instance_id IN ('main','')"
	}
	streams := window("r.instance_id='main' AND " + mainIdentity)
	if query.InstanceID != "main" {
		streams += " UNION ALL " + window("r.instance_id<>'main' AND log.instance_id NOT IN ('main','') AND log.instance_id=r.instance_id")
	}
	return `WITH resources AS MATERIALIZED (` + resources + `)
 SELECT logs.id,logs.agent_id,logs.engine,logs.level,logs.message,logs.logged_at,logs.received_at,logs.instance_id,logs.service,logs.owner_id
 FROM unnest($1::text[]) selected(engine)
 CROSS JOIN LATERAL (
 SELECT recent.* FROM resources r
 CROSS JOIN LATERAL (` + streams + `) AS recent
 WHERE ` + resourceWhere + ` ORDER BY recent.id DESC LIMIT $2
 ) AS logs ORDER BY logs.id DESC`, args
}
