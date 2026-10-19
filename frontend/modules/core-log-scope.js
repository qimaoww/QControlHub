// The source directory is already permission-filtered by the server.
export function coreLogScopeKey(filters = {}) {
  const key = [filters.agent_id || "", Number(filters.limit || 1000)];
  if (filters.instance_id || filters.user_id) key.push(filters.instance_id || "", filters.user_id || "");
  return JSON.stringify(key);
}

export function normalizeCoreLogScope(filters, sources) {
  const next = { ...filters };
  if (next.agent_id && !sources.some(source => source.agent_id === next.agent_id)) next.agent_id = "";
  const available = sources.filter(source => !next.agent_id || source.agent_id === next.agent_id);
  if (next.instance_id && !available.some(source => source.instance_id === next.instance_id)) next.instance_id = "";
  if (next.user_id && !available.some(source => source.owner_id === next.user_id)) next.user_id = "";
  if (next.engine && !available.some(source => source.engine === next.engine && (!next.instance_id || source.instance_id === next.instance_id))) next.engine = "";
  return next;
}

export function coreLogSourceAgents(sources) {
  return [...new Map(sources.map(source => [source.agent_id, {
    id: source.agent_id, name: source.name, features: source.features,
    status: source.status === "offline" ? "offline" : "online",
  }])).values()];
}
