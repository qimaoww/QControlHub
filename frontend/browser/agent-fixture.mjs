import { accountStorage, setStorageAccount } from "../modules/account-storage.js";
import { assert } from "./assertions.mjs";

export function installAgentFixture(mode) {
const tcpRules = [
  { key: "net.ipv4.tcp_congestion_control", label: "拥塞控制算法", choices: ["bbr", "bbr2", "bbr3", "cubic", "reno"] },
  { key: "net.core.default_qdisc", label: "默认队列算法", choices: ["fq", "fq_codel", "fq_pie", "pfifo_fast", "sfq", "cake"] },
  { key: "net.ipv4.tcp_rmem", label: "TCP 接收缓冲区：最小 / 默认 / 最大（字节）", tuple: true, min: 1, max: 1073741824 },
  { key: "net.ipv4.tcp_wmem", label: "TCP 发送缓冲区：最小 / 默认 / 最大（字节）", tuple: true, min: 1, max: 1073741824 },
  { key: "net.core.rmem_max", label: "接收缓冲区上限（字节）", min: 4096, max: 1073741824 },
  { key: "net.core.wmem_max", label: "发送缓冲区上限（字节）", min: 4096, max: 1073741824 },
  { key: "net.core.somaxconn", label: "监听连接队列上限", min: 128, max: 65535 },
  { key: "net.core.netdev_max_backlog", label: "网卡接收积压队列上限", min: 64, max: 1000000 },
  { key: "net.ipv4.tcp_max_syn_backlog", label: "TCP SYN 队列上限", min: 128, max: 1000000 },
  { key: "net.ipv4.tcp_mtu_probing", label: "MTU 探测（0 / 1 / 2）", max: 2 },
  { key: "net.ipv4.tcp_ecn", label: "ECN（0 / 1 / 2）", max: 2 },
  { key: "net.ipv4.tcp_fastopen", label: "TCP Fast Open（0 / 1 / 2 / 3）", max: 3 },
  { key: "net.ipv4.tcp_sack", label: "SACK（0 / 1）", max: 1 },
  { key: "net.ipv4.tcp_window_scaling", label: "窗口缩放（0 / 1）", max: 1 },
];
const onlineAgent = (id, features = ["agent-self-upgrade-v1"]) => ({
  id,
  can_manage: true,
  name: id.toUpperCase(),
  os: "linux",
  arch: "amd64",
  status: "online",
  version: "1.2.3",
  capabilities: ["mihomo", "sing-box"],
  supported_capabilities: ["mihomo", "xray", "sing-box", "ss-rust"],
  features,
  labels: {},
  metrics: {},
  runtime: {
    mihomo: { installed: true, service_status: "running" },
    "sing-box": { installed: false, service_status: "unknown" },
  },
  last_seen: "2026-08-24T00:00:00Z",
  enrolled_at: "2026-08-24T00:00:00Z",
  enrollment_command_available: id === "alpha",
});
const populatedAgents = [
  {
    ...onlineAgent("alpha"),
    labels: { komari_uuid: "komari-alpha" },
    metrics: {
      cpu_available: true,
      cpu_percent: 18.6,
      memory_available: true,
      memory_used_bytes: 1288490189,
      memory_total_bytes: 4294967296,
      disk_available: true,
      disk_used_bytes: 17179869184,
      disk_total_bytes: 53687091200,
      network_available: true,
      network_rx_bps: 1887437,
      network_tx_bps: 645120,
      network_rx_bytes: 9878424781,
      network_tx_bytes: 4617089843,
      public_ipv4: "8.8.8.8",
      public_ipv4_source: "agent-config",
      collected_at: "2026-08-30T09:00:00Z",
    },
  },
  // The control plane resolves automatic GeoIP in the node list, so this node
  // carries the code and must not need a per-node lookup request.
  { ...onlineAgent("bravo"), region_code: "SG" },
  { ...onlineAgent("charlie"), status: "offline" },
  // The control plane inlines the Komari resource it already cached, so this
  // node renders its monthly traffic without a request of its own.
  {
    ...onlineAgent("delta", []),
    labels: { komari_uuid: "komari-delta" },
    komari: {
      uuid: "komari-delta",
      name: "Osaka edge-02",
      billing_cycle: 30,
      traffic_limit: 21474836480,
      traffic_limit_type: "sum",
      traffic_used: 5368709120,
      traffic_used_available: true,
      traffic_reset_day: 1,
    },
  },
];

const testAPI = {
  calls: [],
  pendingTasks: [],
  enrollmentFailure: false,
  renameFailure: false,
  renameGate: null,
  profileNames: {},
  profileModes: {},
  profileAddresses: {},
  profileSaves: [],
  profileSaveFailure: false,
  profileSaveGate: null,
  deployments: [],
  savedConfigs: [],
  agentsFailure: false,
  enrollmentRecords: [
    {
      id: "enr-alpha",
      name: "alpha",
      reusable: true,
      used_count: 1,
      max_uses: 0,
      command_available: true,
    },
  ],
  taskMode: "immediate",
  agents: populatedAgents,
};
window.__agentsBrowserTestAPI = testAPI;
if (mode.startsWith("client-order")) {
  location.hash = "#client-access";
  testAPI.clientAccessEntries = [
    ["bravo", "mihomo"], ["alpha", "xray"], ["charlie", "xray"], ["alpha", "mihomo"],
  ].map(([agent_id, engine]) => ({
    agent_id, agent_name: agent_id.toUpperCase(), engine,
    address: `${agent_id}.example.test`, source: "test",
    profiles: [20002, 20001].map(port => ({
      tag: `${agent_id}-${port}`, port, protocol: "test",
      profile: { format: "URI", uri: `test-${agent_id}-${port}`, fields: [] },
    })),
  }));
}
if (mode.startsWith("enrollment")) testAPI.enrollmentRecords = [];
if (mode.startsWith("users-layout")) {
  location.hash = "#users";
  testAPI.users = [
    { id: "cdn", username: "cdn", role: "user" },
    { id: "new-user", username: "new-user", display_name: "未分配用户", role: "user" },
    { id: "admin", username: "admin", role: "admin" },
  ];
  testAPI.agents = populatedAgents.map((agent, index) => ({
    ...agent,
    name: ["Catixs HK", "Tokyo Edge", `Frankfurt-${"long-node-name-".repeat(6)}`, "自有节点"][index],
    features: ["shared-traffic-v1", "shared-engines-v1", "independent-egress-v1"],
    capabilities: ["mihomo", "xray", "sing-box", "ss-rust"],
  }));
  testAPI.userAccess = {
    cdn: { isolated: true, revision: 4, owned_agent_ids: ["delta"], shares: [
      { id: "shr_alpha", agent_id: "alpha", enabled: true, status: "pending", engines: ["mihomo", "xray", "sing-box", "ss-rust"], ports: [], limit_bytes: 0, used_bytes: 0 },
      { id: "shr_bravo", agent_id: "bravo", enabled: true, status: "accepted", engines: ["mihomo", "xray"], ports: [31001, 31002], limit_bytes: 50 * 1024 ** 3, used_bytes: 12 * 1024 ** 3 },
    ] },
    "new-user": { isolated: true, revision: 1, owned_agent_ids: [], shares: [] },
    admin: { isolated: false, revision: 1, owned_agent_ids: [], shares: [] },
  };
  testAPI.allocationWrites = [];
}
if (mode === "shared-node" || mode === "shared-node-mobile") {
  testAPI.agents = [
    { ...populatedAgents[0], can_manage: false, capabilities: ["mihomo"], supported_capabilities: ["mihomo"], shared_engines: ["mihomo"], labels: {}, enrollment_command_available: false },
    populatedAgents[1],
  ];
}
if (mode === "logs-restore") {
  // Seed the browser store before the application boots so the log page has
  // to restore the previous session's node scope, filters, and live switch.
  testAPI.agents = populatedAgents.map((agent) => ({
    ...agent,
    features: [...(agent.features || []), "core-logs-v1", "core-log-status-v1"],
    runtime: { ...agent.runtime, xray: { installed: true, core_log_status: "active" } },
  }));
  setStorageAccount({ role: "admin" });
  accountStorage.setItem("qcontrolhub:core-log-preferences", JSON.stringify({
    agent_id: "bravo",
    engine: "xray",
    level: "warning",
    q: "pressure entry 1999",
    limit: 2000,
    auto_refresh: false,
  }));
}
if (mode === "traffic-layout") {
  location.hash = "#traffic";
  setStorageAccount({ role: "admin" });
  accountStorage.setItem("qcontrolhub:node-card-order", JSON.stringify(["alpha", "delta", "bravo", "charlie"]));
  testAPI.trafficCandidates = [
    { agent_id: "alpha", name: "误删的 VLESS 入口", engine: "xray", port: 443, protocol: "both", kind: "deleted" },
    { agent_id: "bravo", name: "新增的香港入口", engine: "sing-box", port: 9443, protocol: "tcp", kind: "new" },
    { agent_id: "alpha", name: "暂不恢复的端口", engine: "mihomo", port: 10443, protocol: "both", kind: "deleted" },
  ];
  testAPI.agents = populatedAgents.map(agent=>({...agent,features:[...(agent.features||[]),"port-traffic-v1"]}));
  const issue = "; dual accounting unavailable: single-protocol policy uses listener-only accounting; dual accounting requires TCP+UDP because core counters and outbound marks are shared";
  testAPI.trafficPolicies = ["xray", "sing-box", "mihomo", "ss-rust"].map((engine, index) => ({
    id: `trf_layout_${index}`, agent_id: testAPI.agents[index % testAPI.agents.length].id,
    name: ["VLESS-REALITY-8443", "vless-in-443", "香港入口", "SS-2022"][index], engine,
    port: 8443 + index, protocol: index < 2 ? "tcp" : "both", cycle: "monthly",
    cycle_anchor: "2026-09-01", period_start: "2026-09-01", period_end: "2026-10-01",
    quota_enabled: false, monitoring_enabled: true,
    received_bytes: 3e9, sent_bytes: index > 1 ? 3e9 : 31.6e9, used_bytes: index > 1 ? 6e9 : 34.6e9,
    receive_bps: 7000, send_bps: 137000, enforcement_available: index !== 1,
    enforcement_error: index === 0 ? issue : index === 1 ? "dual accounting unavailable: unknown route target api" : "",
    last_reported_at: new Date().toISOString(), last_collected_at: new Date().toISOString(),
    ...(index > 1 ? {accounting: {source: "nft-dual", client_received: 1e9, client_sent: 2e9, target_received: 2e9, target_sent: 1e9}} : {}),
  }));
}
const layoutConfig = engine => engine === "mihomo" ? "log-level: info\nlisteners:\n  - name: socks-in\n    type: socks\n    port: 1080\n    listen: 0.0.0.0\nrules:\n  - MATCH,DIRECT\n" : engine === "ss-rust" ? JSON.stringify({server:"0.0.0.0",server_port:8388,method:"aes-256-gcm",password:"demo-not-a-real-secret",mode:"tcp_and_udp"},null,2) : JSON.stringify({log:{loglevel:"warning"},dns:{servers:["1.1.1.1","8.8.8.8"]},inbounds:[{tag:"socks-in",listen:"127.0.0.1",...(engine==="xray"?{port:1080,protocol:"socks",settings:{auth:"noauth",udp:true}}:{listen_port:1080,type:"socks"})},{tag:"http-in",listen:"127.0.0.1",...(engine==="xray"?{port:8080,protocol:"http"}:{listen_port:8080,type:"http"})}],outbounds:[{tag:"direct",...(engine==="xray"?{protocol:"freedom"}:{type:"direct"})}],...(engine==="xray"?{routing:{domainStrategy:"AsIs",rules:[]}}:{route:{final:"direct"}})},null,2);
if (mode.startsWith("capabilities-settings")) {
  location.hash = "#settings-engines";
  testAPI.settings = { panel_name: "QControlHub Browser Smoke", panel_description: "可信远程编排", revision: 1, ui_font_scale: 100, default_agent_engines: ["mihomo", "sing-box"] };
}
if (mode === "config-layout") {
  testAPI.agents = populatedAgents.map((agent,index)=>({...agent,name:["香港 · HK-01","新加坡 · SG-02","东京 · JP-03","美国 · US-04"][index],features:["managed-config-read-v1","config-files-v1"],capabilities:["xray","sing-box","mihomo","ss-rust"],runtime:Object.fromEntries(["xray","sing-box","mihomo","ss-rust"].map(engine=>[engine,{installed:true,service_status:"running",version:{xray:"26.3.27","sing-box":"1.13.19",mihomo:"1.19.0","ss-rust":"1.25.0"}[engine]}]))}));
  testAPI.layoutTasks = new Map();
  location.hash = "#live-config";
}
if (mode.startsWith("bbr")) {
  setStorageAccount({ role: mode === "bbr-readonly" ? "readonly" : mode === "bbr-writeonly" ? "user" : "admin" });
  accountStorage.setItem("qcontrolhub:node-card-order", JSON.stringify(["alpha", "delta", "bravo", "charlie"]));
  testAPI.tcpTasks = [];
  testAPI.tcpMutations = [];
  testAPI.agents = populatedAgents.map((agent, index) => ({
    ...agent, features: index === 3 ? [] : ["system-bbr-v1"],
    metrics: { bbr: {
      available: true, collected_at: new Date().toISOString(), kernel_release: "6.12.46-amd64",
      congestion_control: index === 1 ? "cubic" : "bbr", default_qdisc: "fq_codel",
      available_algorithms: ["reno", "cubic", "bbr"], persistence: "unmanaged",
      parameters: Object.fromEntries(tcpRules.map((rule) => [rule.key,
        rule.key === "net.ipv4.tcp_congestion_control" ? (index === 1 ? "cubic" : "bbr") :
        rule.key === "net.core.default_qdisc" ? "fq_codel" : rule.tuple ? "4096 131072 16777216" :
        rule.max < 4 ? "1" : "4096",
      ])),
      qdiscs: [{ device: "eth0", kind: "mq", root: true }, { device: "eth0", kind: "fq_codel", parent: "1:1", handle: "0:" }],
    } },
  }));
  location.hash = "#system-bbr";
}

const json = (value, status = 200) =>
  new Response(value === null ? null : JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const fixtureFetch = window.fetch.bind(window);
window.fetch = async (input, options = {}) => {
  if (input === "/assets/preset-plans.json") return fixtureFetch(input, options);
  const url = new URL(input instanceof Request ? input.url : input, location.href);
  const path = url.pathname.replace(/^\/api\/v1/, "");
  const method = String(options.method || (input instanceof Request ? input.method : "GET")).toUpperCase();
  testAPI.calls.push({ method, path, query: url.search });
  if (method === "GET" && path === "/agent-access") return json({ isolated: false, revision: 1, shares: [] });
  if (mode === "traffic-layout") {
    if (path === "/traffic-policies") return json(testAPI.trafficPolicies);
    if (path === "/traffic-endpoints") return json([]);
    if (path === "/traffic-endpoints/sync") {
      if (method === "GET") {
        if (testAPI.trafficPreviewGate) await testAPI.trafficPreviewGate;
        if (testAPI.trafficPreviewUnauthorized) return json({ error: "session expired" }, 401);
        if (testAPI.trafficPreviewFailure) return json({ error: "preview unavailable" }, 500);
        return json({ candidates: testAPI.trafficCandidates });
      }
      const body = JSON.parse(options.body);
      testAPI.trafficSelection = body.selections;
      if (testAPI.trafficSyncGate) await testAPI.trafficSyncGate;
      if (testAPI.trafficSyncFailure) return json({ error: "sync unavailable" }, 500);
      testAPI.trafficCandidates = testAPI.trafficCandidates.filter(candidate => {
        if (!body.selections.some(item => item.agent_id === candidate.agent_id && item.port === candidate.port)) return true;
        testAPI.trafficPolicies.push({ ...candidate, id: `trf_sync_${candidate.port}`, quota_enabled: false, monitoring_enabled: true });
        return false;
      });
      return json({ changed_agents: [...new Set(body.selections.map(item => item.agent_id))] });
    }
  }
  if (mode === "config-layout") {
    if (path === "/access-controls") return json(testAPI.agents.flatMap(agent => ["xray","sing-box"].flatMap(engine => ["socks-in","http-in"].map((tag,i) => ({agent_id:agent.id,agent_name:agent.name,agent_status:agent.status,engine,tag,port:i?8080:1080,config_version:8,block_mainland_destination:!i,block_mainland_source:Boolean(i)})))));
    if (path === "/settings") return json({panel_name:"QControlHub"});
    if (path.endsWith("/workspace")) {
      const engine = path.split("/")[4];
      return json({config:{id:"cfg-layout",version:8,name:"节点实际配置",content:layoutConfig(engine)}});
    }
    if (path === "/tasks" && method === "POST") {
      const input = JSON.parse(options.body);
      const task = {id:`layout-${testAPI.layoutTasks.size}`,status:"succeeded",...input};
      testAPI.layoutTasks.set(task.id,task);return json(task);
    }
    if (path.startsWith("/tasks/layout-")) {
      const task = testAPI.layoutTasks.get(path.split("/")[2]);
      return json(path.endsWith("/config-snapshot")?{content:layoutConfig(task.engine)}:task);
    }
  }
  if (!["GET", "HEAD", "OPTIONS"].includes(method))
    assert.equal(
      new Headers(options.headers).get("X-QControlHub-CSRF"),
      "browser-test-csrf",
      `mutation ${method} ${path} 缺少 CSRF 头`,
    );
  if (method === "GET" && path === "/auth/session")
    return json(mode === "bbr-writeonly"
      ? { role: "user", permissions: ["agents.read", "agents.manage", "tasks.execute"], csrf_token: "browser-test-csrf" }
      : mode.startsWith("shared-node")
      ? { role: "user", user_id: "recipient", permissions: ["agents.read", "agents.manage", "enrollment.manage", "agent-config.read", "agent-config.write", "configs.read", "tasks.read", "tasks.execute", "metrics.read"], csrf_token: "browser-test-csrf" }
      : { role: mode === "readonly" || mode.endsWith("-readonly") ? "readonly" : "admin", csrf_token: "browser-test-csrf" });
  if (method === "GET" && path === "/system-tcp/parameters") return json(tcpRules);
  if (method === "GET" && path === "/system-tcp/tasks") {
    const latest = new Map();
    for (const task of testAPI.tcpTasks || []) {
      if (url.searchParams.get("agent_id") && task.agent_id !== url.searchParams.get("agent_id")) continue;
      if (!latest.has(task.agent_id) || task.created_at >= latest.get(task.agent_id).created_at) latest.set(task.agent_id, task);
    }
    return json([...latest.values()]);
  }
  if (mode.startsWith("bbr") && path === "/tasks") {
    if (method === "GET") return json(testAPI.tcpTasks.filter((task) => task.action === url.searchParams.get("action") && (!url.searchParams.get("agent_id") || task.agent_id === url.searchParams.get("agent_id"))));
    if (method === "POST") {
      const payload = JSON.parse(options.body);
      testAPI.tcpMutations.push(payload);
      if (testAPI.tcpFailure) return json({ error: "TCP test failure" }, 503);
      const task = { ...payload, id: `tcp-${testAPI.tcpTasks.length}`, created_at: new Date().toISOString(), status: "pending" };
      testAPI.tcpTasks.push(task);
      return json(task, 201);
    }
  }
  if (method === "GET" && path === "/overview")
    return json({ agents: mode === "empty" ? 0 : populatedAgents.length, agents_online: mode === "empty" ? 0 : 3 });
  if (method === "GET" && path === "/settings")
    return json(testAPI.settings || { panel_name: "QControlHub Browser Smoke" });
  if (mode.startsWith("users-layout") && method === "GET" && path === "/users")
    return json(testAPI.users);
  if (mode.startsWith("users-layout") && /^\/users\/[^/]+\/agent-access$/.test(path)) {
    const id = decodeURIComponent(path.split("/")[2]);
    const access = testAPI.userAccess[id];
    if (method === "GET") return json(access);
    if (method === "PUT") {
      const payload = JSON.parse(options.body);
      if (payload.revision !== access.revision) return json({ error: "stale allocation" }, 409);
      for (const share of payload.shares) {
        const previous = access.shares.find(item => item.agent_id === share.agent_id);
        if (share.reinvite && (!share.enabled || previous?.status !== "rejected"))
          return json({ error: "only a rejected share can be reinvited" }, 400);
      }
      testAPI.allocationWrites.push({ user_id: id, ...payload });
      testAPI.userAccess[id] = {
        ...access, ...payload, revision: access.revision + 1,
        shares: payload.shares.map(share => {
          const previous = access.shares.find(item => item.agent_id === share.agent_id);
          const invite = share.enabled && (share.reinvite || !previous?.enabled || (previous.status === "accepted" && share.engines.some(engine => !previous.engines.includes(engine))));
          return {
            id: `shr_${share.agent_id}`, used_bytes: 0, ...previous, ...share,
            status: invite ? "pending" : previous?.status || "pending", reinvite: false,
          };
        }),
      };
      return json(testAPI.userAccess[id]);
    }
  }
  if (path === "/settings/deployment" && mode.startsWith("capabilities-settings"))
    return json({ secure_transport: true, database_tls_verified: true, config_encryption_configured: true, webhook_signing_configured: true, trusted_proxy_count: 1, control_plane_version: "preview", agent_package_version: "preview" });
  if (method === "PUT" && path === "/settings" && mode.startsWith("capabilities-settings")) {
    if (testAPI.settingsFailure) return json({ error: "settings save failed" }, 409);
    testAPI.settings = { ...JSON.parse(options.body), revision: testAPI.settings.revision + 1 };
    return json(testAPI.settings);
  }
  if (method === "GET" && path === "/agents" && testAPI.agentsFailure) return json({error:"temporary runtime failure"},503);
  if (method === "GET" && path === "/agents") {
    if (testAPI.agentsGate) await testAPI.agentsGate;
    return json(mode === "empty" ? [] : testAPI.agents);
  }
  if (method === "GET" && path === "/core-logs" && (mode === "logs" || mode === "logs-restore")) {
    const limit = Number(url.searchParams.get("limit") || 1000);
    const agent = url.searchParams.get("agent_id") || "alpha";
    if (testAPI.logGates?.[`${agent}:${limit}`]) await testAPI.logGates[`${agent}:${limit}`];
    if (options.signal?.aborted) throw new DOMException("Aborted", "AbortError");
    return json(["mihomo", "xray", "sing-box", "ss-rust"].flatMap((engine, engineIndex) =>
      Array.from({ length: limit }, (_, index) => ({
        id: engineIndex * 10000 + index + 1, agent_id: agent, engine, level: "warning",
        message: `${agent} ${engine} pressure entry ${index}`, logged_at: "2026-09-06T00:00:00Z",
      }))));
  }
  if (method === "GET" && path === "/deployments") return json(testAPI.deployments);
  if (method === "GET" && path === "/client-access" && mode.startsWith("client-order"))
    return json(testAPI.clientAccessEntries);
  if (method === "GET" && path === "/client-access" && ["ports","readonly","regions","regions-preview"].includes(mode)) {
    const profiles = (address) => [20001,20002].map((port,index) => {
      const tag = `ss-rust-${index+1}`;
      const name = testAPI.profileNames[port] || tag;
      const mode = testAPI.profileModes[port] || "auto";
      const override = testAPI.profileAddresses[port] || "";
      const effective = override || (mode === "ipv6" ? "[2001:db8::1]" : address);
      return {tag,port,client_name:testAPI.profileNames[port] || "",protocol:"Shadowsocks",address:effective,address_mode:mode,address_overridden:Boolean(override),profile:{format:"Shadowsocks SIP002 URI",uri:`ss://example@${effective}:${port}#${encodeURIComponent(name)}`,fields:[]}};
    });
    return json([{agent_id:"alpha",agent_name:"ALPHA",engine:"ss-rust",address:"edge.example.com",source:"test",address_mode:"auto",profiles:profiles("edge.example.com"),address_options:[
      {address:"edge.example.com",family:"ipv4",source:"test",profiles:profiles("edge.example.com")},
      {address:"2001:db8::1",family:"ipv6",source:"test",profiles:profiles("[2001:db8::1]")},
    ]}]);
  }
  if (method === "PUT" && path === "/agents/alpha/client-address") {
    const payload = JSON.parse(options.body);
    testAPI.profileSaves.push(payload);
    if (testAPI.profileSaveFailure) return json({error:"temporary profile save failure"},503);
    if (testAPI.profileSaveGate) await testAPI.profileSaveGate;
    if (payload.profile) {
      if (payload.name !== undefined) testAPI.profileNames[payload.profile.port] = payload.name;
      if (payload.address_mode !== undefined) testAPI.profileModes[payload.profile.port] = payload.address_mode;
      if (payload.address !== undefined) testAPI.profileAddresses[payload.profile.port] = payload.address;
    }
    return json(payload);
  }
  if (method === "PUT" && /^\/agents\/[^/]+\/name$/.test(path)) {
    if (testAPI.renameFailure) return json({ error: "temporary rename failure" }, 503);
    const agentID = decodeURIComponent(path.split("/")[2]);
    const { name } = JSON.parse(String(options.body || "{}"));
    if (testAPI.renameGate) await testAPI.renameGate;
    testAPI.agents = testAPI.agents.map((agent) => agent.id === agentID ? { ...agent, name } : agent);
    return json({ name });
  }
  if (method === "PUT" && /^\/agents\/[^/]+\/capabilities\/[^/]+$/.test(path)) {
    if (testAPI.capabilityFailure) return json({ error: "capability save failure" }, 409);
    const [, , id, , engine] = path.split("/");
    const { enabled } = JSON.parse(options.body);
    const agent = testAPI.agents.find((item) => item.id === id);
    if (agent.runtime?.[engine]?.installed) {
      agent.capability_transitions ||= {};
      agent.capability_transitions[engine] = { task_id: `capability-${Date.now()}`, status: "pending", enabled };
      return json({ enabled: agent.capabilities.includes(engine), task_id: agent.capability_transitions[engine].task_id }, 202);
    }
    agent.capabilities = agent.capabilities.filter((item) => item !== engine);
    if (enabled) agent.capabilities.push(engine);
    return json({ enabled });
  }
  if (method === "GET" && path === "/regions")
    return testAPI.regionCatalogFailure ? json({ error: "temporary catalog failure" }, 503) : json(["CN", "HK", "MO", "TW", "US", "SG", "JP", "GB", "DE", "FR", "AQ", "KR", "CA", "AU", "NL", "IN", "AT", "BE", "BO", "BR", "CH", "ES", "FI", "IE", "IS", "IT", "LU", "MX", "MY", "NO", "NZ", "PH", "PL", "RS", "RU", "SE", "SV", "TH", "TR", "VN", "ZA"]);
  if (method === "PUT" && /^\/agents\/[^/]+\/region$/.test(path)) {
    if (testAPI.regionSaveFailure) return json({ error: "temporary region save failure" }, 503);
    if (testAPI.regionSaveGate) await testAPI.regionSaveGate;
    const agent = testAPI.agents.find((item) => item.id === path.split("/")[2]);
    const { country_code } = JSON.parse(options.body);
    if (country_code) agent.labels.region_code = country_code;
    else delete agent.labels.region_code;
    return json({ country_code });
  }
  if (method === "GET" && path === "/agents/alpha/region") {
    const country_code = testAPI.agents.find((item) => item.id === "alpha")?.labels.region_code || "TW";
    if (testAPI.regionLookupGate) await testAPI.regionLookupGate;
    return json({ ip: "8.8.8.8", country_code });
  }
  if (method === "GET" && path === "/agents/alpha/komari")
    return json({
      uuid: "komari-alpha",
      server: {
        uuid: "komari-alpha",
        name: "Tokyo edge-01",
        billing_cycle: 30,
        expired_at: "2026-08-27T00:00:00Z",
        traffic_limit: 10737418240,
        traffic_limit_type: "sum",
        traffic_used: 4294967296,
        traffic_used_available: true,
      },
    });
  if (method === "GET" && path === "/enrollment-tokens") return json(testAPI.enrollmentRecords);
  if (method === "GET" && /^\/agents\/[^/]+\/configs$/.test(path)) return json(testAPI.savedConfigs.filter((item) => path.includes(item.agent_id)));
  if (method === "GET" && path.startsWith("/metrics/")) return json([]);
  if (method === "POST" && path === "/enrollment-tokens") {
    testAPI.lastEnrollmentRequest = JSON.parse(options.body);
    if (testAPI.enrollmentFailure) return json({ error: "temporary enrollment failure" }, 503);
    return json({ token: "browser-test-enrollment", name: "browser-node" });
  }
  if (method === "POST" && /^\/agents\/[^/]+\/enrollment-command$/.test(path))
    return json({ token: "browser-test-enrollment", name: "ALPHA" });
  if (method === "POST" && path === "/enrollment-tokens/enr-alpha/command")
    return json({ token: "browser-test-enrollment", name: "ALPHA" });
  if (method === "DELETE" && path.startsWith("/enrollment-tokens/")) return json(null, 204);
  if (method === "POST" && path === "/tasks") {
    const payload = JSON.parse(String(options.body || "{}"));
    if (testAPI.taskMode !== "deferred") return json({ id: `task-${payload.agent_id}` });
    return await new Promise((resolve) => {
      testAPI.pendingTasks.push({
        payload,
        ok: (value) => resolve(json(value)),
        fail: (message) => resolve(json({ error: message }, 503)),
      });
    });
  }
  if (method === "POST" && path === "/auth/logout") return json(null, 204);
  return json([]);
};

  return { mode, testAPI, onlineAgent, populatedAgents };
}
