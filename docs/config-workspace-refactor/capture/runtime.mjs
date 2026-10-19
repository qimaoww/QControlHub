import {installAgentFixture} from "/assets/browser/agent-fixture.mjs";

const params = new URLSearchParams(location.search);
const mode = params.get("mode") || "config-layout";
const scenario = params.get("state") || "";
const longText = params.get("long") === "1";

window.reviewScenario = installAgentFixture(mode);

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

if (scenario || longText) {
  const inner = window.fetch.bind(window);
  window.fetch = async (input, options = {}) => {
    const url = typeof input === "string" ? input : input?.url || String(input);
    const method = String(options.method || (typeof input === "string" ? "GET" : input?.method) || "GET").toUpperCase();
    const segments = new URL(url, location.href).pathname.split("/").filter(Boolean);
    const isWorkspaceRead = segments[segments.length - 1] === "workspace" && method === "GET";
    const isSnapshot = segments[segments.length - 1] === "config-snapshot" && method === "GET";
    if (scenario === "loading" && isWorkspaceRead) return new Promise(() => {});
    if (scenario === "error" && isWorkspaceRead)
      return new Response(JSON.stringify({error: "节点存储层读取超时，请稍后重新读取"}), {status: 504, headers: {"Content-Type": "application/json"}});
    const response = await inner(input, options);
    const tail = String(segments[segments.length - 1] || "");
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
