import {
  node,
  button,
  actions,
  stack,
  grid,
  section,
  field,
  muted,
  badge,
  notice,
  code,
  table,
} from "./ui.js";
import { patch, poll } from "./live-dom.js";
import { requestID } from "./request-id.js";
import { networkName, networkKind } from "./nexttrace-format.js";
import { downloadMeasurementImage } from "./network-tools-export.js";

const exportingImages = new Set();

const labels = {
  preparing: "准备中",
  running: "执行中",
  ready: "服务端已就绪",
  stopping: "停止中",
  cleanup_pending: "等待确认资源回收",
  succeeded: "测量结束",
  failed: "失败",
  canceled: "已停止",
};
const terminal = (state) => ["succeeded", "failed", "canceled"].includes(state);
const number = (value) =>
  Number.isFinite(Number(value))
    ? Number(value).toLocaleString("zh-CN", { maximumFractionDigits: 3 })
    : "—";
const providers = [
  ["disable-geoip", "不查询"],
  ["NextTrace-API", "NextTrace-API"],
  ["IP.SB", "IP.SB"],
  ["IPInfo", "IPInfo"],
  ["IPInsight", "IPInsight"],
  ["IPAPI.com", "IP-API.com"],
  ["IPInfoLocal", "IPInfoLocal"],
  ["ipdb.one", "ipdb.one"],
  ["chunzhen", "纯真"],
  ["DN42", "DN42"],
];

