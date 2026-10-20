import {installAgentFixture} from "/assets/browser/agent-fixture.mjs";

const params = new URLSearchParams(location.search);
const mode = params.get("mode") || "config-layout";
const scenario = params.get("state") || "";
const longText = params.get("long") === "1";

window.reviewScenario = installAgentFixture(mode);

// State fixtures let every rendered phase be reviewed with the same layout,
// typography and theme rules as production pages.
const fixtureAgents = window.reviewScenario.testAPI.agents;
const primaryAgent = fixtureAgents.find(agent => agent.id === "alpha") || fixtureAgents[0];
if (primaryAgent) {
  const engineKey = params.get("engine") || "xray";
  const engineRuntime = {...(primaryAgent.runtime?.[engineKey] || {})};
  if (scenario === "offline") primaryAgent.status = "offline";
  if (scenario === "unsupported") primaryAgent.runtime = {...primaryAgent.runtime, [engineKey]: {
    ...engineRuntime, installed: false, existing_config_available: true,
    existing_config_unsupported_reason: "现有系统服务配置包含无法自动迁移的字段：tun.stack、experimental.cache_file",
  }};
  // An older Agent cannot tell the managed file from a system service one, so
  // the workspace has no managed config to open and must ask for an upgrade.
  if (scenario === "upgrade") {
    primaryAgent.features = (primaryAgent.features || []).filter(item => item !== "managed-config-read-v1");
    primaryAgent.runtime = {...primaryAgent.runtime, [engineKey]: {...engineRuntime, existing_config_available: true}};
  }
  if (scenario === "import") primaryAgent.runtime = {...primaryAgent.runtime, [engineKey]: {...engineRuntime, installed: false, existing_config_available: true}};
}
if (scenario === "empty") window.reviewScenario.testAPI.agents = [];

if (longText) {
  const agents = window.reviewScenario.testAPI.agents;
  const target = agents.find(agent => agent.id === "alpha") || agents[0];
  if (target) target.name = "香港 · 九龙 · 生产环境核心出口节点 · 超长名称展示验证";
}

function longConfig(engine) {
  const tag = "very-long-object-tag-name-used-for-layout-verification";
  if (engine === "mihomo") {
    const lines = ["log-level: info", "listeners:"];
    for (let index = 1; index <= 6; index += 1) {
      lines.push(`  - name: ${tag}-${index}`, "    type: socks", `    port: ${10800 + index}`, "    listen: 0.0.0.0");
    }
    lines.push("rules:");
    for (let index = 1; index <= 40; index += 1) lines.push(`  - DOMAIN-SUFFIX,service-${index}.example.internal,DIRECT`);
    lines.push("  - MATCH,DIRECT");
    return `${lines.join("\n")}\n`;
  }
  const document = {
    log: {loglevel: "warning", access: "none", error: "logs/error.log"},
    inbounds: Array.from({length: 6}, (_, index) => ({
      tag: `${tag}-${index + 1}`,
      listen: "0.0.0.0",
      port: 10800 + index,
      protocol: "socks",
      settings: {auth: "noauth", udp: true, ip: "127.0.0.1"},
      streamSettings: {network: "tcp", security: "none", tcpSettings: {header: {type: "none"}}},
      sniffing: {enabled: false, destOverride: ["http", "tls"]},
    })),
    outbounds: Array.from({length: 4}, (_, index) => ({
      tag: `${tag}-outbound-${index + 1}`,
      protocol: "freedom",
      settings: {domainStrategy: "AsIs", redirect: `1.1.1.${index + 1}:53`},
      streamSettings: {network: "tcp", security: "none"},
    })),
    routing: {
      domainStrategy: "IPIfNonMatch",
      rules: Array.from({length: 40}, (_, index) => ({
        type: "field",
        domain: [`domain:service-${index + 1}.example.internal`],
        outboundTag: `${tag}-outbound-${(index % 4) + 1}`,
      })),
    },
  };
  return `${JSON.stringify(document, null, 2)}\n`;
}

