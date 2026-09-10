// Only node/core identity belongs in the URL; never serialize preset secrets.
export function presetRoute(data) {
  return `#agent-config?${new URLSearchParams({agent:data.agentId, engine:data.engine})}`;
}

export function readPresetRoute(hash) {
  if (!hash.startsWith("#agent-config?")) return null;
  const params = new URLSearchParams(hash.slice(hash.indexOf("?") + 1));
  return {agentId:params.get("agent") || "", engine:params.get("engine") || ""};
}
