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
    pinned,
    readingPinned = false;
  const controls = new Map(),
    expanded = new Map();
  results.addEventListener("click", (event) => {
    const summary = event.target.closest("summary");
    const disclosure = summary?.parentElement;
    if (disclosure?.hasAttribute("data-refresh-key"))
      expanded.set(disclosure.dataset.refreshKey, !disclosure.open);
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
  const provider = f("IP 查询提供方", "provider", "disable-geoip", {
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
  const bandwidth = f("每流目标带宽（bit/s）", "bandwidth", 20000000, {
    type: "number",
    min: 1000,
    max: 100000000,
  });
  const forward = button("开始测试", () => void submit(false), {
      disabled: true,
      tone: "primary",
    }),
    both = button("双向追踪", () => void submit(true), { disabled: true });
  const swap = button(
    "交换两端",
    () => {
      const a = controls.get("node1").value;
      controls.get("node1").value = controls.get("node2").value;
      controls.get("node2").value = a;
      addresses(1);
      addresses(2);
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
    bandwidth,
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
      old = select.value;
    options(select, [
      ["", "请选择"],
      ...(n?.capabilities?.addresses || []).map((ip) => [ip, ip]),
    ]);
    select.value = [...select.options].some((o) => o.value === old) ? old : "";
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
    provider.hidden = !trace;
    hops.hidden = !trace;
    port.hidden = p === "icmp";
    duration.hidden = streams.hidden = bandwidth.hidden = t !== "iperf3";
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
          ? (value("address1").includes(":") ? 20 : 0) + (p === "tcp" ? 40 : 28)
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
    controls.get("size").value = t === "iperf3" ? 131072 : 64;
    update();
  });
  controls.get("protocol").addEventListener("change", () => {
    if (value("tool") === "iperf3")
      controls.get("size").value = value("protocol") === "udp" ? 1200 : 131072;
    update();
  });
  for (const i of [1, 2])
    controls.get(`node${i}`).addEventListener("change", () => {
      addresses(i);
      update();
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
        "bandwidth",
      ].map((k) => [
        k,
        ["tool", "protocol", "provider"].includes(k)
          ? value(k)
          : Number(value(k)),
      ]),
    );
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
        body: { ...body, request_id: crypto.randomUUID() },
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
        ? items.map((d, index) => {
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
            const direction = req.bidirectional ? "⇄" : "→";
            const summary = node(
              "summary",
              {},
              `${new Date(d.created_at).toLocaleString("zh-CN")} · ${name} / ${params.protocol.toUpperCase()} · ${req.node1.name} ${direction} ${req.node2.name} · ${labels[d.state] || d.state}`,
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
                  await loadResults();
                } catch (e) {
                  feedback.textContent = e.message;
                  await loadResults();
                } finally {
                  if (control.isConnected) control.disabled = false;
                }
              });
            const details = node(
              "details",
              {
                className: "a-disclosure",
                "data-refresh-key": d.id,
                open: index === 0,
              },
              summary,
              node(
                "div",
                { className: "a-disclosure-body a-stack" },
                actions(stop, retry),
                muted(
                  tool === "iperf3"
                    ? `${params.duration} s · ${params.streams} 流 · ${number(params.bandwidth / 1000000)} Mbit/s / 流 · ${params.size} 字节`
                    : `${params.count} ${tool === "mtr" ? "轮" : "次"} · ${params.interval_ms} ms 间隔 · ${params.timeout_ms} ms 超时 · ${params.size} 字节${tool === "ping" ? "负载" : `总包长 · ${params.max_hops} 跳 · ${params.provider}`}`,
                ),
                server &&
                  actions(
                    muted("接收端服务"),
                    badge(
                      server.cleaned
                        ? "已回收"
                        : labels[server.state] || server.state,
                    ),
                  ),
                server?.error && notice(server.error, "error"),
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
                      return stack(
                        actions(
                          node(
                            "h3",
                            {},
                            tool === "iperf3"
                              ? "测速结果"
                              : reverse
                                ? "回程"
                                : "去程",
                          ),
                          badge(labels[r.state] || r.state),
                        ),
                        muted(`${endpointLabel(from)} → ${endpointLabel(to)}`),
                        r.error && notice(r.error, "error"),
                        renderOutput(tool, r.output),
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
                      );
                    })
                  : muted(
                      terminal(d.state) ? "未产生探测结果" : "等待 Agent 执行",
                    ),
              ),
            );
            return details;
          })
        : muted("暂无测量记录"),
    );
    patch(results, fresh);
    const ids = new Set(items.map((item) => item.id));
    for (const id of expanded.keys()) if (!ids.has(id)) expanded.delete(id);
    for (const disclosure of results.querySelectorAll(
      "details[data-refresh-key]",
    )) {
      const id = disclosure.dataset.refreshKey;
      disclosure.open = expanded.has(id)
        ? expanded.get(id)
        : id === items[0]?.id;
    }
  }
  async function loadResults() {
    if (reading) return;
    reading = true;
    try {
      const data = await ctx.api("/network-diagnostics");
      if (!ctx.current()) return;
      history = data;
      renderSnapshot();
    } catch (e) {
      if (ctx.current()) {
        feedback.textContent = e.message;
        if (e.status === 401 || e.status === 403) {
          results.replaceChildren();
          snapshot = undefined;
        }
      }
    } finally {
      reading = false;
    }
  }
  function renderSnapshot() {
    const data = pinned
      ? [pinned, ...history.filter((item) => item.id !== pinned.id)]
      : history;
    const next = JSON.stringify(data);
    if (next !== snapshot) {
      snapshot = next;
      render(data);
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
        renderSnapshot();
      }
    } catch (error) {
      if (ctx.current()) {
        feedback.textContent = error.message;
        if ([401, 403, 404].includes(error.status)) {
          pinned = undefined;
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
      section("测量记录", node("div", { className: "a-card-body" }, results)),
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
    section("测量记录", node("div", { className: "a-card-body" }, results)),
  );
}

function renderOutput(tool, out) {
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
      muted(pathReason(out.path_end?.reason)),
      table(
        [
          "TTL",
          "地址",
          "发送 / 接收",
          "丢包",
          "最近 / 平均（ms）",
          "最佳 / 最差（ms）",
          "标准差（ms）",
          "ASN / 归属",
        ],
        (out.stats || []).map((h) => [
          h.ttl,
          h.ip || "*",
          `${number(h.snt)} / ${number(h.received)}`,
          `${number(h.loss_percent)}%`,
          h.received > 0 ? `${number(h.last_ms)} / ${number(h.avg_ms)}` : "—",
          h.received > 0 ? `${number(h.best_ms)} / ${number(h.wrst_ms)}` : "—",
          h.received > 0 ? number(h.stdev_ms) : "—",
          geo(h.geo),
        ]),
      ),
    );
  if (tool === "traceroute")
    return stack(
      muted(pathReason(out.StopReason?.reason)),
      table(
        ["TTL", "响应地址", "延迟（ms）", "ASN / 归属"],
        (out.Hops || [])
          .flat()
          .map((h) => [
            h.TTL,
            h.Success ? h.Address?.IP || h.Address || "*" : "*",
            h.Success ? number(h.RTT / 1000000) : "无响应",
            geo(h.Geo),
          ]),
      ),
    );
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
        r.retransmits == null ? "—" : number(r.retransmits),
        r.jitter_ms == null ? "—" : number(r.jitter_ms),
        r.lost_percent == null ? "—" : `${number(r.lost_percent)}%`,
      ]);
  return stack(
    out.error && notice(out.error, "error"),
    rows.length
      ? table(["统计端", "吞吐", "传输量", "重传", "抖动（ms）", "丢包"], rows)
      : muted("未产生有效吞吐统计"),
  );
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
  const hops = tool === "mtr" ? out.stats || [] : (out.Hops || []).flat();
  const observed = hops.filter((h) => (tool === "mtr" ? h.ip : h.Success));
  const enriched = observed.filter((h) => geo(h.geo || h.Geo) !== "—");
  const status = !enriched.length
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
