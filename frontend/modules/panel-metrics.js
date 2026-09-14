import { createPoller, createRefreshChannel, reconcileView } from "./refresh.js";

const finiteCounter = (value) => typeof value === "number" && Number.isFinite(value) && value >= 0;
const clampPercent = (value) => Math.min(100, Math.max(0, value));
const icons = {
  cpu: '<rect x="6" y="6" width="12" height="12" rx="2"/><path d="M9 2v4m6-4v4M9 18v4m6-4v4M2 9h4m-4 6h4m12-6h4m-4 6h4"/><rect x="9" y="9" width="6" height="6" rx="1"/>',
  memory: '<rect x="3" y="6" width="18" height="12" rx="2"/><path d="M7 10v4m5-4v4m5-4v4M7 18v3m5-3v3m5-3v3"/>',
  disk: '<path d="m5 4-3 10v5a1 1 0 0 0 1 1h18a1 1 0 0 0 1-1v-5L19 4Z"/><path d="M2 14h20M6 17h.01M10 17h.01"/>',
  network: '<path d="M7 3v16m-4-4 4 4 4-4M17 21V5m-4 4 4-4 4 4"/>',
};

export function panelMetricUsage(metrics, kind) {
  if (!metrics?.[`${kind}_available`]) return null;
  if (kind === "cpu") return finiteCounter(metrics.cpu_percent) ? clampPercent(metrics.cpu_percent) : null;
  const used = metrics[`${kind}_used_bytes`], total = metrics[`${kind}_total_bytes`];
  return finiteCounter(used) && finiteCounter(total) && total > 0
    ? clampPercent(used / total * 100) : null;
}

export function renderPanelMetrics(metrics, {
  esc, bytes, rate, failed = false, pending = false, now = Date.now(),
}) {
  const usages = Object.fromEntries(["cpu", "memory", "disk"].map((kind) => [kind, panelMetricUsage(metrics, kind)]));
  const networkAvailable = Boolean(metrics?.network_available) &&
    finiteCounter(metrics.network_rx_bps) && finiteCounter(metrics.network_tx_bps);
  const hasData = Object.values(usages).some((value) => value !== null) || networkAvailable;
  const collectedAt = new Date(metrics?.collected_at || "").getTime();
  const validTime = Number.isFinite(collectedAt) && collectedAt > 0;
  const stale = hasData && (!validTime || now - collectedAt > 15_000);
  const status = failed ? (hasData ? "刷新失败 · 保留上次数据" : "暂时无法读取")
    : stale ? "数据已过期" : hasData ? "每 2 秒更新" : pending ? "正在采集" : "指标暂不可用";
  const tone = failed || stale ? "warn" : hasData ? "ok" : "muted";
  const icon = (kind) => `<svg viewBox="0 0 24 24" aria-hidden="true">${icons[kind]}</svg>`;
  const capacityCard = (kind, label, note) => {
    const value = usages[kind];
    const available = value !== null;
    const level = available && value >= 90 ? "high" : available && value >= 75 ? "elevated" : "";
    const detail = available && kind !== "cpu"
      ? `${bytes(metrics[`${kind}_used_bytes`])} / ${bytes(metrics[`${kind}_total_bytes`])}` : available ? note : "等待有效采样";
    return `<div class="panel-metric ${level}" data-panel-metric="${kind}">
      <div class="panel-metric-label">${icon(kind)}<span>${label}</span></div>
      <strong class="panel-metric-value" data-panel-value>${available ? `${value.toFixed(1)}<small>%</small>` : "—"}</strong>
      <div class="panel-metric-track" aria-hidden="true"><span style="width:${available ? value.toFixed(1) : 0}%"></span></div>
      <small class="panel-metric-detail">${esc(detail)}</small>
    </div>`;
  };
  return `<section class="dashboard-host workspace-panel qch-swap-panel" id="panel-host" data-refresh-key="panel-host" aria-labelledby="panel-host-title">
    <header><div class="dashboard-section-heading"><h3 id="panel-host-title">面板主机</h3><small>控制面所在环境的资源占用</small></div><button class="button small panel-metrics-refresh" type="button" data-panel-metrics-refresh aria-label="刷新面板主机指标" title="刷新面板主机指标"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 7h-5V2m5 0-3.5 3.5A8 8 0 1 0 20.8 14"/></svg></button></header>
    <div class="panel-metrics-grid">
      ${capacityCard("cpu", "CPU", "所有逻辑核心的平均使用率")}
      ${capacityCard("memory", "内存", "")}
      ${capacityCard("disk", "磁盘 · 根目录", "")}
      <div class="panel-metric panel-metric-network" data-panel-metric="network">
        <div class="panel-metric-label">${icon("network")}<span>实时网络</span></div>
        <dl><div><dt><span aria-hidden="true">↓</span> 接收</dt><dd data-panel-rx>${networkAvailable ? esc(rate(metrics.network_rx_bps)) : "—"}</dd></div><div><dt><span aria-hidden="true">↑</span> 发送</dt><dd data-panel-tx>${networkAvailable ? esc(rate(metrics.network_tx_bps)) : "—"}</dd></div></dl>
        <small class="panel-metric-detail">默认路由网卡 · 非节点流量</small>
      </div>
    </div>
    <footer><div class="panel-metrics-status ${tone}" data-panel-metrics-status role="status"><i aria-hidden="true"></i><span>${status}</span>${hasData && validTime ? `<time datetime="${esc(metrics.collected_at)}" title="${esc(new Date(collectedAt).toLocaleString())}">${esc(new Date(collectedAt).toLocaleTimeString([], { hour12: false }))}</time>` : ""}</div><p>容器部署时，磁盘与网络仅反映容器可见范围。</p></footer>
  </section>`;
}