let opsWorkspace = null;
if (scenario || longText || params.get("ops") === "1") {
  const inner = window.fetch.bind(window);
  window.fetch = async (input, options = {}) => {
    const url = typeof input === "string" ? input : input?.url || String(input);
    const method = String(options.method || (typeof input === "string" ? "GET" : input?.method) || "GET").toUpperCase();
    const segments = new URL(url, location.href).pathname.split("/").filter(Boolean);
    const isWorkspaceRead = segments[segments.length - 1] === "workspace" && method === "GET";
    const isSnapshot = segments[segments.length - 1] === "config-snapshot" && method === "GET";

    // Writable workspace for operation walkthroughs (?ops=1): create, edit,
    // delete, save and deploy share one in-memory configuration.
    if (params.get("ops") === "1") {
      const opsJson = payload => new Response(JSON.stringify(payload), {status: 200, headers: {"Content-Type": "application/json"}});
      const persistOps = () => { try { sessionStorage.setItem("qch-ops-workspace", JSON.stringify(opsWorkspace)); } catch { /* private mode */ } };
      try { const stored = sessionStorage.getItem("qch-ops-workspace"); if (stored && stored !== "null") opsWorkspace = JSON.parse(stored); } catch { /* ignore */ }
      const taskFails = params.get("task") === "fail";
      const isPreview = method === "POST" && segments[segments.length - 1] === "preview";
      const isConfigSave = method === "PUT" && segments.includes("configs");
      const isInboundMutation = method === "POST" && segments[segments.length - 1] === "server-inbounds";
      if ((isWorkspaceRead || isPreview) && !opsWorkspace) {
        const payload = await (await inner(input, options)).json();
        const agent = window.reviewScenario.testAPI.agents.find(item => item.id === "alpha");
        const parsedConfig = JSON.parse(payload.config.content || "{}");
        payload.inbounds = (parsedConfig.inbounds || []).map(item => ({tag: item.tag, port: item.port, protocol: item.protocol}));
        payload.inbound_targets = payload.inbounds;
        opsWorkspace = {config: payload.config, agent, protocols: [{key: "vless", label: "VLESS", default_port: 443}, {key: "vmess", label: "VMess", default_port: 443}, {key: "trojan", label: "Trojan", default_port: 443}, {key: "shadowsocks", label: "Shadowsocks", default_port: 8388}, {key: "socks", label: "SOCKS", default_port: 1080}, {key: "http", label: "HTTP", default_port: 8080}], reality_presets: ["www.amazon.com"], common_fields: [], catalog: {fields: [], protocols: [{key: "vless", label: "VLESS", default_port: 443}, {key: "vmess", label: "VMess", default_port: 443}, {key: "trojan", label: "Trojan", default_port: 443}, {key: "shadowsocks", label: "Shadowsocks", default_port: 8388}, {key: "socks", label: "SOCKS", default_port: 1080}, {key: "http", label: "HTTP", default_port: 8080}]}};
        persistOps();
      }
      if (method === "POST" && segments[0] === "tasks" && segments.length === 1) {
        const response = await inner(input, options);
        const payload = await response.clone().json();
        const fails = taskFails || (params.get("task") === "fail-once" && !window.__opsFailedOnce);
        if (fails) window.__opsFailedOnce = true;
        window.__opsTaskStatus = window.__opsTaskStatus || {};
        window.__opsTaskStatus[payload.id] = fails ? "failed" : "succeeded";
        payload.status = fails ? "failed" : "succeeded";
        payload.error = fails ? "listen tcp 0.0.0.0:24001: bind: address already in use" : "";
        return new Response(JSON.stringify(payload), {status: response.status, headers: {"Content-Type": "application/json"}});
      }
      if (method === "GET" && segments[0] === "tasks" && segments.length === 2) {
        const id = segments[1];
        const status = (window.__opsTaskStatus || {})[id] || "succeeded";
        return opsJson({id, action: "validate", status, error: status === "failed" ? "listen tcp 0.0.0.0:24001: bind: address already in use" : "", config_version: (opsWorkspace && opsWorkspace.config.version) || 0});
      }
      if (isWorkspaceRead) return opsJson(opsWorkspace);
      if (isSnapshot && opsWorkspace) return opsJson({content: opsWorkspace.config.content});
      if (isPreview) {
        const body = JSON.parse(options.body);
        return opsJson({config: {...opsWorkspace.config, content: body.content}, agent: opsWorkspace.agent, protocols: opsWorkspace.protocols, reality_presets: opsWorkspace.reality_presets, common_fields: opsWorkspace.common_fields, catalog: opsWorkspace.catalog, inbounds: JSON.parse(body.content).inbounds || []});
      }
      if (isConfigSave) {
        if (params.get("delay")) await new Promise(resolve => setTimeout(resolve, Number(params.get("delay")) * 1000));
        const body = JSON.parse(options.body);
        opsWorkspace = {...opsWorkspace, config: {...opsWorkspace.config, ...body, version: opsWorkspace.config.version + 1}};
        persistOps();
        return opsJson(opsWorkspace.config);
      }
      if (method === "POST" && segments[segments.length - 1] === "plans") {
        const body = JSON.parse(options.body);
        const protocol = opsWorkspace.protocols.find(item => item.key === body.protocol) || opsWorkspace.protocols[0];
        return opsJson({protocol: protocol.key, tag: "", listen: "0.0.0.0", port: protocol.default_port, transport: "raw", username: "", credential: ""});
      }
      if (isInboundMutation) {
        if (params.get("delay")) await new Promise(resolve => setTimeout(resolve, Number(params.get("delay")) * 1000));
        const body = JSON.parse(options.body);
        const doc = JSON.parse(opsWorkspace.config.content);
        const list = Array.isArray(doc.inbounds) ? doc.inbounds : [];
        if (body.operation === "delete") doc.inbounds = list.filter(item => item.tag !== body.original_tag);
        else if (body.operation === "add") doc.inbounds = [...list, body.input];
        else doc.inbounds = list.map(item => item.tag === body.original_tag ? {...item, ...body.input} : item);
        const content = JSON.stringify(doc, null, 2) + String.fromCharCode(10);
        opsWorkspace = {...opsWorkspace, config: {...opsWorkspace.config, content, version: opsWorkspace.config.version + 1}};
        opsWorkspace.inbounds = (doc.inbounds || []).map(item => ({tag: item.tag, port: item.port, protocol: item.protocol}));
        opsWorkspace.inbound_targets = opsWorkspace.inbounds;
        persistOps();
        const task = {id: "layout-ops-" + Date.now(), action: body.intent, status: taskFails ? "failed" : "succeeded",
          config_version: opsWorkspace.config.version, engine: params.get("engine") || "xray", agent_id: "alpha",
          error: taskFails ? "listen tcp 0.0.0.0:23001: bind: address already in use" : ""};
        window.reviewScenario.testAPI.layoutTasks.set(task.id, task);
        return opsJson({config: opsWorkspace.config, task});
      }
    }

    if (scenario === "loading" && isWorkspaceRead) return new Promise(() => {});
    if (scenario === "error" && isWorkspaceRead)
      return new Response(JSON.stringify({error: "节点存储层读取超时，请稍后重新读取"}), {status: 504, headers: {"Content-Type": "application/json"}});
    // English operator log: the workspace must pair it with a Chinese cause.
    if (scenario === "error-en" && isWorkspaceRead)
      return new Response(JSON.stringify({error: "read workspace: dial tcp 10.0.4.21:7801: connect: connection refused"}), {status: 502, headers: {"Content-Type": "application/json"}});
    // A node that is offline cannot be read at all, so the phase has to come
    // from the agent state rather than from a generic read failure.
    if (scenario === "offline" && isWorkspaceRead)
      return new Response(JSON.stringify({error: "节点离线，暂时无法读取配置"}), {status: 503, headers: {"Content-Type": "application/json"}});
    const tail = String(segments[segments.length - 1] || "");
    if (scenario === "slow" && method !== "GET") await new Promise(resolve => setTimeout(resolve, 8000));
    const response = await inner(input, options);
    if (scenario === "fail" && tail === "tasks" && method === "POST") {
      // Only operator actions fail; configuration reads stay healthy so the
      // editor and its retry affordances remain visible.
      const payload = await response.clone().json();
      if (payload && ["deploy", "validate", "import-existing"].includes(payload.action))
        return new Response(
          JSON.stringify({...payload, status: "failed", error: "listen tcp 0.0.0.0:23001: bind: address already in use"}),
          {status: 200, headers: {"Content-Type": "application/json"}},
        );
      return response;
    }
    if (scenario === "upgrade" && isWorkspaceRead) {
      const payload = await response.clone().json();
      return new Response(JSON.stringify({...payload, config: null}), {status: 200, headers: {"Content-Type": "application/json"}});
    }
    if (!longText || (!isWorkspaceRead && !isSnapshot)) return response;
    const payload = await response.clone().json();
    const engine = isWorkspaceRead ? segments[segments.length - 2] : params.get("engine") || "xray";
    const content = longConfig(engine);
    if (isWorkspaceRead) payload.config = {...payload.config, name: "香港 · 九龙 · 生产环境 · 全量转发节点配置（2026 Q4 归档副本）", content};
    else payload.content = content;
    return new Response(JSON.stringify(payload), {status: 200, headers: {"Content-Type": "application/json"}});
  };
}

if (params.get("kind") === "form") {
  const {createShellAppearance} = await import("/assets/modules/shell-appearance.js");
  createShellAppearance({data: {}}).applyTheme();
  const {createConfigFixture} = await import("/assets/browser/config-fixture.mjs");
  window.reviewConfig = await createConfigFixture(params.get("engine") || "xray", {protocol: params.get("protocol") || "vless"});
} else {
  location.hash = `#live-config?agent=alpha&engine=${params.get("engine") || "xray"}`;
  await import("/assets/app.js");
}
document.documentElement.dataset.reviewReady = "true";