export function networkToolsPage(ctx) {
  let nodes,
    nodesSnapshot,
    reading = false,
    creating = false,
    snapshot,
    pending,
    grantEpoch = 0;
  const feedback = notice(""),
    results = node("div", { className: "a-stack" }),
    capability = muted("正在读取节点能力…");
  const requestedID = new URLSearchParams(
    location.hash.split("?")[1] || "",
  ).get("measurement");
  let history = [],
    historyTool = "", historyError = "",
    pinned,
    readingPinned = false;
  const controls = new Map(), recordChoices = new Map();
  let recordOptions = "", pinnedOpened = false;
  const recordPicker = field("测量时间", "measurement", "", { options: [] }),
    recordSelect = recordPicker.querySelector("select");
  recordSelect.addEventListener("change", () => {
    if (recordSelect.selectedIndex === 0) recordChoices.delete(value("tool"));
    else recordChoices.set(value("tool"), recordSelect.value);
    snapshot = undefined;
    renderSnapshot();
  });
  function f(label, name, value, attrs = {}) {
    const el = field(label, name, value, attrs);
    controls.set(name, el.querySelector("input,select"));
    return el;
  }
  const tool = f("工具", "tool", "ping", {
    options: [
      ["ping", "Ping"],
      ["traceroute", "NextTrace 路由追踪"],
      ["mtr", "NextTrace MTR"],
      ["iperf3", "iperf3"],
    ],
  });
  const protocol = f("协议", "protocol", "icmp", {
    options: [["icmp", "ICMP"]],
  });
  const n1 = f("节点1", "node1", "", { options: [["", "请选择"]] }),
    n2 = f("节点2", "node2", "", { options: [["", "请选择"]] });
  const a1 = f("节点1地址", "address1", "", { options: [["", "请选择"]] }),
    a2 = f("节点2地址", "address2", "", { options: [["", "请选择"]] });
  const targetMode = f("目标类型", "target_mode", "", {
    options: [
      ["", "面板节点"],
      ["ip", "自定义 IP"],
    ],
  });
  const customIP = f("目标 IP", "target_ip", "", {
    placeholder: "IPv4 或 IPv6",
    maxlength: 45,
  });
  const externalHint = muted("连接已有 iperf3 服务端；面板仅停止本次客户端。");
  const targetColumn = stack(n2, a2, customIP, externalHint);
  const provider = f("IP 查询提供方", "provider", "NextTrace-API", {
    options: providers,
  });
  const count = f("次数 / 轮数", "count", 5, {
    type: "number",
    min: 1,
    max: 100,
  });
  const interval = f("间隔（ms）", "interval_ms", 1000, {
    type: "number",
    min: 200,
    max: 5000,
  });
  const timeout = f("单次超时（ms）", "timeout_ms", 1000, {
    type: "number",
    min: 100,
    max: 5000,
  });
  const hops = f("最大跳数", "max_hops", 30, {
    type: "number",
    min: 1,
    max: 64,
  });
  const size = f("ICMP 负载（字节）", "size", 64, {
    type: "number",
    min: 0,
    max: 1400,
  });
  const port = f("目标端口", "port", 5201, {
    type: "number",
    min: 1,
    max: 65535,
  });
  const duration = f("时长（s）", "duration", 10, {
    type: "number",
    min: 1,
    max: 60,
  });
  const streams = f("并发流", "streams", 1, { type: "number", min: 1, max: 4 });
  const forward = button("开始测试", () => void submit(false), {
      disabled: true,
      tone: "primary",
    }),
    both = button("双向追踪", () => void submit(true), { disabled: true });
  const swap = button(
    "交换两端",
    () => {
      const previous = [value("address1"), value("address2")];
      const a = value("node1");
      controls.get("node1").value = value("node2");
      controls.get("node2").value = a;
      for (const i of [1, 2]) {
        addresses(i);
        const select = controls.get(`address${i}`), address = previous[2 - i];
        if ([...select.options].some(option => option.value === address) && address)
          select.value = address;
      }
      update();
    },
    { glyph: "sync" },
  );
  const parameterGrid = node(
    "div",
    { className: "a-form-grid" },
    count,
    duration,
    port,
    provider,
    size,
    interval,
    timeout,
    hops,
    streams,
  );
  const form = node(
    "form",
    { className: "a-stack a-card-body", onsubmit: (e) => e.preventDefault() },
    node("div", { className: "a-form-grid" }, tool, protocol, targetMode),
    actions(node("h3", {}, "测试两端"), swap),
    grid(stack(n1, a1), targetColumn),
    capability,
    node("h3", {}, "测试参数"),
    parameterGrid,
    actions(forward, both),
    feedback,
  );
  function value(name) {
    return controls.get(name).value;
  }
  function selected(i) {
    return nodes?.find((n) => key(n) === value(`node${i}`));
  }
  function key(n) {
    return `${n.agent_id}/${n.instance_id || ""}`;
  }
  function options(select, items) {
    select.replaceChildren(
      node(
        "button",
        { type: "button", tabindex: "-1" },
        node("selectedcontent"),
      ),
      ...items.map(([v, t]) => node("option", { value: v }, t)),
    );
  }
  function addresses(i) {
    const n = selected(i),
      select = controls.get(`address${i}`),
      old = select.value,
      ips = n?.capabilities?.addresses || [];
    options(select, [
      ["", "请选择"],
      ...ips.map((ip) => [ip, ip]),
    ]);
    select.value = old && ips.includes(old)
      ? old
      : ips.find((ip) => !ip.includes(":")) || ips[0] || "";
  }
  function update() {
    const custom = value("target_mode") === "ip";
    n2.hidden = a2.hidden = custom;
    customIP.hidden = !custom;
    externalHint.hidden = !custom || value("tool") !== "iperf3";
    swap.hidden = custom;
    const t = value("tool"),
      p = value("protocol"),
      trace = ["traceroute", "mtr"].includes(t);
    parameterGrid.className = t === "ping" ? "a-grid" : "a-form-grid";
    count
      .querySelector(".a-field-label")
      ?.replaceChildren(
        t === "mtr" ? "轮数" : t === "traceroute" ? "每跳探测次数" : "次数",
      );
    if (t === "mtr") controls.get("count").removeAttribute("max");
    else controls.get("count").max = "100";
    provider.hidden = !trace;
    hops.hidden = !trace;
    port.hidden = p === "icmp";
    controls.get("port").min = t === "iperf3" ? 1024 : 1;
    duration.hidden = streams.hidden = t !== "iperf3";
    count.hidden = interval.hidden = timeout.hidden = t === "iperf3";
    size
      .querySelector(".a-field-label")
      ?.replaceChildren(
        t === "ping"
          ? "ICMP 负载（字节）"
          : trace
            ? "总包长（字节）"
            : p === "udp"
              ? "UDP 数据报（字节）"
              : "缓冲区（字节）",
      );
    controls.get("size").min =
      t === "ping"
        ? 0
        : trace
          ? (value("address1").includes(":") ? (p === "udp" ? 22 : 20) : 0) +
            (p === "tcp" ? 44 : 28)
          : p === "udp"
            ? 16
            : 1;
    controls.get("size").max =
      t === "ping" ? 1400 : trace ? 1500 : p === "udp" ? 1400 : 1048576;
    both.hidden = !trace || custom;
    both.textContent = t === "mtr" ? "双向 MTR" : "双向追踪";
    const pair = [selected(1), selected(2)],
      reasons = [];
    for (let i = 0; i < (custom ? 1 : 2); i++) {
      const n = pair[i];
      if (!n) {
        reasons.push(`请选择节点${i + 1}`);
        continue;
      }
      if (!n.online) reasons.push(`${n.name} 离线`);
      const name = trace ? "nexttrace" : t,
        tool = n.capabilities?.tools?.[name];
      if (!tool?.version || tool.error)
        reasons.push(
          `${n.name}：${tool?.error || "请升级 Agent 并准备诊断工具"}`,
        );
      if (trace) {
        const all = n.capabilities?.providers || {},
          reason = Object.hasOwn(all, value("provider"))
            ? all[value("provider")]
            : "未上报查询能力";
        if (reason) reasons.push(`${n.name}：${reason}`);
      }
    }
    if (!custom && pair[0] && pair[1] && pair[0].agent_id === pair[1].agent_id)
      reasons.push("请选择两个不同节点");
    const targetAddress = custom
      ? value("target_ip").trim()
      : value("address2");
    if (custom && targetAddress && !/^[0-9a-fA-F:.]+$/.test(targetAddress))
      reasons.push("请填写 IPv4 或 IPv6 地址");
    if (!value("address1") || !targetAddress)
      reasons.push("请选择两端实际地址");
    if (
      value("address1") &&
      targetAddress &&
      value("address1").includes(":") !== targetAddress.includes(":")
    )
      reasons.push("两端必须使用同一地址族");
    const visibleReasons = reasons.filter(
      (reason) => !reason.startsWith("请选择"),
    );
    capability.textContent = nodes
      ? visibleReasons.join("；") ||
        ((custom ? [pair[0]] : pair).every(Boolean) &&
        pair.some((n) => n?.instance_id)
          ? `使用宿主机网络${t === "iperf3" ? "；测速按流量上限预留共享额度" : ""}。`
          : "")
      : "正在读取节点能力…";
    capability.hidden = !capability.textContent;
    forward.disabled = both.disabled =
      !nodes || creating || reasons.length > 0 || !ctx.can("tasks.execute");
  }
  controls.get("tool").addEventListener("change", () => {
    const t = value("tool");
    options(
      controls.get("protocol"),
      (t === "ping"
        ? ["icmp"]
        : t === "iperf3"
          ? ["tcp", "udp"]
          : ["icmp", "tcp", "udp"]
      ).map((p) => [p, p.toUpperCase()]),
    );
    controls.get("count").value = t === "traceroute" ? 3 : t === "mtr" ? 100 : 5;
    controls.get("size").value = t === "iperf3" ? 131072 : t === "traceroute" ? 28 : 64;
    controls.get("port").value = t === "iperf3" ? 5201 : 80;
    update();
    renderSnapshot();
    void loadResults();
  });
  controls.get("protocol").addEventListener("change", () => {
    if (value("tool") === "iperf3")
      controls.get("size").value = value("protocol") === "udp" ? 1200 : 131072;
    if (value("tool") === "traceroute") {
      const ipv6 = value("address1").includes(":"), p = value("protocol");
      controls.get("size").value = (p === "tcp" ? 44 : 28) + (ipv6 ? p === "udp" ? 22 : 20 : 0);
      controls.get("port").value = p === "udp" ? 33494 : 80;
    }
    update();
  });
  for (const i of [1, 2])
    controls.get(`node${i}`).addEventListener("change", () => {
      addresses(i);
      update();
      if (value("tool") === "traceroute")
        controls.get("size").value = Math.max(Number(value("size")), Number(controls.get("size").min));
    });
  controls.get("address1").addEventListener("change", () => {
    update();
    if (value("tool") === "traceroute")
      controls.get("size").value = Math.max(Number(value("size")), Number(controls.get("size").min));
  });
  form.addEventListener("change", update);
  async function loadNodes() {
    try {
      const data = await ctx.api("/network-diagnostics/nodes");
      if (!ctx.current()) return;
      const nextNodes = JSON.stringify(data);
      if (nextNodes === nodesSnapshot) return;
      nodesSnapshot = nextNodes;
      nodes = data;
      for (const i of [1, 2]) {
        const select = controls.get(`node${i}`),
          old = select.value;
        options(select, [
          ["", "请选择"],
          ...nodes.map((n) => [
            key(n),
            `${n.name}${n.instance_id ? " · 共享实例" : ""}${n.online ? "" : " · 离线"}`,
          ]),
        ]);
        select.value = old;
        addresses(i);
      }
      update();
    } catch (e) {
      if (ctx.current()) {
        nodes = undefined;
        nodesSnapshot = undefined;
        if (e.status === 401 || e.status === 403)
          for (const name of ["node1", "node2", "address1", "address2"])
            options(controls.get(name), [["", "请选择"]]);
        capability.textContent = e.message;
        forward.disabled = both.disabled = true;
      }
    }
  }
  async function submit(bidirectional) {
    if (creating) return;
    const endpoint = (i) => ({
      agent_id: selected(i)?.agent_id,
      instance_id: selected(i)?.instance_id || "",
      name: selected(i)?.name,
      address: value(`address${i}`),
    });
    const parameters = Object.fromEntries(
      [
        "tool",
        "protocol",
        "provider",
        "count",
        "interval_ms",
        "timeout_ms",
        "max_hops",
        "size",
        "port",
        "duration",
        "streams",
      ].map((k) => [
        k,
        ["tool", "protocol", "provider"].includes(k)
          ? value(k)
          : Number(value(k)),
      ]),
    );
    parameters.bandwidth = 0;
    const body = {
      target_mode: value("target_mode"),
      node1: endpoint(1),
      node2:
        value("target_mode") === "ip"
          ? {
              agent_id: "",
              instance_id: "",
              name: "",
              address: value("target_ip").trim(),
            }
          : endpoint(2),
      parameters,
      bidirectional,
    };
    const signature = JSON.stringify(body);
    if (!pending || pending.signature !== signature)
      pending = {
        signature,
        body: { ...body, request_id: requestID() },
      };
    if (!form.reportValidity()) return;
    creating = true;
    update();
    feedback.textContent = "";
    try {
      await ctx.api("/network-diagnostics", {
        method: "POST",
        body: pending.body,
      });
      pending = null;
      recordChoices.delete(body.parameters.tool);
      await loadResults();
    } catch (e) {
      if (ctx.current()) {
        feedback.textContent = e.message;
        await loadResults();
      }
    } finally {
      creating = false;
      if (ctx.current()) update();
    }
  }
  function render(items) {
    const fresh = node(
      "div",
      { className: "a-stack" },
      items.length
        ? items.map((d) => {
            const req = d.request,
              tool = req.parameters.tool,
              params = req.parameters;
            const name = {
              ping: "Ping",
              traceroute: "NextTrace",
              mtr: "NextTrace MTR",
              iperf3: "iperf3",
            }[tool];
            const measurements = Object.values(d.results || {}).filter(
              (r) => !r.id.endsWith("-server"),
            );
            const server = d.results?.[d.id + "-server"];
            const summary = actions(
              node("h3", {}, name),
              badge(params.protocol.toUpperCase()),
              badge(labels[d.state] || d.state),
              req.bidirectional && badge("双向"),
              tool === "iperf3" && server && badge(server.cleaned ? "服务端已回收" : server.state === "ready" ? "服务端就绪" : "服务端准备中"),
            );
            const stop =
              !terminal(d.state) &&
              button(
                "停止测试",
                async () => {
                  try {
                    await ctx.api(
                      `/network-diagnostics/${encodeURIComponent(d.id)}/stop`,
                      { method: "POST" },
                    );
                    await loadResults();
                  } catch (e) {
                    feedback.textContent = e.message;
                  }
                },
                { disabled: d.state === "stopping" },
              );
            const retry =
              ["failed", "canceled"].includes(d.state) &&
              button("重试", async (event) => {
                const control = event.currentTarget;
                control.disabled = true;
                try {
                  await ctx.api(`/tasks/${encodeURIComponent(d.id)}/retry`, {
                    method: "POST",
                    body: {},
                  });
                  recordChoices.delete(tool);
                  await loadResults();
                } catch (e) {
                  feedback.textContent = e.message;
                  await loadResults();
                } finally {
                  if (control.isConnected) control.disabled = false;
                }
              });
            const details = node(
              "div",
              {
                className: "a-stack",
                "data-refresh-key": d.id,
              },
              summary,
              node(
                "div",
                { className: "a-stack" },
                actions(stop, retry,
                  button("导出图片", async (event) => {
                    const control=event.currentTarget;
                    const record=control.closest("[data-refresh-key]");
                    if (exportingImages.has(d.id)) return;
                    exportingImages.add(d.id);
                    const hidden=record.querySelector('input[name="export-sensitive"]').checked;
                    control.disabled=true;
                    try { await downloadMeasurementImage(d, record, hidden, () => ctx.current()); }
                    catch(e) { feedback.textContent=e.message; }
                    finally { exportingImages.delete(d.id); if(control.isConnected) control.disabled=false; }
                  }, {disabled:exportingImages.has(d.id)}),
                  field("隐藏敏感信息", "export-sensitive", true, {type:"checkbox", title:"隐藏节点名称、IP、主机名及诊断原文"}),
                ),
                node("details", {className:"a-query-options"},
                  node("summary", {}, "测试参数"),
                  table(["参数", "设置"], [
                    ...(tool === "iperf3" ? [["时长", `${params.duration} s`], ["并发流", params.streams], ["目标端口", params.port], ["数据长度", `${params.size} 字节`]] : [[tool === "mtr" ? "轮数" : "探测次数", params.count], ["间隔", `${params.interval_ms} ms`], ["单次超时", `${params.timeout_ms} ms`], ["包大小", `${params.size} 字节`], ...(["mtr", "traceroute"].includes(tool) ? [["最大跳数", params.max_hops], ["IP 查询提供方", params.provider]] : [])]),
                    ...(d.byte_limit>0 ? [["共享流量预算", `${number(d.byte_limit/1048576)} MiB`]] : []),
                  ], {paginate:false}),
                ),
                server?.error &&
                  server.error !== "测量已停止" &&
                  notice(server.error, "error"),
                server?.stderr &&
                  node(
                    "details",
                    { className: "a-query-options" },
                    node("summary", {}, "服务端诊断"),
                    code(server.stderr),
                  ),
                tool === "iperf3" &&
                  req.target_mode !== "ip" &&
                  d.state === "failed" &&
                  !measurements.length &&
                  !server?.error &&
                  notice("接收端已结束，客户端未完成连接。", "error"),
                measurements.length
                  ? measurements.map((r) => {
                      const reverse = r.id.endsWith("-reverse");
                      const from = reverse ? req.node2 : req.node1,
                        to = reverse ? req.node1 : req.node2;
                      return section(
                        "",
                        node("div", { className: "a-card-body a-stack", "data-measurement-result": r.id },
                        actions(
                          node(
                            "h3",
                            {},
                            tool === "iperf3"
                              ? (r.state === "running" ? "测速中" : "测速结果")
                              : reverse
                                ? "回程"
                                : "去程",
                          ),
                          badge(labels[r.state] || r.state),
                        ),
                        grid(
                          stack(muted("来源"), node("strong", {}, from.name || from.address), from.name && muted(from.address)),
                          stack(muted("目标"), node("strong", {}, to.name || to.address), to.name && muted(to.address)),
                        ),
                        (r.error || r.output?.error) &&
                          r.error !== "测量已停止" &&
                          notice(r.error || r.output.error, "error"),
                        node("div", {className:"a-stack", "data-export-output":""},renderOutput(tool, r.output, r.state, params.count)),
                        (tool === "traceroute" || tool === "mtr") &&
                          params.provider !== "disable-geoip" &&
                          r.output &&
                          muted(querySummary(tool, r.output, params.provider)),
                        r.stderr &&
                          node(
                            "details",
                            { className: "a-query-options" },
                            node("summary", {}, "工具诊断"),
                            code(r.stderr),
                          ),
                        ),
                      );
                    })
                  : muted(
                      terminal(d.state) ? "未产生探测结果" : "等待 Agent 执行",
                    ),
              ),
            );
            return details;
          })
        : historyError ? notice(historyError, "error") : muted(historyTool === value("tool") ? "暂无测量记录" : "正在读取测量记录…"),
    );
    patch(results, fresh);
  }
  async function loadResults() {
    if (reading) return;
    reading = true;
    const requestedTool = value("tool");
    try {
      const data = await ctx.api(`/network-diagnostics?tool=${encodeURIComponent(requestedTool)}`);
      if (!ctx.current() || requestedTool !== value("tool")) return;
      history = data;
      historyTool = requestedTool;
      historyError = "";
      renderSnapshot();
    } catch (e) {
      if (ctx.current() && requestedTool === value("tool")) {
        feedback.textContent = e.message;
        historyError = e.message;
        if (e.status === 401 || e.status === 403) {
          history = [];
          pinned = undefined;
          recordChoices.clear();
          recordPicker.hidden = true;
          options(recordSelect, []);
          recordOptions = "";
          results.replaceChildren();
          snapshot = undefined;
        }
        renderSnapshot();
      }
    } finally {
      reading = false;
      if (ctx.current() && requestedTool !== value("tool")) void loadResults();
    }
  }
  function renderSnapshot() {
    const all = pinned
      ? [pinned, ...history.filter((item) => item.id !== pinned.id)]
      : history;
    const t = value("tool"), data = all
      .filter(item => item.request.parameters.tool === t)
      .sort((a, b) => new Date(b.created_at) - new Date(a.created_at));
    const choice = recordChoices.get(t), selected = data.find(item => item.id === choice) || data[0];
    if (choice && !data.some(item => item.id === choice)) recordChoices.delete(t);
    const choices = data.map((item, index) => [item.id,
      `${index === 0 ? "最新 · " : ""}${new Date(item.created_at).toLocaleString("zh-CN")} · ${item.request.parameters.protocol.toUpperCase()}`]);
    const signature = JSON.stringify([t, choices]);
    if (signature !== recordOptions) {
      options(recordSelect, choices);
      recordOptions = signature;
    }
    recordPicker.hidden = !data.length;
    recordSelect.value = selected?.id || "";
    const visible = selected ? [selected] : [];
    const next = JSON.stringify([t, visible, historyTool, historyError]);
    if (next !== snapshot) {
      snapshot = next;
      render(visible);
    }
  }
  async function loadPinned() {
    if (!requestedID || readingPinned) return;
    readingPinned = true;
    try {
      const data = await ctx.api(
        `/network-diagnostics/${encodeURIComponent(requestedID)}`,
      );
      if (ctx.current()) {
        pinned = data;
        if (!pinnedOpened) {
          pinnedOpened = true;
          controls.get("tool").value = data.request.parameters.tool;
          controls.get("tool").dispatchEvent(new Event("change", { bubbles: true }));
          recordChoices.set(data.request.parameters.tool, data.id);
        }
        renderSnapshot();
      }
    } catch (error) {
      if (ctx.current()) {
        feedback.textContent = error.message;
        if ([401, 403, 404].includes(error.status)) {
          pinned = undefined;
          history =
            error.status === 401
              ? []
              : history.filter((item) => item.id !== requestedID);
          renderSnapshot();
        }
      }
    } finally {
      readingPinned = false;
    }
  }
  const refresh = () =>
    Promise.allSettled([loadNodes(), loadResults(), loadPinned()]);
  ctx.onRefresh(refresh);
  void refresh();
  poll(ctx, loadResults, 2000);
  poll(ctx, loadNodes, 10000);
  if (requestedID) poll(ctx, loadPinned, 2000);
  const grantHost = node("div", { className: "a-stack" });
  if (ctx.can("agents.manage")) {
    const management = field("授权节点", "grant_node", "", {
        options: [["", "请选择"]],
      }),
      select = management.querySelector("select");
    void ctx
      .api("/agents")
      .then((items) => {
        if (ctx.current())
          options(select, [
            ["", "请选择"],
            ...items
              .filter(
                (a) => a.can_manage !== false && ctx.can("agents.manage", a),
              )
              .map((a) => [a.id, a.name]),
          ]);
      })
      .catch((e) => {
        if (ctx.current()) grantHost.textContent = e.message;
      });
    select.addEventListener("change", async () => {
      const epoch = ++grantEpoch,
        id = select.value;
      grantHost.replaceChildren();
      if (!id) return;
      try {
        const [shares, grants] = await Promise.all([
          ctx.api(`/agents/${encodeURIComponent(id)}/sharing`),
          ctx.api(`/agents/${encodeURIComponent(id)}/diagnostic-grants`),
        ]);
        if (!ctx.current() || epoch !== grantEpoch) return;
        grantHost.replaceChildren(
          ...(shares.shares || []).map((sh) => {
            const checked = field(
                `${sh.display_name || sh.username} · 节点网络诊断`,
                "grant",
                Boolean(grants[sh.id]?.enabled),
                { type: "checkbox" },
              ),
              input = checked.querySelector("input");
            input.addEventListener("change", async () => {
              input.disabled = true;
              try {
                await ctx.api(
                  `/agents/${encodeURIComponent(id)}/diagnostic-grants/${encodeURIComponent(sh.id)}`,
                  {
                    method: "PUT",
                    body: {
                      enabled: input.checked,
                      revision: grants[sh.id]?.revision || 0,
                    },
                  },
                );
                grants[sh.id] = {
                  enabled: input.checked,
                  revision: (grants[sh.id]?.revision || 0) + 1,
                };
                await loadNodes();
              } catch (e) {
                input.checked = !input.checked;
                feedback.textContent = e.message;
              } finally {
                input.disabled = false;
              }
            });
            return checked;
          }),
        );
      } catch (e) {
        if (ctx.current() && epoch === grantEpoch)
          grantHost.textContent = e.message;
      }
    });
    return stack(
      section("新建测量", form),
      section("测量记录", node("div", { className: "a-card-body a-stack" }, recordPicker, results)),
      node(
        "details",
        {},
        node("summary", {}, "共享诊断授权"),
        section(
          "",
          node(
            "div",
            { className: "a-card-body a-stack" },
            management,
            muted(
              "授权使用宿主机网络；不代表隔离实例专属路径。测速预留的流量上限计入共享额度。",
            ),
            grantHost,
          ),
        ),
      ),
    );
  }
  return stack(
    section("新建测量", form),
    section("测量记录", node("div", { className: "a-card-body a-stack" }, recordPicker, results)),
  );
}

