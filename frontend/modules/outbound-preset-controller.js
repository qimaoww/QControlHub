import { formatConfigContent } from "./code-format.js";
import { orderNodesBySavedOrder } from "./node-order.js";
import { outboundProtocols, outboundPreset, outboundPeers, readOutboundPreset, outboundPresetText } from "./outbound-preset-model.js";

export function bindOutboundPresetController({ form, input, engine, agentId, api, canReadPeers, active, onChange, initialMode = "preset", initialObject, host, visibility = (element, visible) => { if (element) element.hidden = !visible; }, feedback = (element, value) => { element.textContent = value; } }) {
  form.prepend(host);
  const mode = host.querySelector("[data-outbound-mode]"), builder = host.querySelector("fieldset");
  const protocol = host.querySelector("[data-outbound-protocol]"), node = host.querySelector("[data-outbound-node-select]");
  const peer = host.querySelector("[data-outbound-peer-select]"), peerStatus = host.querySelector("[data-outbound-peer-status]");
  const value = key => host.querySelector(`[data-outbound-value="${key}"]`);
  const values = () => Object.fromEntries([...host.querySelectorAll("[data-outbound-value]")].map(element => [element.dataset.outboundValue, element.value]));
  outboundProtocols(engine).forEach(([key, label]) => protocol.add(new Option(label, key)));
  let original;
  try { original = initialObject || JSON.parse(input.value); } catch { /* Native YAML is projected by the API. */ }
  const projection = readOutboundPreset(engine, original);
  value("tag").value = original?.tag || original?.name || "new-exit";
  if (projection) {
    protocol.value = projection.protocol;
    for (const [key,item] of Object.entries(projection.values)) if (value(key)) value(key).value = item;
  }
  if (initialMode === "auto") initialMode = projection ? "preset" : "json";
  mode.value = initialMode;
  if (initialMode === "json" && input.closest("details")) input.closest("details").open = true;
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
    if (mode.value === "preset") return outboundPresetText(engine, outboundPreset(engine, protocol.value, values(), validate));
    const selected = chosenPeer();
    if (!selected?.outbound) {
      if (!validate) return "";
      throw Error(selected?.outbound_error || "请先选择可用的其他节点入站");
    }
    const tag = value("tag").value.trim();
    if (validate && (!tag || /^qch-(trf-|stat-)/.test(tag))) throw Error("请填写有效的非系统出站标签");
    return outboundPresetText(engine, {...selected.outbound, [engine === "mihomo" ? "name" : "tag"]:tag});
  };
  const show = (selector, visible) => host.querySelectorAll(selector).forEach(element => { visibility(element, visible); });
  const sync = () => {
    const manual = mode.value === "preset", source = mode.value === "node", kind = protocol.value;
    const proxy = kind !== "direct", transportable = ["vless", "vmess", "trojan"].includes(kind);
    const tlsRequired = ["hysteria2", "tuic", "anytls"].includes(kind);
    visibility(builder, !(mode.value === "json"));
    builder.disabled = busy;
    mode.disabled = busy;
    show("[data-outbound-protocol-label], [data-outbound-manual]", manual);
    show("[data-outbound-node]", source);
    show("[data-preset-server]", proxy);
    show("[data-preset-credential]", proxy && !["http", "socks"].includes(kind));
    feedback(host.querySelector("[data-outbound-credential-label]"), ["vless", "vmess", "tuic"].includes(kind) ? "用户 UUID" : "密码");
    show("[data-preset-username]", ["http", "socks"].includes(kind));
    show("[data-preset-password]", ["http", "socks", "tuic"].includes(kind));
    show("[data-preset-method]", kind === "shadowsocks");
    show("[data-preset-transport]", transportable);
    show("[data-preset-ws-host]", transportable && value("transport").value === "ws");
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
      input.value = content ? engine === "mihomo" ? content : formatConfigContent(content, "JSON") : "";
      input.rows = Math.min(16, Math.max(6, input.value.split("\n").length + 1));
    }
  };
  const updatePeer = () => {
    const selected = chosenPeer();
    feedback(peerStatus, selected ? selected.outbound_error ||
      `${selected.agentName} / ${selected.tag} · ${selected.address || ""}:${selected.port}${selected.agentStatus === "offline" ? " · 节点离线，请确认可达性" : ""}` :
      peers.filter(item => item.agentId === node.value && item.outbound_error).map(item => item.outbound_error).join("；") ||
      (peers.length ? "请选择出口节点及目标入站。" : "没有可用入站。请先在其他节点部署支持的协议并设置客户端连接地址。"));
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
    if (preserved && !chosenPeer()) feedback(peerStatus, "原目标入站已变化或不可用，请重新选择；不会自动改用其他入站。");
  };
  const loadPeers = async () => {
    if (!canReadPeers || busy) return;
    const request = ++loadRequest, selectedNode = node.value, selectedPeer = peerKey(chosenPeer());
    loading = true;
    feedback(peerStatus, "正在读取已部署入站…");
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
        feedback(peerStatus, "读取可用入站失败，请重试。");
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
    if (mode.value === "json" && input.closest("details")) input.closest("details").open = true;
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
  const presetBase = projection ? generatedContent(false) : "";
  return {
    baseline: () => initialObject && mode.value === "preset" ? presetBase : "",
    dirty: () => touched,
    verifiedContent: async () => {
      if (mode.value === "node") {
        const previous = JSON.stringify(chosenPeer()?.outbound);
        await loadPeers();
        if (chosenPeer()?.outbound && JSON.stringify(chosenPeer().outbound) !== previous) throw Error("目标连接参数已变化，请核对更新后的内容再提交");
        if (!active()) throw new DOMException("目标已切换", "AbortError");
        if (!chosenPeer()?.outbound) throw Error("目标入站已变化或不再获授权，请重新选择");
      }
      return mode.value === "json" ? input.value : generatedContent();
    },
    content: () => {
      if (mode.value === "node" && loading) throw Error("正在读取目标入站，请稍候");
      return mode.value === "json" ? input.value : generatedContent();
    },
    busy: value => { busy = value; sync(); },
    refresh: sync,
    focus: () => mode.focus({preventScroll:true}),
    dispose: () => controller.abort(),
  };
}
