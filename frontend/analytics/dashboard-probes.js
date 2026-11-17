import { isValidProbeIPAddress } from "../modules/dashboard-model.js";
import { orderNodesBySavedOrder } from "../modules/node-order.js";
import { badge, button, field, list, muted, node, pathID } from "./ui.js";

const nodeKey = (endpoint) => `${endpoint?.agent_id || ""}/${endpoint?.instance_id || ""}`;
const isIPv6 = (addr) => String(addr || "").includes(":");

export function createDashboardProbeController(ctx, { getOrderIDs, getProbes, reloadProbes, onSaved, onDeleted }) {
  const canExecute = () => ctx.can("tasks.execute");
  const canView = () => ctx.can("agents.read") && ctx.can("tasks.read");
  const inFlightOps = new Set();
  let diagnosticNodes = [];
  let activeFormDialog = null;

  ctx.signal.addEventListener("abort", () => {
    activeFormDialog?.close();
    activeFormDialog = null;
  }, { once: true });

  function openProbeDialog(pair = null, opts = {}) {
    if (!canExecute()) return;
    const MAX_HOPS = 10;
    const isEdit = Boolean(pair?.id);
    const params = pair?.parameters || {};
    const initialMode = pair?.target_mode === "ip" ? "ip" : "node";

    const existingNodes = Array.isArray(pair?.nodes) && pair.nodes.length >= 2
      ? pair.nodes
      : pair
        ? [pair.node1, pair.node2].filter(Boolean)
        : [];
    const hopsState = initialMode !== "ip" && existingNodes.length >= 2
      ? existingNodes.map((ep) => ({
          key: ep?.agent_id ? nodeKey(ep) : "",
          name: ep?.name || "",
          address: ep?.address || "",
        }))
      : [
          existingNodes[0]?.agent_id
            ? { key: nodeKey(existingNodes[0]), name: existingNodes[0].name || "", address: existingNodes[0].address || "" }
            : { key: "", name: "", address: "" },
          { key: "", name: "", address: "" },
        ];

    const occupiedGroupNames = () => new Set(
      (getProbes()?.pairs || [])
        .filter((item) => item?.id && item.id !== pair?.id)
        .map((item) => String(item?.group_name || "").trim())
        .filter(Boolean),
    );

    const groupInputWrap = field("监测分组", "group_name", pair?.group_name || opts?.groupName || "", {
      required: true,
      maxlength: 40,
      placeholder: "输入唯一分组名称，如 亚太核心专线",
      hint: "每个监测分组严格对应一条监测链路",
    });
    const groupInput = groupInputWrap.querySelector("input");
    const groupErrorEl = node("p", {
      className: "a-validation",
      id: `${groupInput.id}-dup-err`,
      role: "alert",
      hidden: true,
    });
    groupInputWrap.append(groupErrorEl);

    const checkGroupUniqueness = () => {
      const val = String(groupInput.value || "").trim();
      if (!val) {
        groupInput.setCustomValidity("");
        groupErrorEl.textContent = "";
        groupErrorEl.hidden = true;
        return true;
      }
      if (occupiedGroupNames().has(val)) {
        const msg = `监测分组“${val}”已存在链路，每个分组仅允许配置一条链路`;
        groupInput.setCustomValidity(msg);
        groupInput.setAttribute("aria-errormessage", groupErrorEl.id);
        groupErrorEl.textContent = msg;
        groupErrorEl.hidden = false;
        return false;
      }
      groupInput.setCustomValidity("");
      groupInput.removeAttribute("aria-errormessage");
      groupErrorEl.textContent = "";
      groupErrorEl.hidden = true;
      return true;
    };
    groupInput.addEventListener("input", checkGroupUniqueness);
    groupInput.addEventListener("change", checkGroupUniqueness);
    if (groupInput.value) checkGroupUniqueness();

    const nameInput = field("链路备注", "name", pair?.name || "", {
      maxlength: 80,
      placeholder: "选填，例如 主用低延迟路线",
    });
    const modeField = field("监测模式", "target_mode", initialMode, {
      options: [
        ["node", "托管节点链路 (2–10 跳)"],
        ["ip", "自定义目标 IP (单跳)"],
      ],
    });

    const ipSrcNodeField = field("源节点", "ip_src_key", hopsState[0]?.key || "", {
      options: hopsState[0]?.key ? [[hopsState[0].key, hopsState[0].name || hopsState[0].key]] : [["", "读取中…"]],
    });
    const ipSrcAddrField = field("源地址", "ip_src_addr", hopsState[0]?.address || "", {
      options: hopsState[0]?.address ? [[hopsState[0].address, hopsState[0].address]] : [["", "请选择"]],
    });
    const customIPField = field("目标 IP", "node2_ip", pair && initialMode === "ip" ? pair?.node2?.address || "" : "", {
      placeholder: "IPv4 / IPv6 单播地址",
    });

    const bidirField = field("双向测量（去程与回程）", "bidirectional", pair ? Boolean(pair.bidirectional) : true, {
      type: "checkbox",
      hint: "同时实测各跳去程与回程的延迟、抖动与丢包率",
    });

    const countField = field("探测次数", "count", params.count || 5, { type: "number", min: 1, max: 100, required: true });
    const intervalField = field("间隔 (ms)", "interval_ms", params.interval_ms || 200, { type: "number", min: 200, max: 5000, required: true });
    const timeoutField = field("超时 (ms)", "timeout_ms", params.timeout_ms || 1000, { type: "number", min: 100, max: 5000, required: true });
    const sizeField = field("负载 (B)", "size", params.size ?? 56, { type: "number", min: 0, max: 1400, required: true });
    const autoEnabledField = field("启用自动周期探测", "auto_enabled", pair ? Boolean(pair.auto_enabled) : true, { type: "checkbox" });
    const autoSecField = field("探测周期 (s)", "auto_interval_seconds", pair?.auto_interval_seconds || 60, { type: "number", min: 15, max: 3600, required: true });
    const readHint = muted("读取诊断节点列表中…");

    const modeSelect = modeField.querySelector("select");
    const ipSrcNodeSelect = ipSrcNodeField.querySelector("select");
    const ipSrcAddrSelect = ipSrcAddrField.querySelector("select");
    const ipInput = customIPField.querySelector("input");
    const bidirInput = bidirField.querySelector("input");

    const chainCountBadge = badge(`${hopsState.length} / ${MAX_HOPS} 节点`);
    const hopListHost = node("div", { className: "a-dash-probe-hop-list" });
    const chainPreview = node("p", { className: "a-muted a-dash-probe-chain-preview" });
    let addHopBtn;
    const chainBuilder = node("div", { className: "a-dash-probe-chain-builder" },
      node("div", { className: "a-dash-probe-chain-head" },
        node("div", { className: "a-dash-probe-chain-title" },
          node("strong", {}, "链路节点顺序"),
          chainCountBadge,
        ),
        addHopBtn = button("+ 添加节点", () => {
          if (hopsState.length >= MAX_HOPS) return;
          const usedAgents = new Set(hopsState.map((h) => nodeByKey.get(h.key)?.agent_id || String(h.key || "").split("/")[0]).filter(Boolean));
          const candidate = orderedNodes.find((item) => !usedAgents.has(item.agent_id)) || null;
          hopsState.push({
            key: candidate ? nodeKey(candidate) : "",
            name: candidate?.name || "",
            address: "",
          });
          renderChainRows();
        }, { className: "a-button", "data-refresh-key": "add-hop" }),
      ),
      hopListHost,
      chainPreview,
    );
    const customIPGrid = node("div", { className: "a-form-grid" },
      ipSrcNodeField,
      ipSrcAddrField,
      customIPField,
    );

    let orderedNodes = [];
    let nodesLoaded = false;
    const nodeByKey = new Map();

    const syncOptions = (selectEl, items, preferred) => {
      const btn = selectEl.querySelector("button");
      const optsList = items.map(([val, label, disabled = false]) => node("option", { value: val, disabled }, label));
      selectEl.replaceChildren(...(btn ? [btn] : []), ...optsList);
      if (items.some(([val, , dis]) => val === preferred && !dis)) selectEl.value = preferred;
      else selectEl.value = items.find(([, , dis]) => !dis)?.[0] || "";
    };

    const nodeOptionItems = (excludeAgentIDs, currentHop) => {
      const items = orderedNodes
        .filter((item) => !excludeAgentIDs.has(item.agent_id))
        .map((item) => {
          const key = nodeKey(item);
          const suffix = !item.online
            ? " · 离线"
            : item.instance_id && item.instance_id !== "main"
              ? ` · ${item.context || item.instance_id}`
              : "";
          return [key, `${item.name}${suffix}`, false];
        });
      if (currentHop?.key && !items.some(([val]) => val === currentHop.key)) {
        const label = currentHop.name
          ? `${currentHop.name}（${nodesLoaded ? "当前不可访问/未授权" : "读取中"}）`
          : currentHop.key;
        items.unshift([currentHop.key, label, false]);
      }
      return items;
    };

    const compatibleAddrsForHop = (agentItem, baseAddr) => {
      const allAddrs = agentItem?.capabilities?.addresses || [];
      if (!baseAddr) return allAddrs;
      return allAddrs.filter((addr) => isIPv6(addr) === isIPv6(baseAddr));
    };

    const updatePreviewText = () => {
      const names = hopsState.map((hop, idx) => {
        const item = nodeByKey.get(hop.key);
        return item?.name || hop.name || existingNodes[idx]?.name || `节点 ${idx + 1}`;
      });
      const bidir = !bidirInput.disabled && bidirInput.checked;
      chainPreview.textContent = bidir
        ? `去程: ${names.join(" → ")} ｜ 回程: ${[...names].reverse().join(" → ")}`
        : `去程: ${names.join(" → ")}`;
      chainCountBadge.textContent = `${hopsState.length} / ${MAX_HOPS} 节点`;
      if (addHopBtn) {
        const distinctAgentCount = new Set(orderedNodes.map((item) => item.agent_id).filter(Boolean)).size;
        const limitReached = hopsState.length >= MAX_HOPS || (nodesLoaded && hopsState.length >= distinctAgentCount);
        addHopBtn.disabled = limitReached;
        addHopBtn.title = limitReached ? "已达到可用节点数或 10 跳上限" : "添加中继节点";
      }
    };

    const renderChainRows = () => {
      const rows = hopsState.map((hop, idx) => {
        const otherAgents = new Set(
          hopsState
            .filter((_, i) => i !== idx)
            .map((h) => nodeByKey.get(h.key)?.agent_id || String(h.key || "").split("/")[0])
            .filter(Boolean),
        );
        const availableItems = nodeOptionItems(otherAgents, hop);
        const nodeSelectID = `a-probe-hop-node-${idx}`;
        const nodeSelect = node("select", { id: nodeSelectID, name: `hop_node_${idx}`, "data-hop-index": String(idx), required: modeSelect.value !== "ip" });
        nodeSelect.prepend(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")));
        syncOptions(
          nodeSelect,
          availableItems.length ? availableItems : [["", nodesLoaded ? "无可用节点" : "读取中…", true]],
          hop.key,
        );
        if (nodeSelect.value && nodeByKey.has(nodeSelect.value)) {
          hop.key = nodeSelect.value;
          hop.name = nodeByKey.get(nodeSelect.value)?.name || hop.name;
        }

        const addrSelectID = `a-probe-hop-addr-${idx}`;
        const addrSelect = node("select", { id: addrSelectID, name: `hop_addr_${idx}`, "data-hop-addr-index": String(idx), required: modeSelect.value !== "ip" });
        addrSelect.prepend(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")));

        const syncRowAddrs = (baseAddr) => {
          const item = nodeByKey.get(hop.key);
          const allAddrs = item?.capabilities?.addresses || [];
          const compat = idx === 0 ? allAddrs : compatibleAddrsForHop(item, baseAddr);
          let addrItems;
          if (compat.length) {
            addrItems = compat.map((addr) => [addr, addr, false]);
          } else if (hop.address && (!nodesLoaded || !item)) {
            addrItems = [[hop.address, hop.address, false]];
          } else {
            addrItems = [["", allAddrs.length ? "无匹配 IP 协议族地址" : (nodesLoaded ? "节点无可用探测地址" : "请选择"), true]];
          }
          syncOptions(addrSelect, addrItems, hop.address);
          if (addrSelect.value) hop.address = addrSelect.value;
          else if (nodesLoaded) hop.address = "";
        };

        const baseAddr = idx === 0 ? "" : hopsState[0]?.address || "";
        syncRowAddrs(baseAddr);

        nodeSelect.addEventListener("change", () => {
          hop.key = nodeSelect.value;
          hop.name = nodeByKey.get(hop.key)?.name || "";
          hop.address = "";
          renderChainRows();
        });
        addrSelect.addEventListener("change", () => {
          hop.address = addrSelect.value;
          if (idx === 0) renderChainRows();
          else updatePreviewText();
        });

        const roleLabel = idx === 0 ? "起点" : idx === hopsState.length - 1 ? "终点" : `中继 ${idx}`;
        return node("div", { className: "a-dash-probe-hop-row", "data-hop-row": String(idx) },
          node("span", { className: "a-badge a-dash-probe-hop-idx" }, roleLabel),
          node("div", { className: "a-dash-probe-hop-fields" },
            node("div", { className: "a-field" },
              node("label", { className: "a-field-label", for: nodeSelectID }, idx === 0 ? "起点节点" : idx === hopsState.length - 1 ? "终点节点" : `中继节点 ${idx}`),
              nodeSelect,
            ),
            node("div", { className: "a-field" },
              node("label", { className: "a-field-label", for: addrSelectID }, "探测地址"),
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
            }, { className: "a-button", disabled: hopsState.length <= 2, title: "移除该跳" }),
          ),
        );
      });
      hopListHost.replaceChildren(...rows);
      updatePreviewText();
    };

    const syncCustomIPSrcAddrs = () => {
      const n1 = nodeByKey.get(ipSrcNodeSelect.value);
      const addrs = n1?.capabilities?.addresses || [];
      const fallbackAddr = (!nodesLoaded || !n1) && hopsState[0]?.address ? [[hopsState[0].address, hopsState[0].address, false]] : null;
      const items = addrs.length
        ? addrs.map((addr) => [addr, addr, false])
        : (fallbackAddr || [["", nodesLoaded ? "节点无可用探测地址" : "请选择", true]]);
      syncOptions(ipSrcAddrSelect, items, ipSrcAddrSelect.value || hopsState[0]?.address);
    };
    ipSrcNodeSelect.addEventListener("change", syncCustomIPSrcAddrs);

    let savedBidir = pair ? Boolean(pair.bidirectional) : true;
    const syncMode = (isToggle = false) => {
      const custom = modeSelect.value === "ip";
      chainBuilder.hidden = custom;
      customIPGrid.hidden = !custom;
      ipSrcNodeSelect.required = custom;
      ipSrcAddrSelect.required = custom;
      ipInput.required = custom;
      bidirField.hidden = custom;
      if (custom) {
        if (isToggle && !bidirInput.disabled) savedBidir = bidirInput.checked;
        bidirInput.checked = false;
        bidirInput.disabled = true;
      } else {
        bidirInput.disabled = false;
        if (isToggle) bidirInput.checked = savedBidir;
      }
      renderChainRows();
    };

    bidirInput.addEventListener("change", () => {
      if (!bidirInput.disabled) savedBidir = bidirInput.checked;
      updatePreviewText();
    });
    modeSelect.addEventListener("change", () => syncMode(true));
    syncMode(false);

    let box;
    const form = ctx.form([
      readHint,
      node("div", { className: "a-form-grid" }, groupInputWrap, nameInput, modeField),
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
    ], isEdit ? "保存修改" : "创建监测组", async (values, formEl) => {
      const groupName = String(values.group_name || "").trim();
      if (!groupName) {
        throw Object.assign(new Error("请输入监测分组名称。"), { field: "group_name" });
      }
      if (occupiedGroupNames().has(groupName)) {
        throw Object.assign(new Error(`监测分组“${groupName}”已存在链路，每个分组仅允许配置一条链路。`), { field: "group_name" });
      }

      const custom = values.target_mode === "ip";
      let payload;
      if (custom) {
        const n1 = nodeByKey.get(values.ip_src_key);
        if (!n1) throw Object.assign(new Error("请选择有效的可用源节点。"), { field: "ip_src_key" });
        if (!values.ip_src_addr) throw Object.assign(new Error("请选择源节点的探测地址。"), { field: "ip_src_addr" });
        const ip = String(values.node2_ip || "").trim();
        if (!isValidProbeIPAddress(ip)) throw Object.assign(new Error("请输入有效的 IPv4 或 IPv6 单播目标地址。"), { field: "node2_ip" });
        if (isIPv6(ip) !== isIPv6(values.ip_src_addr)) throw Object.assign(new Error("目标 IP 须与源地址使用相同 IP 地址族。"), { field: "node2_ip" });
        payload = {
          name: String(values.name || "").trim(),
          group_name: groupName,
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
        if (hopsState.length < 2) throw new Error("链路至少需要 2 个节点。");
        if (hopsState.length > MAX_HOPS) throw new Error(`单条链路最多支持 ${MAX_HOPS} 个节点。`);
        const seenAgents = new Set();
        let baseIs6 = null;
        const endpoints = hopsState.map((hop, idx) => {
          const item = nodeByKey.get(hop.key);
          if (!item) throw Object.assign(new Error(`第 ${idx + 1} 跳节点无效或当前无权限访问，请重新选择。`), { field: `hop_node_${idx}` });
          if (seenAgents.has(item.agent_id)) throw Object.assign(new Error("同一条链路中不能重复选择相同节点。"), { field: `hop_node_${idx}` });
          seenAgents.add(item.agent_id);
          if (!hop.address) throw Object.assign(new Error(`第 ${idx + 1} 跳节点缺少匹配协议族的可用探测地址。`), { field: `hop_addr_${idx}` });
          const v6 = isIPv6(hop.address);
          if (baseIs6 === null) baseIs6 = v6;
          else if (v6 !== baseIs6) throw Object.assign(new Error("全链路节点必须使用相同 IP 地址族。"), { field: `hop_addr_${idx}` });
          return {
            agent_id: item.agent_id,
            instance_id: item.instance_id || "",
            name: item.name,
            address: hop.address,
          };
        });
        payload = {
          name: String(values.name || "").trim(),
          group_name: groupName,
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

      let saved;
      try {
        saved = await ctx.api(isEdit ? `/dashboard/probes/${pathID(pair.id)}` : "/dashboard/probes", {
          method: isEdit ? "PUT" : "POST",
          body: payload,
        });
      } catch (error) {
        if (error?.status === 409) {
          void reloadProbes?.();
          if (String(error.message || "").includes("监测分组")) {
            error.field = "group_name";
          }
        }
        throw error;
      }
      if (!ctx.current() || !box?.open) return;
      box.close();
      await onSaved?.(saved, { isEdit, previousGroupName: pair?.group_name || "" });
      await reloadProbes();
      ctx.announce(isEdit ? "监测分组已更新" : "监测分组已创建");
    });

    box = ctx.dialog(isEdit ? `编辑监测分组 · ${pair.group_name || pair.name}` : "新建监测分组", form, { wide: true });
    activeFormDialog = box;
    box.addEventListener("close", () => {
      if (activeFormDialog === box) activeFormDialog = null;
    }, { once: true });

    const populateNodes = (nodes) => {
      if (!ctx.current() || !box.open) return;
      nodesLoaded = true;
      diagnosticNodes = list(nodes);
      const orderIDs = getOrderIDs();
      orderedNodes = orderNodesBySavedOrder(
        diagnosticNodes.map((item) => ({ ...item, id: item.agent_id })),
        orderIDs,
      );
      nodeByKey.clear();
      orderedNodes.forEach((item) => nodeByKey.set(nodeKey(item), item));
      const srcItems = nodeOptionItems(new Set(), hopsState[0]);
      syncOptions(ipSrcNodeSelect, srcItems.length ? srcItems : [["", "暂无可用诊断节点", true]], hopsState[0]?.key || "");
      syncCustomIPSrcAddrs();
      renderChainRows();
      readHint.hidden = true;
    };

    void ctx.api("/network-diagnostics/nodes", { signal: ctx.signal })
      .then((nodes) => populateNodes(nodes))
      .catch((error) => {
        if (!ctx.current() || !box.open || error.name === "AbortError") return;
        readHint.textContent = `诊断节点读取失败：${error.message}`;
        if (diagnosticNodes.length) populateNodes(diagnosticNodes);
      });
  }

  async function mutatePair(pair, btn, action) {
    if (!canExecute() || !pair?.id || inFlightOps.has(pair.id)) return;
    const label = pair.group_name || pair.name || pair.id;
    if (action === "delete") {
      if (!await ctx.confirm(`确认删除监测分组“${label}”及其全部历史采样记录？`, "删除监测分组")) return;
    }
    inFlightOps.add(pair.id);
    try {
      await ctx.mutate(btn, async () => {
        try {
          if (action === "run") {
            await ctx.api(`/dashboard/probes/${pathID(pair.id)}/run`, { method: "POST" });
            ctx.announce(`已启动“${label}”测量`);
          } else if (action === "stop") {
            await ctx.api(`/dashboard/probes/${pathID(pair.id)}/stop`, { method: "POST" });
            ctx.announce(`已发送“${label}”停止指令`);
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
            ctx.announce(!pair.auto_enabled ? `已启用“${label}”自动探测` : `已暂停“${label}”自动探测`);
          } else if (action === "delete") {
            await ctx.api(`/dashboard/probes/${pathID(pair.id)}`, { method: "DELETE" });
            await onDeleted?.(pair);
            ctx.announce(`监测分组“${label}”已删除`);
          }
          await reloadProbes();
        } catch (error) {
          if (error?.status === 409) {
            void reloadProbes?.();
          }
          throw error;
        }
      });
    } finally {
      inFlightOps.delete(pair.id);
    }
  }

  return {
    canView: canView(),
    canExecute: canExecute(),
    openCreate: (options = {}) => openProbeDialog(null, options),
    openEdit: (pair) => openProbeDialog(pair),
    mutatePair,
    open(targetProbeID = "", options = {}) {
      if (options?.edit && targetProbeID) {
        const target = (getProbes()?.pairs || []).find((item) => item.id === targetProbeID);
        if (target) {
          openProbeDialog(target);
          return;
        }
      }
      openProbeDialog(null, options);
    },
  };
}
