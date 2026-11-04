// Only node/core identity belongs in the URL; never serialize preset secrets.
export function presetRoute(data) {
  const params = new URLSearchParams({agent:data.agentId, engine:data.engine});
  const source = data.sourceMode || data.liveConfigSource;
  if (["personal", "managed", "import"].includes(source)) params.set("source", source);
  return `#live-config?${params}`;
}

export function readPresetRoute(hash) {
  if (!hash.startsWith("#agent-config?") && !hash.startsWith("#live-config?")) return null;
  const params = new URLSearchParams(hash.slice(hash.indexOf("?") + 1));
  return {agentId:params.get("agent") || "", engine:params.get("engine") || "",
    ...(["personal", "managed", "import"].includes(params.get("source")) ? {liveConfigSource:params.get("source")} : {})};
}