function renderOutput(tool, out, state, rounds) {
  const running = state === "running";
  if (!out) return muted("尚无结果");
  if (tool === "ping")
    return table(
      [
        "发送",
        "接收",
        "丢包",
        "最小 / 平均 / 最大（ms）",
        "标准差（ms）",
        "目标响应",
      ],
      [
        [
          number(out.sent),
          number(out.received),
          `${number(out.loss_percent)}%`,
          out.received > 0
            ? `${number(out.min_ms)} / ${number(out.avg_ms)} / ${number(out.max_ms)}`
            : "—",
          out.received > 0 ? number(out.stdev_ms) : "—",
          out.reached ? "已收到响应" : "未收到目标响应",
        ],
      ],
    );
  if (tool === "mtr")
    return stack(
      actions(
        traceEnd(out.path_end?.reason, running),
        Number.isInteger(out.iteration) && muted(`轮次 ${out.iteration} / ${rounds}`),
      ),
      table(
        ["跳数", "响应主机", "ASN", "网络", "丢包", "发送 / 接收", "最近（ms）", "平均（ms）", "最佳（ms）", "最差（ms）", "标准差（ms）"],
        (out.stats || []).filter(Boolean).map((h) => [
          traceValue("hop", h.ttl),
          stack(
            traceValue(h.ip ? "ip" : "timeout", h.ip || "*"),
            h.host && h.host !== h.ip && muted(h.host),
            h.geo && traceValue("geo", locationName(h.geo)),
          ),
          traceValue(networkKind(h.geo, h.ip), asnName(h.geo)),
          traceValue(networkName(h.geo) === "—" ? "timeout" : networkKind(h.geo, h.ip), networkName(h.geo)),
          `${number(h.loss_percent)}%`,
          `${number(h.snt)} / ${number(h.received)}`,
          ...[h.last_ms, h.avg_ms, h.best_ms, h.wrst_ms, h.stdev_ms].map(value =>
            traceValue(h.received > 0 ? "latency" : "timeout", h.received > 0 ? number(value) : "—")),
        ]),
        { paginate: false, label: "MTR 跳点", columnWidths: ["4%", "28%", "7%", "12%", "6%", "8%", "7%", "7%", "7%", "7%", "7%"] },
      ),
    );
  if (tool === "traceroute") {
    const hops = new Map();
    for (const h of traceHops(out)) {
      if (!h?.TTL) continue;
      const ip = h.Success ? (typeof h.Address === "string" ? h.Address : h.Address?.IP) || "*" : "*";
      const key = `${h.TTL}/${ip}`;
      if (!hops.has(key)) hops.set(key, { ttl: h.TTL, ip, samples: [], geo: h.Geo, hostname: h.Hostname });
      const hop = hops.get(key);
      hop.samples.push(h.Success ? number(h.RTT / 1000000) : "*");
      if (!hop.geo && h.Geo) hop.geo = h.Geo;
    }
    return stack(
      traceEnd(out.StopReason?.reason, running),
      table(
        ["跳数", "响应地址", "ASN", "网络", "归属", "延迟（ms）"],
        [...hops.values()].map(h => [
          traceValue("hop", h.ttl),
          stack(traceValue(h.ip === "*" ? "timeout" : "ip", h.ip), h.hostname && muted(h.hostname)),
          traceValue(networkKind(h.geo, h.ip), asnName(h.geo)),
          traceValue(networkName(h.geo) === "—" ? "timeout" : networkKind(h.geo, h.ip), networkName(h.geo)),
          traceValue("geo", [...new Set([h.geo?.country || (/^(RFC|DOD)/.test(h.geo?.whois || "") ? h.geo.whois : ""), h.geo?.prov, h.geo?.city, h.geo?.district, h.geo?.owner, h.geo?.domain, h.geo?.isp].filter(Boolean))].join(" · ") || "—"),
          traceValue(h.ip === "*" ? "timeout" : "latency", h.samples.join(" / ")),
        ]),
        { paginate: false, label: "路由跳点" },
      ),
    );
  }
  const end = out.end || {},
    rows = [];
  for (const [name, r] of [
    ["发送端", end.sum_sent],
    ["接收端", end.sum_received],
    ["UDP 统计", end.sum],
  ])
    if (r)
      rows.push([
        name,
        `${number(r.bits_per_second / 1000000)} Mbit/s`,
        `${number(r.bytes / 1048576)} MiB`,
        number(r.seconds),
        r.retransmits == null ? "—" : number(r.retransmits),
        r.jitter_ms == null ? "—" : number(r.jitter_ms),
        r.lost_percent == null ? "—" : `${number(r.lost_percent)}%`,
      ]);
  const intervals = (out.intervals || []).filter(item => item?.sum).map(item => item.sum);
  const current = intervals.at(-1);
  return stack(
    current && node("dl", {className:"a-tools-metrics", "aria-label":"测速概况"},
      ...[[running ? "当前吞吐" : "最后一秒吞吐", number(current.bits_per_second/1000000), "Mbit/s"],
      ["平均吞吐", number(intervals.reduce((sum,item)=>sum+(item.bytes || 0),0)*8/Math.max(current.end,0.001)/1000000), "Mbit/s"],
      ["累计传输", number(intervals.reduce((sum,item)=>sum+(item.bytes || 0),0)/1048576), "MiB"],
      ["已测时长", number(current.end), "s"]].map(([label,value,unit]) =>
        node("div", {}, node("dt",{},label), node("dd",{},node("span",{className:"a-tools-metric-value"},value),node("span",{className:"a-tools-metric-unit"},unit)))),
    ),
    intervals.length>0 && stack(
      node("h3",{},"测速过程"),
      table(["时间段（s）", "吞吐", "传输量", "重传", "抖动（ms）", "丢包"], intervals.map(item => [
        `${number(item.start)}–${number(item.end)}`, `${number(item.bits_per_second/1000000)} Mbit/s`, `${number(item.bytes/1048576)} MiB`,
        item.retransmits==null ? "—" : number(item.retransmits), item.jitter_ms==null ? "—" : number(item.jitter_ms), item.lost_percent==null ? "—" : `${number(item.lost_percent)}%`,
      ]), {paginate:false,label:"iperf3 每秒统计"}),
    ),
    rows.length
      ? stack(node("h3",{},"汇总"), table(["统计端", "吞吐", "传输量", "实际时长（s）", "重传", "抖动（ms）", "丢包"], rows, {paginate:false}))
      : !intervals.length && muted(running ? "正在建立测速连接…" : "未产生有效吞吐统计"),
  );
}
function asnName(g) {
  const asn = g?.asnumber || g?.asn;
  return asn ? `AS${String(asn).replace(/^AS/i, "")}` : "—";
}
function locationName(g) {
  return [...new Set([g?.country || (/^(RFC|DOD)/.test(g?.whois || "") ? g.whois : ""), g?.prov, g?.city, g?.district, g?.owner, g?.domain, g?.isp].filter(Boolean))].join(" · ") || "—";
}
function traceValue(kind, value) {
  return node("span", { className: `a-trace-${kind}` }, value);
}
function traceHops(out) {
  return (Array.isArray(out?.Hops) ? out.Hops : []).flat().filter((hop) => hop && typeof hop === "object" && hop.TTL > 0);
}
function traceEnd(reason, running) {
  if (running) return traceValue("timeout", "正在探测");
  return traceValue(reason === "destination_reached" ? "asn" : "timeout", pathReason(reason));
}
function pathReason(reason) {
  return (
    {
      destination_reached: "目标已响应",
      unreachable: "路径返回不可达",
      max_hops: "已达最大跳数，未确认目标可达",
    }[reason] || "测量结束原因未确认目标可达"
  );
}
function geo(g) {
  return g
    ? [
        g.asnumber || g.asn,
        g.country,
        g.prov,
        g.city,
        g.owner,
        g.Source || g.source,
      ]
        .filter(Boolean)
        .join(" · ") || "—"
    : "—";
}

function querySummary(tool, out, provider) {
  const hops = tool === "mtr" ? (out.stats || []).filter(Boolean) : traceHops(out);
  const observed = hops.filter((h) => (tool === "mtr" ? h.ip : h.Success));
  const failedLookup = (g) => g && (["timeout", "error"].includes(g.source) || g.country === "网络故障");
  const failed = observed.filter((h) => failedLookup(h.geo || h.Geo));
  const enriched = observed.filter((h) => !failedLookup(h.geo || h.Geo) && geo(h.geo || h.Geo) !== "—");
  const status = failed.length ? "部分地址查询失败" : !enriched.length
    ? "未返回归属信息"
    : enriched.length < observed.length
      ? "部分地址未返回归属信息"
      : "已返回归属信息";
  return `IP 查询：${provider} · ${status}`;
}

function endpointLabel(endpoint) {
  return endpoint.name && endpoint.name !== endpoint.address
    ? `${endpoint.name} (${endpoint.address})`
    : endpoint.address;
}