// Only this card refreshes. The month picker, traffic dialog, focused controls
// and dashboard scroll position are independent of host-counter polling.
export function installPanelMetrics({ api, state, can, esc, bytes, rate }) {
  let stop = () => {};
  const render = (options = {}) => renderPanelMetrics(state.data.panelMetrics, {
    esc, bytes, rate, pending: !state.data.panelMetrics, ...options,
  });
  const mount = () => {
    stop();
    const root = document.querySelector("#panel-host");
    if (!root || !can("panel-metrics.read")) return;
    const scope = state.routeSignal;
    const epoch = state.navigationEpoch;
    const session = state.session;
    let disposed = false;
    const isCurrent = () => !disposed && root.isConnected && state.route === "dashboard" &&
      state.routeSignal === scope && state.navigationEpoch === epoch && state.session === session && !scope?.aborted;
    const channel = createRefreshChannel({ isCurrent, getScope: () => state.navigationEpoch });
    const apply = (options) => {
      if (!isCurrent()) return;
      const template = document.createElement("template");
      template.innerHTML = render(options);
      reconcileView(root, template.content.firstElementChild);
      bindRefresh();
    };
    const poller = createPoller({
      isActive: () => isCurrent() && !document.hidden,
      delay: () => 2000,
      run: async () => {
        try {
          await channel.run(
            (signal) => api("/panel-metrics", { signal }),
            (metrics) => {
              state.data.panelMetrics = metrics;
              apply({ pending: false });
            },
          );
        } catch {
          apply({ failed: true, pending: false });
        }
      },
    });
    const bindRefresh = () => {
      root.querySelector("[data-panel-metrics-refresh]").onclick = () => void poller.trigger();
    };
    const onVisibility = () => {
      if (document.hidden) {
        poller.stop();
        channel.invalidate();
      } else {
        void poller.trigger();
      }
    };
    stop = () => {
      disposed = true;
      poller.stop();
      channel.invalidate();
      document.removeEventListener("visibilitychange", onVisibility);
      scope?.removeEventListener("abort", stop);
    };
    document.addEventListener("visibilitychange", onVisibility);
    scope?.addEventListener("abort", stop, { once: true });
    bindRefresh();
    void poller.trigger();
  };
  return { render, mount };
}
