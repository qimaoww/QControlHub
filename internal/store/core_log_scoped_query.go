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
	where := `log.agent_id=r.agent_id AND log.engine=r.engine AND log.instance_id=identity.instance_id
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
	return `WITH resources AS MATERIALIZED (` + resources + `)
 SELECT logs.id,logs.agent_id,logs.engine,logs.level,logs.message,logs.logged_at,logs.received_at,logs.instance_id,logs.service,logs.owner_id
 FROM unnest($1::text[]) selected(engine)
 CROSS JOIN LATERAL (
 SELECT recent.* FROM resources r
 CROSS JOIN LATERAL unnest(CASE WHEN r.instance_id='main' AND ` + fmt.Sprintf("%t", query.InstanceID == "") + ` THEN ARRAY['main',''] ELSE ARRAY[r.instance_id] END) identity(instance_id)
 CROSS JOIN LATERAL (
 SELECT log.id,log.agent_id,log.engine,log.level,log.message,log.logged_at,log.received_at,log.instance_id,log.service,log.owner_id
 FROM core_logs log WHERE ` + where + ` ORDER BY log.id DESC LIMIT $2
 ) AS recent WHERE ` + resourceWhere + ` ORDER BY recent.id DESC LIMIT $2
 ) AS logs ORDER BY logs.id DESC`, args
}
