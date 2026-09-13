import { formatConfigContent } from "./code-format.js";
import { orderNodesBySavedOrder } from "./node-order.js";

export function outboundProtocols(engine) {
  const protocols = [["direct", "直连"], ["vless", "VLESS"], ["vmess", "VMess"], ["trojan", "Trojan"],
    ["shadowsocks", "Shadowsocks / 2022"], ["socks", "SOCKS5"], ["http", "HTTP"]];
  return engine === "sing-box" ? [...protocols, ["hysteria2", "Hysteria 2"], ["tuic", "TUIC"], ["anytls", "AnyTLS"]] : protocols;
}

// Deliberately generate only supported, independent exits. Advanced JSON is
// preserved separately, never reverse-converted through a lossy preset form.
export function outboundPreset(engine, protocol, values, validate = true) {
  if (!["xray", "sing-box"].includes(engine) || !outboundProtocols(engine).some(([key]) => key === protocol))
    throw Error("当前内核不支持此出站预设");
  const tag = values.tag?.trim(), server = values.server?.trim().replace(/^\[|\]$/g, ""), port = Number(values.port);
  if (validate && (!tag || /^qch-(trf-|stat-)/.test(tag))) throw Error("请填写有效的非系统出站标签");
  if (protocol === "direct") return engine === "xray" ? {tag, protocol:"freedom"} : {tag, type:"direct"};
  if (validate && (!server || /[\s/@?#]/.test(server) || !Number.isInteger(port) || port < 1 || port > 65535))
    throw Error("请填写出口服务器地址和 1–65535 范围内的端口，不要填写 URL");
  const credential = values.credential || "", password = values.password || "", username = values.username || "";
  const uuid = ["vless", "vmess", "tuic"].includes(protocol), authentication = ["socks", "http"].includes(protocol);
  if (validate && !authentication && !credential) throw Error(uuid ? "请填写目标入站的 UUID" : "请填写目标入站的密码");
  if (validate && uuid && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(credential))
    throw Error("请填写有效的用户 UUID");
  if (validate && (protocol === "tuic" && !password || authentication && Boolean(username) !== Boolean(password)))
    throw Error("请完整填写认证用户名和密码；TUIC 还需用户密码");
  const transportable = ["vless", "vmess", "trojan"].includes(protocol);
  const tlsRequired = ["hysteria2", "tuic", "anytls"].includes(protocol);
  const security = tlsRequired ? "tls" : transportable || protocol === "http" ? values.security || "none" : "none";
  const transport = transportable ? values.transport || "tcp" : "tcp";
  if (security === "reality" && protocol !== "vless") throw Error("Reality 预设仅用于 VLESS");
  if (!["none", "tls", "reality"].includes(security) || !["tcp", "ws", "grpc", ...(engine === "xray" ? ["xhttp"] : [])].includes(transport))
    throw Error("当前内核不支持此安全或传输选项");
  if (validate && security === "reality" && (!values.serverName?.trim() || !values.publicKey?.trim()))
    throw Error("Reality 需要填写 ServerName 和目标入站的公钥，不是服务端私钥");
  if (validate && security === "reality" && !/^(?:[0-9a-f]{2}){0,8}$/i.test(values.shortId || ""))
    throw Error("Reality Short ID 必须是最多 16 位的偶数长度十六进制字符");
  const sni = values.serverName?.trim() || server, flow = protocol === "vless" ? values.flow || "" : "";
  if (validate && flow && (flow !== "xtls-rprx-vision" || transport !== "tcp" || security === "none"))
    throw Error("Vision 预设需要 TCP + TLS / Reality；其他组合请使用高级 JSON 并校验");
  const out = {tag};
  if (engine === "xray") {
    out.protocol = protocol;
    const peer = {address:server, port};
    if (["vless", "vmess"].includes(protocol)) {
      peer.users = [{id:credential, ...(protocol === "vless" ? {encryption:"none", ...(flow ? {flow} : {})} : {security:"auto"})}];
      out.settings = {vnext:[peer]};
    } else {
      if (authentication) {
        if (username) peer.users = [{user:username, pass:password}];
      } else peer.password = credential;
      if (protocol === "shadowsocks") peer.method = values.method;
      out.settings = {servers:[peer]};
    }
    if (transportable || protocol === "http" && security !== "none") {
      const stream = {network:transport};
      if (transport === "ws") stream.wsSettings = {path:values.path || "/"};
      if (transport === "grpc") stream.grpcSettings = {serviceName:values.path || ""};
      if (transport === "xhttp") stream.xhttpSettings = {path:values.path || "/", mode:"auto"};
      if (security === "tls") Object.assign(stream, {security:"tls", tlsSettings:{serverName:sni}});
      if (security === "reality") Object.assign(stream, {security:"reality",
        realitySettings:{serverName:sni, fingerprint:"chrome", publicKey:values.publicKey?.trim(), shortId:values.shortId || ""}});
      out.streamSettings = stream;
    }
  } else {
    Object.assign(out, {type:protocol, server, server_port:port});
    if (uuid) out.uuid = credential;
    if (protocol === "vmess") Object.assign(out, {security:"auto", alter_id:0});
    if (flow) out.flow = flow;
    if (authentication) {
      if (username) Object.assign(out, {username, password});
      if (protocol === "socks") out.version = "5";
    } else if (protocol === "tuic") Object.assign(out, {password, congestion_control:"bbr"});
    else if (!uuid) out.password = credential;
    if (protocol === "shadowsocks") out.method = values.method;
    if (transport === "ws") out.transport = {type:"ws", path:values.path || "/"};
    if (transport === "grpc") out.transport = {type:"grpc", service_name:values.path || ""};
    if (security !== "none") out.tls = {enabled:true, server_name:sni};
    if (security === "reality") Object.assign(out.tls, {utls:{enabled:true, fingerprint:"chrome"},
      reality:{enabled:true, public_key:values.publicKey?.trim(), short_id:values.shortId || ""}});
  }
  return out;
}

export function outboundPeers(entries, agentId) {
  return entries.filter(entry => entry.agent_id !== agentId).flatMap(entry =>
    (entry.profiles || []).map(profile => ({...profile, agentId:entry.agent_id, agentName:entry.agent_name,
      agentStatus:entry.agent_status, engine:entry.engine})));
}

export function bindOutboundPresets({ form, input, engine, agentId, api, canReadPeers, active, onChange, initialMode = "preset" }) {
  const host = document.createElement("div");
  host.className = "config-outbound-presets";
  host.innerHTML = `<label class="field-value-label">出站来源<select data-outbound-mode><option value="preset">填写协议预设</option><option value="node">选择其他节点入站</option><option value="json">高级 JSON</option></select></label>
    <fieldset data-outbound-builder><div class="config-outbound-fields"><label>出站标签<input data-outbound-value="tag" autocomplete="off" maxlength="100"></label>
      <label data-outbound-protocol-label>协议预设<select data-outbound-protocol></select></label></div>
      <div data-outbound-manual class="config-outbound-fields">
        <label data-preset-server>服务器地址<input data-outbound-value="server" autocomplete="off" placeholder="域名或 IP"></label>
        <label data-preset-server>端口<input data-outbound-value="port" type="number" min="1" max="65535" value="443"></label>
        <label data-preset-credential><span data-outbound-credential-label>密码</span><input data-outbound-value="credential" type="password" autocomplete="new-password" spellcheck="false"></label>
        <label data-preset-username>用户名（可选）<input data-outbound-value="username" autocomplete="off"></label>
        <label data-preset-password>认证密码<input data-outbound-value="password" type="password" autocomplete="new-password"></label>
        <label data-preset-method>加密方法<select data-outbound-value="method">
          <option>aes-128-gcm</option><option>aes-256-gcm</option><option>chacha20-ietf-poly1305</option>
          <option>2022-blake3-aes-128-gcm</option><option>2022-blake3-aes-256-gcm</option><option>2022-blake3-chacha20-poly1305</option></select></label>
        <label data-preset-transport>传输<select data-outbound-value="transport"><option value="tcp">TCP</option><option value="ws">WebSocket</option><option value="grpc">gRPC</option>${engine === "xray" ? '<option value="xhttp">XHTTP</option>' : ""}</select></label>
        <label data-preset-path>路径 / ServiceName<input data-outbound-value="path" autocomplete="off" placeholder="/"></label>
        <label data-preset-security>传输安全<select data-outbound-value="security"><option value="none">无 TLS</option><option value="tls">TLS</option><option value="reality">Reality</option></select></label>
        <label data-preset-tls>ServerName<input data-outbound-value="serverName" autocomplete="off" placeholder="留空使用服务器地址"></label>
        <label data-preset-reality>Reality 公钥<input data-outbound-value="publicKey" autocomplete="off" spellcheck="false"></label>
        <label data-preset-reality>Reality Short ID<input data-outbound-value="shortId" autocomplete="off" maxlength="16"></label>
        <label data-preset-flow>Flow<select data-outbound-value="flow"><option value="">不设置</option><option value="xtls-rprx-vision">xtls-rprx-vision</option></select></label>
      </div>
      <div data-outbound-node hidden><div class="config-outbound-fields"><label>出口节点<select data-outbound-node-select aria-label="出口节点"></select></label><label>目标入站<select data-outbound-peer-select aria-label="目标入站"></select></label></div>
        <p class="field-editor-note" data-outbound-peer-status role="status"></p><button class="button small" type="button" data-outbound-peer-reload>刷新可用入站</button>
        <p class="field-editor-note">只使用有权读取的已部署入站；仅复制连接参数，不修改目标节点，也不自动跟随后续变更。请避免节点间循环转发。</p></div>
    </fieldset>`;
  form.prepend(host);
  const mode = host.querySelector("[data-outbound-mode]"), builder = host.querySelector("fieldset");
  const protocol = host.querySelector("[data-outbound-protocol]"), node = host.querySelector("[data-outbound-node-select]");
  const peer = host.querySelector("[data-outbound-peer-select]"), peerStatus = host.querySelector("[data-outbound-peer-status]");
  const value = key => host.querySelector(`[data-outbound-value="${key}"]`);
  const values = () => Object.fromEntries([...host.querySelectorAll("[data-outbound-value]")].map(element => [element.dataset.outboundValue, element.value]));
  outboundProtocols(engine).forEach(([key, label]) => protocol.add(new Option(label, key)));
  value("tag").value = JSON.parse(input.value).tag;
  mode.value = initialMode;
  mode.querySelector('[value="node"]').disabled = !canReadPeers;
  if (!canReadPeers) mode.title = "选择节点入站需要客户端配置读取权限";
  let touched = false, busy = false, peers = [], loaded = false, loadRequest = 0, jsonDraft = initialMode === "json" ? input.value : null, previousMode = mode.value;
  let loading = false;
  const controller = new AbortController();
  const chosenPeer = () => {
    const selected = peer.value === "" ? null : peers[Number(peer.value)];
    return selected?.agentId === node.value ? selected : null;
  };
  const peerKey = selected => selected ? JSON.stringify([selected.agentId, selected.engine, selected.tag, selected.port]) : "";
  const generatedContent = (validate = true) => {
    if (mode.value === "preset") return JSON.stringify(outboundPreset(engine, protocol.value, values(), validate), null, 2);
    const selected = chosenPeer();
    if (!selected?.outbound) {
      if (!validate) return "";
      throw Error(selected?.outbound_error || "请先选择可用的其他节点入站");
    }
    const tag = value("tag").value.trim();
    if (validate && (!tag || /^qch-(trf-|stat-)/.test(tag))) throw Error("请填写有效的非系统出站标签");
    return JSON.stringify({...selected.outbound, tag}, null, 2);
  };
  const show = (selector, visible) => host.querySelectorAll(selector).forEach(element => { element.hidden = !visible; });
  const sync = () => {
    const manual = mode.value === "preset", source = mode.value === "node", kind = protocol.value;
    const proxy = kind !== "direct", transportable = ["vless", "vmess", "trojan"].includes(kind);
    const tlsRequired = ["hysteria2", "tuic", "anytls"].includes(kind);
    builder.hidden = mode.value === "json";
    builder.disabled = busy;
    mode.disabled = busy;
    show("[data-outbound-protocol-label], [data-outbound-manual]", manual);
    show("[data-outbound-node]", source);
    show("[data-preset-server]", proxy);
    show("[data-preset-credential]", proxy && !["http", "socks"].includes(kind));
    host.querySelector("[data-outbound-credential-label]").textContent = ["vless", "vmess", "tuic"].includes(kind) ? "用户 UUID" : "密码";
    show("[data-preset-username]", ["http", "socks"].includes(kind));
    show("[data-preset-password]", ["http", "socks", "tuic"].includes(kind));
    show("[data-preset-method]", kind === "shadowsocks");
    show("[data-preset-transport]", transportable);
    show("[data-preset-path]", transportable && value("transport").value !== "tcp");
    show("[data-preset-security]", transportable || kind === "http" || tlsRequired);
    show("[data-preset-tls]", (transportable || kind === "http" || tlsRequired) && value("security").value !== "none");
    show("[data-preset-reality]", kind === "vless" && value("security").value === "reality");
    show("[data-preset-flow]", kind === "vless");
    value("serverName").placeholder = value("security").value === "reality" ? "必填，与目标入站的 ServerName 一致" : "留空使用服务器地址";
    value("security").querySelector('[value="reality"]').disabled = kind !== "vless";
    value("security").disabled = busy || tlsRequired;
    host.querySelectorAll("[data-outbound-value]").forEach(element => {
      if (element.dataset.outboundValue !== "security") element.disabled = busy || Boolean(element.closest("[hidden]"));
    });
    protocol.disabled = busy || !manual;
    node.disabled = peer.disabled = busy || loading || !source;
    host.querySelector("[data-outbound-peer-reload]").disabled = busy || loading;
    input.readOnly = mode.value !== "json";
    if (mode.value !== "json") {
      const content = generatedContent(false);
      input.value = content ? formatConfigContent(content, "JSON") : "";
      input.rows = Math.min(16, Math.max(6, input.value.split("\n").length + 1));
    }
  };
  const updatePeer = () => {
    const selected = chosenPeer();
    peerStatus.textContent = selected ? selected.outbound_error ||
      `${selected.agentName} / ${selected.tag} · ${selected.address || ""}:${selected.port}${selected.agentStatus === "offline" ? " · 节点离线，请确认可达性" : ""}` :
      peers.filter(item => item.agentId === node.value && item.outbound_error).map(item => item.outbound_error).join("；") ||
      (peers.length ? "请选择出口节点及目标入站。" : "没有可用入站。请先在其他节点部署支持的协议并设置客户端连接地址。");
    sync();
    onChange();
  };
  const updateNode = preserved => {
    peer.replaceChildren();
    peer.add(new Option("请选择目标入站", ""));
    peers.forEach((item, index) => {
      if (item.agentId !== node.value) return;
      const option = new Option(`${item.engine} · ${item.client_name || item.tag} :${item.port} · ${item.protocol}${item.outbound_error ? "（不兼容）" : ""}`, String(index));
      option.disabled = !item.outbound;
      peer.add(option);
    });
    const first = [...peer.options].find(option => option.value && !option.disabled);
    const index = preserved === undefined ? -1 : peers.findIndex(item => peerKey(item) === preserved && item.outbound);
    peer.value = preserved === undefined ? first?.value || "" : index < 0 ? "" : String(index);
    updatePeer();
    if (preserved && !chosenPeer()) peerStatus.textContent = "原目标入站已变化或不可用，请重新选择；不会自动改用其他入站。";
  };
  const loadPeers = async () => {
    if (!canReadPeers || busy) return;
    const request = ++loadRequest, selectedNode = node.value, selectedPeer = peerKey(chosenPeer());
    loading = true;
    peerStatus.textContent = "正在读取已部署入站…";
    sync();
    try {
      const entries = await api(`/client-access?outbound_engine=${encodeURIComponent(engine)}`, {method:"GET", signal:controller.signal});
      if (!active() || request !== loadRequest) return;
      peers = outboundPeers(entries, agentId);
      loaded = true;
      node.replaceChildren();
      node.add(new Option("请选择出口节点", ""));
      const nodes = new Map(peers.map(item => [item.agentId, item.agentName]));
      for (const {id, name} of orderNodesBySavedOrder([...nodes].map(([id, name]) => ({id, name}))))
        node.add(new Option(name, id));
      if (nodes.has(selectedNode)) node.value = selectedNode;
      updateNode(selectedPeer);
    } catch (error) {
      if (active() && request === loadRequest) {
        peerStatus.textContent = "读取可用入站失败，请重试。";
        peers = []; node.replaceChildren(); peer.replaceChildren(); loaded = false;
      }
    } finally {
      if (active() && request === loadRequest) { loading = false; sync(); onChange(); }
    }
  };
  mode.addEventListener("change", () => {
    if (previousMode === "json") jsonDraft = input.value;
    if (mode.value === "json" && jsonDraft !== null) input.value = jsonDraft;
    previousMode = mode.value;
    touched = true;
    sync(); onChange();
    if (mode.value === "node" && !loaded) void loadPeers();
  });
  protocol.addEventListener("change", () => {
    if (["trojan", "hysteria2", "tuic", "anytls"].includes(protocol.value)) value("security").value = "tls";
    else if (value("security").value === "reality" && protocol.value !== "vless") value("security").value = "none";
    if (protocol.value !== "vless") value("flow").value = "";
  });
  host.addEventListener("input", event => {
    if (event.target.tagName === "SELECT") return;
    touched = true;
    sync(); onChange();
  });
  // Programmatic and keyboard selection both dispatch change.
  host.addEventListener("change", event => {
    if (event.target.matches("[data-outbound-value], [data-outbound-protocol]")) { touched = true; sync(); onChange(); }
  });
  node.addEventListener("change", () => { touched = true; updateNode(); });
  peer.addEventListener("change", () => { touched = true; updatePeer(); });
  host.querySelector("[data-outbound-peer-reload]").addEventListener("click", () => { void loadPeers(); });
  sync();
  return {
    dirty: () => touched,
    content: () => {
      if (mode.value === "node" && loading) throw Error("正在读取目标入站，请稍候");
      return mode.value === "json" ? input.value : generatedContent();
    },
    busy: value => { busy = value; sync(); },
    focus: () => mode.focus({preventScroll:true}),
    dispose: () => controller.abort(),
  };
}
