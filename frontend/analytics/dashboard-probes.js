import { isValidProbeIPAddress } from "../modules/dashboard-model.js";
import { orderNodesBySavedOrder } from "../modules/node-order.js";
import { badge, button, field, list, muted, node, pathID } from "./ui.js";

const nodeKey = endpoint => `${endpoint?.agent_id || ""}/${endpoint?.instance_id || ""}`;
const isIPv6 = addr => String(addr || "").includes(":");

export function createDashboardProbesSection(ctx, { getOrderIDs, getProbes, getProbesState, reloadProbes, onChange = () => {} }) {
  const canExecute = () => ctx.can("agents.read") && ctx.can("tasks.read") && ctx.can("tasks.execute");
  const canView = () => ctx.can("agents.read") && ctx.can("tasks.read");
  const inFlightOps = new Set();
  const dialogs = new Set();
  ctx.signal.addEventListener("abort", () => { dialogs.forEach(box => box.close()); dialogs.clear(); }, { once: true });
  function openProbeDialog(pair = null, opts = {}) {
    if (!canExecute()) return;
    const MAX_HOPS = 10;
    const isEdit = Boolean(pair?.id);
    const params = pair?.parameters || {};
    const initialMode = pair?.target_mode === "ip" ? "ip" : "node";

    const existingNodes = Array.isArray(pair?.nodes) && pair.nodes.length >= 2
      ? pair.nodes
      : pair
        ? [pair.node1, pair.node2]
        : [];
    const hopsState = existingNodes.length >= 2
      ? existingNodes.map((ep) => ({
          key: ep?.agent_id ? nodeKey(ep) : "",
          address: ep?.address || "",
        }))
      : [
          { key: "", address: "" },
          { key: "", address: "" },
        ];
    if (initialMode === "ip" && hopsState.length < 2) {
      hopsState.push({ key: "", address: "" });
    }

    const groupInput = field("监测分组", "group_name", pair?.group_name || opts?.groupName || "", {
      maxlength: 40,
      placeholder: "分组名称",
      required: true,
    });
    const modeField = field("监测模式", "target_mode", initialMode, {
      options: [
        ["node", "托管节点多跳链路"],
        ["ip", "外部 IP"],
      ],
    });

    const ipSrcNodeField = field("起点节点", "ip_src_key", hopsState[0]?.key || "", {
      options: [["", "读取中…"]],
    });
    const ipSrcAddrField = field("起点地址", "ip_src_addr", hopsState[0]?.address || "", {
      options: hopsState[0]?.address ? [[hopsState[0].address, hopsState[0].address]] : [["", "请选择"]],
    });
    const customIPField = field("对端 IP", "node2_ip", pair && initialMode === "ip" ? pair?.node2?.address || "" : "", {
      placeholder: "IPv4 / IPv6",
    });

    const bidirField = field("去程与回程双向测量", "bidirectional", pair ? Boolean(pair.bidirectional) : true, {
      type: "checkbox",
    });

    const countField = field("探测次数", "count", params.count || 5, { type: "number", min: 1, max: 100, required: true });
    const intervalField = field("间隔 (ms)", "interval_ms", params.interval_ms || 200, { type: "number", min: 200, max: 5000, required: true });
    const timeoutField = field("超时 (ms)", "timeout_ms", params.timeout_ms || 1000, { type: "number", min: 100, max: 5000, required: true });
    const sizeField = field("负载 (B)", "size", params.size ?? 56, { type: "number", min: 0, max: 1400, required: true });
    const autoEnabledField = field("自动周期探测", "auto_enabled", pair ? Boolean(pair.auto_enabled) : true, { type: "checkbox" });
    const autoSecField = field("周期 (s)", "auto_interval_seconds", pair?.auto_interval_seconds || 60, { type: "number", min: 15, max: 3600, required: true });
    autoSecField.hidden = pair ? !pair.auto_enabled : false;
    autoEnabledField.querySelector("input").addEventListener("change", event => { autoSecField.hidden = !event.target.checked; });
    const readHint = muted("读取节点中…");

    const modeSelect = modeField.querySelector("select");
    const ipSrcNodeSelect = ipSrcNodeField.querySelector("select");
    const ipSrcAddrSelect = ipSrcAddrField.querySelector("select");
    const ipInput = customIPField.querySelector("input");
    const bidirInput = bidirField.querySelector("input");

    const chainCountBadge = badge(`${hopsState.length} / ${MAX_HOPS} 节点`);
    const hopListHost = node("div", { className: "a-dash-probe-hop-list" });
    let addHopBtn;
    const chainBuilder = node("div", { className: "a-dash-probe-chain-builder" },
      node("div", { className: "a-dash-probe-chain-head" },
        node("div", { className: "a-dash-probe-chain-title" },
          node("strong", {}, "全程监测节点顺序"),
          chainCountBadge,
        ),
        addHopBtn = button("追加节点", () => {
          if (hopsState.length >= MAX_HOPS) return;
          const usedKeys = new Set(hopsState.map((h) => h.key).filter(Boolean));
          const candidate = orderedNodes.find((item) => !usedKeys.has(nodeKey(item))) || null;
          hopsState.push({ key: candidate ? nodeKey(candidate) : "", address: "" });
          renderChainRows();
        }, { className: "a-button", "data-refresh-key": "add-hop" }),
      ),
      hopListHost,
    );
    const customIPGrid = node("div", { className: "a-form-grid" },
      ipSrcNodeField,
      ipSrcAddrField,
      customIPField,
    );

    let orderedNodes = [];
    const nodeByKey = new Map();

    const syncOptions = (selectEl, items, preferred) => {
      const btn = selectEl.querySelector("button");
      const opts = items.map(([val, label, disabled = false]) => node("option", { value: val, disabled }, label));
      selectEl.replaceChildren(...(btn ? [btn] : []), ...opts);
      if (preferred && !items.some(([val]) => val === preferred)) {
        selectEl.append(node("option", { value: preferred, disabled: true }, "已不可用"));
        selectEl.value = preferred;
      } else if (items.some(([val, , dis]) => val === preferred && !dis)) selectEl.value = preferred;
      else selectEl.value = items.find(([, , dis]) => !dis)?.[0] || "";
    };

    const nodeOptionItems = (excludeKeys) => orderedNodes
      .filter((item) => !excludeKeys.has(nodeKey(item)))
      .map((item) => {
        const key = nodeKey(item);
        const suffix = !item.online
          ? " · 离线"
          : item.instance_id && item.instance_id !== "main"
            ? ` · ${item.context || item.instance_id}`
            : "";
        return [key, `${item.name}${suffix}`, false];
      });

    const compatibleAddrsForHop = (agentItem, baseAddr) => {
      const allAddrs = agentItem?.capabilities?.addresses || [];
      if (!baseAddr) return allAddrs;
      return allAddrs.filter((addr) => isIPv6(addr) === isIPv6(baseAddr));
    };

    const updatePreviewText = () => {
      chainCountBadge.textContent = `${hopsState.length} / ${MAX_HOPS} 节点`;
      if (addHopBtn) addHopBtn.disabled = hopsState.length >= MAX_HOPS || (orderedNodes.length > 0 && hopsState.length >= orderedNodes.length);
    };

    const renderChainRows = () => {
      const rows = hopsState.map((hop, idx) => {
        const otherKeys = new Set(hopsState.filter((_, i) => i !== idx).map((h) => h.key).filter(Boolean));
        const availableItems = nodeOptionItems(otherKeys);
        const nodeSelect = node("select", { name: `hop_node_${idx}`, "data-hop-index": String(idx), required: modeSelect.value !== "ip" });
        nodeSelect.prepend(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")));
        syncOptions(
          nodeSelect,
          availableItems.length ? availableItems : [["", orderedNodes.length ? "无更多可选节点" : "读取节点中…", true]],
          hop.key,
        );
        if (nodeSelect.value && nodeByKey.has(nodeSelect.value)) hop.key = nodeSelect.value;

        const addrSelect = node("select", { name: `hop_addr_${idx}`, "data-hop-addr-index": String(idx), required: modeSelect.value !== "ip" });
        addrSelect.prepend(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")));

        const syncRowAddrs = (baseAddr) => {
          const item = nodeByKey.get(hop.key);
          const allAddrs = item?.capabilities?.addresses || [];
          const compat = idx === 0 ? allAddrs : compatibleAddrsForHop(item, baseAddr);
          const addrItems = compat.length
            ? compat.map((addr) => [addr, addr, false])
            : [["", allAddrs.length ? "无匹配 IP 协议族地址" : (orderedNodes.length ? "节点无可用单播地址" : (hop.address || "请选择")), !hop.address || allAddrs.length > 0]];
          syncOptions(addrSelect, addrItems, hop.address);
          if (addrSelect.value) hop.address = addrSelect.value;
          else if (orderedNodes.length) hop.address = "";
        };

        const baseAddr = idx === 0 ? "" : hopsState[0]?.address || "";
        syncRowAddrs(baseAddr);

        nodeSelect.addEventListener("change", () => {
          hop.key = nodeSelect.value;
          hop.address = "";
          renderChainRows();
        });
        addrSelect.addEventListener("change", () => {
          hop.address = addrSelect.value;
          if (idx === 0) renderChainRows();
          else updatePreviewText();
        });

        const roleLabel = idx === 0 ? "起点" : idx === hopsState.length - 1 ? "终点" : `第 ${idx + 1} 站`;
        return node("div", { className: "a-dash-probe-hop-row", "data-hop-row": String(idx) },
          node("span", { className: "a-badge a-dash-probe-hop-idx" }, roleLabel),
          node("div", { className: "a-dash-probe-hop-fields" },
            node("div", { className: "a-field" },
              node("label", { className: "a-field-label" }, idx === 0 ? "节点" : "下一跳节点"),
              nodeSelect,
            ),
            node("div", { className: "a-field" },
              node("label", { className: "a-field-label" }, "探测地址"),
              addrSelect,
            ),
          ),
          node("div", { className: "a-actions a-dash-probe-hop-ops" },
            button("上移", () => {
              if (idx <= 0) return;
              [hopsState[idx - 1], hopsState[idx]] = [hopsState[idx], hopsState[idx - 1]];
              renderChainRows();
            }, { className: "a-button", disabled: idx === 0, title: "上移节点" }),
            button("下移", () => {
              if (idx >= hopsState.length - 1) return;
              [hopsState[idx], hopsState[idx + 1]] = [hopsState[idx + 1], hopsState[idx]];
              renderChainRows();
            }, { className: "a-button", disabled: idx === hopsState.length - 1, title: "下移节点" }),
            button("移除", () => {
              if (hopsState.length <= 2) return;
              hopsState.splice(idx, 1);
              renderChainRows();
            }, { className: "a-button", tone: "danger", disabled: hopsState.length <= 2, title: "移除此跳节点" }),
          ),
        );
      });
      hopListHost.replaceChildren(...rows);
      updatePreviewText();
    };

    const syncCustomIPSrcAddrs = () => {
      const n1 = nodeByKey.get(ipSrcNodeSelect.value);
      const addrs = n1?.capabilities?.addresses || [];
      syncOptions(
        ipSrcAddrSelect,
        addrs.length ? addrs.map((addr) => [addr, addr]) : [["", orderedNodes.length ? "节点无可用单播地址" : "请选择", true]],
        ipSrcAddrSelect.value || hopsState[0]?.address,
      );
    };

    ipSrcNodeSelect.addEventListener("change", syncCustomIPSrcAddrs);

    const syncMode = () => {
      const custom = modeSelect.value === "ip";
      chainBuilder.hidden = custom;
      customIPGrid.hidden = !custom;
      ipSrcNodeSelect.required = custom;
      ipSrcAddrSelect.required = custom;
      ipInput.required = custom;
      bidirField.hidden = custom;
      if (custom) {
        bidirInput.checked = false;
        bidirInput.disabled = true;
      } else {
        bidirInput.disabled = false;
      }
      renderChainRows();
    };

    bidirInput.addEventListener("change", updatePreviewText);
    modeSelect.addEventListener("change", syncMode);
    syncMode();

    let box;
    let nodesReady = false;
    const dialogLifetime = new AbortController();
    const form = ctx.form([
      readHint,
      node("div", { className: "a-form-grid" }, groupInput, modeField),
      chainBuilder,
      customIPGrid,
      bidirField,
      node("div", { className: "a-form-grid" },
        countField,
        intervalField,
        timeoutField,
        sizeField,
        autoSecField,
      ),
      autoEnabledField,
    ], isEdit ? "保存修改" : "添加链路", async (values, formEl) => {
      if (!nodesReady) throw new Error("诊断节点尚未就绪");
      const groupName = String(values.group_name || "").trim();
      if (!groupName) throw new Error("请输入监测分组名称");
      if ((getProbes()?.pairs || []).some(item => item.id !== pair?.id && item.group_name === groupName)) throw new Error("该监测分组已有链路，请使用其他分组名称");
      const custom = values.target_mode === "ip";
      let payload;
      if (custom) {
        const n1 = nodeByKey.get(values.ip_src_key);
        if (!n1) throw new Error("请选择有效的起点节点。");
        if (!values.ip_src_addr) throw new Error("请选择起点节点的探测地址。");
        const ip = String(values.node2_ip || "").trim();
        if (!isValidProbeIPAddress(ip)) throw new Error("请输入有效的 IPv4 或 IPv6 单播目标地址。");
        if (isIPv6(ip) !== isIPv6(values.ip_src_addr)) throw new Error("对端 IP 须与起点地址使用相同 IP 地址族。");
        payload = {
          name: pair?.name || "",
          group_name: String(values.group_name || "").trim(),
          target_mode: "ip",
          node1: {
            agent_id: n1.agent_id,
            instance_id: n1.instance_id || "",
            name: n1.name,
            address: values.ip_src_addr,
          },
          node2: { address: ip, name: ip },
          bidirectional: false,
        };
      } else {
        if (hopsState.length < 2) throw new Error("全程监测至少需要 2 个节点。");
        if (hopsState.length > MAX_HOPS) throw new Error(`单条全程监测链路最多支持 ${MAX_HOPS} 个节点。`);
        const seenAgents = new Set();
        let baseIs6 = null;
        const endpoints = hopsState.map((hop, idx) => {
          const item = nodeByKey.get(hop.key);
          if (!item) throw new Error(`请选择有效的第 ${idx + 1} 跳节点。`);
          if (seenAgents.has(item.agent_id)) throw new Error("同一条全程监测链路中不能包含重复节点。");
          seenAgents.add(item.agent_id);
          if (!hop.address) throw new Error(`第 ${idx + 1} 跳节点缺少匹配协议族的可用地址。`);
          const v6 = isIPv6(hop.address);
          if (baseIs6 === null) baseIs6 = v6;
          else if (v6 !== baseIs6) throw new Error("全链路节点必须使用相同 IP 地址族。");
          return {
            agent_id: item.agent_id,
            instance_id: item.instance_id || "",
            name: item.name,
            address: hop.address,
          };
        });
        payload = {
          name: pair?.name || "",
          group_name: String(values.group_name || "").trim(),
          target_mode: "",
          node1: endpoints[0],
          node2: endpoints[endpoints.length - 1],
          nodes: endpoints,
          bidirectional: Boolean(formEl.elements.bidirectional.checked),
        };
      }

      payload.parameters = {
        tool: "ping",
        protocol: "icmp",
        count: Number(values.count),
        interval_ms: Number(values.interval_ms),
        timeout_ms: Number(values.timeout_ms),
        size: Number(values.size),
      };
      payload.auto_enabled = Boolean(formEl.elements.auto_enabled.checked);
      payload.auto_interval_seconds = Number(values.auto_interval_seconds);
      if (isEdit) payload.revision = Number(pair.revision || 0);

      await ctx.api(isEdit ? `/dashboard/probes/${pathID(pair.id)}` : "/dashboard/probes", {
        method: isEdit ? "PUT" : "POST",
        body: payload,
      });
      if (!ctx.current() || !box?.open) return;
      box.close();
      await reloadProbes();
      ctx.announce(isEdit ? "探测链路已更新" : "探测链路已添加");
    });

    box = ctx.dialog(isEdit ? "编辑链路" : "添加分组", form, { wide: true });
    dialogs.add(box);
    box.addEventListener("close", () => { dialogLifetime.abort(); dialogs.delete(box); }, { once: true });
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;

    const populateNodes = (nodes) => {
      if (!ctx.current() || !box.open) return;
      nodesReady = true;
      submit.disabled = false;
      const diagnosticNodes = list(nodes);
      const orderIDs = getOrderIDs();
      orderedNodes = orderNodesBySavedOrder(
        diagnosticNodes.map((item) => ({ ...item, id: item.agent_id })),
        orderIDs,
      );
      nodeByKey.clear();
      orderedNodes.forEach((item) => nodeByKey.set(nodeKey(item), item));
      const srcItems = nodeOptionItems(new Set());
      syncOptions(ipSrcNodeSelect, srcItems.length ? srcItems : [["", "暂无已接入诊断节点", true]], hopsState[0]?.key || "");
      syncCustomIPSrcAddrs();
      renderChainRows();
      readHint.hidden = true;
    };

    void ctx.api("/network-diagnostics/nodes", { signal: AbortSignal.any([ctx.signal, dialogLifetime.signal, AbortSignal.timeout(10000)]) })
      .then((nodes) => populateNodes(nodes))
      .catch((error) => {
        if (!ctx.current() || !box.open || error.name === "AbortError") return;
        readHint.textContent = `诊断节点读取失败：${error.message}`;
        submit.disabled = true;
      });
  }

  async function mutatePair(pair, btn, action) {
    if (!canExecute() || inFlightOps.has(pair.id)) return;
    if (action === "delete") {
      if (!await ctx.confirm(`删除探测链路“${pair.name || pair.node1?.name}”及其历史采样？`, "删除链路")) return;
    }
    inFlightOps.add(pair.id);
    onChange();
    try {
      await ctx.mutate(btn, async () => {
        if (action === "run") {
          await ctx.api(`/dashboard/probes/${pathID(pair.id)}/run`, { method: "POST" });
          ctx.announce("已发起探测");
        } else if (action === "stop") {
          await ctx.api(`/dashboard/probes/${pathID(pair.id)}/stop`, { method: "POST" });
          ctx.announce("已发送停止指令");
        } else if (action === "toggle") {
          await ctx.api(`/dashboard/probes/${pathID(pair.id)}`, {
            method: "PUT",
            body: {
              name: pair.name || "",
              group_name: pair.group_name || "",
              target_mode: pair.target_mode || "",
              node1: pair.node1,
              node2: pair.node2,
              ...(pair.target_mode !== "ip" && Array.isArray(pair.nodes) && pair.nodes.length >= 2 ? { nodes: pair.nodes } : {}),
              bidirectional: Boolean(pair.bidirectional),
              parameters: pair.parameters,
              auto_enabled: !pair.auto_enabled,
              auto_interval_seconds: pair.auto_interval_seconds || 60,
              revision: Number(pair.revision || 0),
            },
          });
          ctx.announce(!pair.auto_enabled ? "已启用自动探测" : "已暂停自动探测");
        } else if (action === "delete") {
          await ctx.api(`/dashboard/probes/${pathID(pair.id)}`, { method: "DELETE" });
          ctx.announce("探测链路已删除");
        }
      });
    } finally {
      await reloadProbes();
      inFlightOps.delete(pair.id);
      onChange();
    }
  }


  return {
    canView: canView(),
    busy: id => inFlightOps.has(id),
    mutate: mutatePair,
    paint() {},
    open(id = "", options = {}) {
      if (!canExecute() || getProbesState() !== "ready") return;
      if (options.create) { openProbeDialog(); return; }
      const pair = (getProbes()?.pairs || []).find(item => item.id === id);
      if (pair && !inFlightOps.has(id)) openProbeDialog(pair);
    },
  };
}
