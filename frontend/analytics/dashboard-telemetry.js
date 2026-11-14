import {
  isClockSyncAbnormal,
  panelMetricUsage,
  summarizeClientConnectionsPulse,
  summarizeExpiringCertificates,
  summarizeIPQualityFleet,
} from "../modules/dashboard-model.js";
import { createResponsiveSVG, createSVGEl } from "./dashboard-panel.js";
import { patch } from "./live-dom.js";
import { metricTrack } from "./metric-track.js";
import { nodeMetrics } from "./node-metrics.js";
import { nodeRegionFlag } from "./node-region.js";
import { badge, button, empty, link, muted, node, pathID, section } from "./ui.js";

function drawPulseSVG({ width, height }, pulse) {
  const buckets = pulse?.buckets || [];
  const slice = buckets.slice(-24);
  const w = Math.max(180, Math.round(width));
  const h = height || 56;
  const padX = 2;
  const padT = 8;
  const padB = 4;
  const usableW = Math.max(20, w - padX * 2);
  const usableH = Math.max(16, h - padT - padB);
  const maxFlows = Math.max(1, ...slice.map((b) => b.flows));

  const svg = createSVGEl("svg", {
    class: "a-dash-pulse-svg",
    viewBox: `0 0 ${w} ${h}`,
    "aria-label": "近 24 小时入站连接脉冲",
  });

  svg.append(
    createSVGEl("line", { x1: padX, y1: padT, x2: w - padX, y2: padT, class: "a-dash-spark-baseline" }),
    createSVGEl("line", { x1: padX, y1: (padT + usableH * 0.5).toFixed(1), x2: w - padX, y2: (padT + usableH * 0.5).toFixed(1), class: "a-dash-spark-baseline" }),
    createSVGEl("line", { x1: padX, y1: (h - padB).toFixed(1), x2: w - padX, y2: (h - padB).toFixed(1), class: "a-dash-osc-axis" }),
  );

  if (!slice.length) return svg;

  const slotW = usableW / slice.length;
  const gap = Math.max(2, Math.min(4, slotW * 0.24));
  const barW = Math.max(3, slotW - gap);

  slice.forEach((b, idx) => {
    const x = (padX + idx * slotW + gap * 0.5).toFixed(1);
    const ratio = b.flows > 0 ? Math.max(0.1, b.flows / maxFlows) : 0;
    const barH = b.flows > 0 ? Math.max(4, ratio * usableH) : 2;
    const y = (h - padB - barH).toFixed(1);
    const cls = b.flows <= 0
      ? "a-dash-pulse-bar is-zero"
      : b.flows === maxFlows && maxFlows > 0
        ? "a-dash-pulse-bar is-peak"
        : "a-dash-pulse-bar";
    const rect = createSVGEl("rect", { x, y, width: barW.toFixed(1), height: barH.toFixed(1), rx: "1.5", class: cls });
    rect.append(createSVGEl("title", {}, `${String(b.time || "").slice(11, 16) || `#${idx + 1}`} · 活跃流 ${b.flows} · IP ${b.ips}`));
    svg.append(rect);
  });

  return svg;
}

export function paintNodeTelemetryMatrix({ ctx, nodeSummary, matrixHost, agents, agentsReady, canMetrics, bytes, date, status }) {
  const skewedCount = agents.filter((a) => isClockSyncAbnormal(a.metrics?.clock_sync)).length;
  const onlineCount = agents.filter((a) => a.status === "online").length;

  patch(nodeSummary, node("div", { className: "a-actions" },
    agents.length ? badge(`${onlineCount}/${agents.length} 在线`, onlineCount === agents.length ? "ok" : onlineCount > 0 ? "warn" : "bad") : null,
    skewedCount > 0 ? badge(`时钟异常 ${skewedCount}`, "warn") : null,
    link("管理节点", "#node-settings"),
  ));

  if (!agents.length) {
    patch(matrixHost, node("div", { className: "a-dash-matrix", "aria-label": "节点运行状态" },
      empty(agentsReady ? "暂无节点" : "读取中…"),
    ));
    return;
  }

  const headRow = canMetrics
    ? node("div", { className: "a-dash-matrix-head" },
        muted("节点"),
        muted("内核"),
        muted("CPU"),
        muted("内存"),
        muted("吞吐"),
        muted("操作"),
      )
    : node("div", { className: "a-dash-matrix-head" },
        muted("节点"),
        muted("内核"),
        muted("操作"),
      );

  const rows = agents.map((agent) => {
    const cpu = panelMetricUsage(agent.metrics, "cpu");
    const mem = panelMetricUsage(agent.metrics, "memory");
    const cpuStale = agent.status !== "online" || cpu == null;
    const memStale = agent.status !== "online" || mem == null;
    const rx = agent.metrics?.network_available ? agent.metrics.network_rx_bps : null;
    const tx = agent.metrics?.network_available ? agent.metrics.network_tx_bps : null;
    const runtimes = Object.values(agent.runtime || {}).filter((item) => item?.installed);
    const activeCount = agent.status === "online" ? runtimes.filter((item) => item?.service_status === "active").length : 0;
    const skewAbnormal = isClockSyncAbnormal(agent.metrics?.clock_sync);
    const rowStatus = agent.status !== "online" ? "offline" : skewAbnormal ? "skew" : "online";
    const ipv4Ready = Boolean(agent.public_ipv4 || agent.metrics?.public_ipv4);
    const ipv6Ready = Boolean(agent.public_ipv6 || agent.metrics?.public_ipv6);
    const lastHeartbeat = agent.metrics?.collected_at || agent.last_seen;

    return node("div", { className: "a-dash-matrix-row", "data-refresh-key": agent.id, "data-status": rowStatus },
      node("div", { className: "a-dash-matrix-identity" },
        node("div", { className: "a-probe-name" },
          nodeRegionFlag(ctx, agent),
          node("a", { className: "a-dash-node-link", href: `#node-settings?agent=${pathID(agent.id)}` }, agent.name || agent.id),
          status(agent.status),
        ),
        node("div", { className: "a-dash-matrix-sub" },
          muted(lastHeartbeat ? date(lastHeartbeat) : "未同步"),
          ipv4Ready ? badge("IPv4") : null,
          ipv6Ready ? badge("IPv6") : null,
          skewAbnormal ? badge("时钟异常", "warn") : null,
        ),
      ),
      node("div", { className: "a-dash-matrix-cores" },
        node("span", { className: "a-dash-mobile-lbl a-muted" }, "内核"),
        node("strong", {}, runtimes.length ? `${activeCount}/${runtimes.length}` : "—"),
      ),
      canMetrics ? node("div", { className: "a-dash-matrix-meter is-cpu" },
        node("div", { className: "a-dash-matrix-meter-label" },
          node("span", { className: "a-dash-mobile-lbl a-muted" }, "CPU"),
          node("strong", {}, cpuStale ? "—" : `${cpu.toFixed(1)}%`),
        ),
        metricTrack(cpuStale ? null : cpu, !cpuStale, true),
      ) : null,
      canMetrics ? node("div", { className: "a-dash-matrix-meter is-mem" },
        node("div", { className: "a-dash-matrix-meter-label" },
          node("span", { className: "a-dash-mobile-lbl a-muted" }, "内存"),
          node("strong", {}, memStale ? "—" : `${mem.toFixed(1)}%`),
        ),
        metricTrack(memStale ? null : mem, !memStale, true),
      ) : null,
      canMetrics ? node("div", { className: "a-dash-matrix-rate" },
        node("span", { className: "a-dash-mobile-lbl a-muted" }, "吞吐"),
        agent.status === "online" && rx != null && tx != null
          ? node("div", { className: "a-dash-matrix-duplex" },
              node("span", { className: "a-dash-rx" }, `↓ ${bytes(rx)}/s`),
              node("span", { className: "a-dash-tx" }, `↑ ${bytes(tx)}/s`),
            )
          : muted("—"),
      ) : null,
      node("div", { className: "a-dash-matrix-actions a-actions" },
        canMetrics ? button("24h 趋势", () => nodeMetrics(ctx, agent)) : null,
        node("a", { className: "a-button", href: `#node-settings?agent=${pathID(agent.id)}` }, "管理"),
      ),
    );
  });

  patch(matrixHost, node("div", { className: `a-dash-matrix${canMetrics ? "" : " is-no-metrics"}`, "aria-label": "节点运行状态" },
    headRow,
    ...rows,
  ));
}

function edgeKPIStat(label, valueEl, sub = "") {
  return node("div", { className: "a-dash-edge-kpi" },
    muted(label),
    valueEl,
    sub ? node("span", { className: "a-dash-edge-kpi-sub a-muted" }, sub) : null,
  );
}

export function createEdgeTelemetryController(ctx, { readEdgeState }) {
  const canConn = () => ctx.can("core-logs.read");
  const canCert = () => ctx.can("certificates.read");
  const canIPQ = () => ctx.can("agents.read");

  const edgeCard = (enabled, title, inner, action) => {
    if (!enabled) return null;
    const card = section(title, inner, action);
    card.classList.add("a-dash-edge-card");
    return card;
  };

  const connStatsHost = node("div", { className: "a-dash-edge-stats" });
  const connSlot = createResponsiveSVG("a-dash-pulse-wrap", ctx.signal, { height: 56, fallbackWidth: 340, draw: drawPulseSVG });
  const connFoot = node("div", { className: "a-chart-meta" });
  const connViz = node("div", { className: "a-dash-edge-viz" }, connSlot.element, connFoot);
  const connCard = edgeCard(canConn(), "入站连接脉冲 (24h)", node("div", { className: "a-dash-edge-inner" }, connStatsHost, connViz), link("连接明细", "#client-connections"));

  const certStatsHost = node("div", { className: "a-dash-edge-stats" });
  const certViz = node("div", { className: "a-dash-edge-viz" });
  const certCard = edgeCard(canCert(), "证书健康", node("div", { className: "a-dash-edge-inner" }, certStatsHost, certViz), link("管理证书", "#certificates"));

  const ipqStatsHost = node("div", { className: "a-dash-edge-stats" });
  const ipqViz = node("div", { className: "a-dash-edge-viz" });
  const ipqCard = edgeCard(canIPQ(), "IP 质量概况", node("div", { className: "a-dash-edge-inner" }, ipqStatsHost, ipqViz), link("检测记录", "#ip-quality"));

  const cards = [connCard, certCard, ipqCard].filter(Boolean);
  const grid = node("div", { className: `a-dash-edge-grid is-cols-${cards.length || 1}` }, ...cards);

  function paintConn() {
    if (!connCard) return;
    const { data, state } = readEdgeState("connections");
    if (!data) {
      patch(connStatsHost, node("div", { className: "a-dash-edge-stats" },
        edgeKPIStat("活跃 IP", node("strong", {}, "—")),
        edgeKPIStat("活跃流", node("strong", {}, "—")),
        edgeKPIStat("来源", node("strong", {}, "—")),
      ));
      connSlot.update({ buckets: [] });
      patch(connFoot, node("div", { className: "a-chart-meta" },
        muted(state === "failed" ? "读取失败" : state === "denied" ? "无权限" : "读取中…"),
      ));
      return;
    }

    const pulse = summarizeClientConnectionsPulse(data);
    const slice = pulse.buckets.slice(-24);
    const maxFlow = Math.max(0, ...slice.map((b) => b.flows));

    patch(connStatsHost, node("div", { className: "a-dash-edge-stats" },
      edgeKPIStat("活跃 IP", node("strong", {}, String(pulse.ips))),
      edgeKPIStat("活跃流", node("strong", {}, String(pulse.flows))),
      edgeKPIStat("来源", node("strong", {}, `${pulse.activeSources}/${pulse.totalSources}`)),
    ));
    connSlot.update(pulse);
    patch(connFoot, node("div", { className: "a-chart-meta" },
      muted(slice.length ? `近 ${slice.length}h` : "暂无数据"),
      muted(slice.length ? `峰值 ${maxFlow}/h` : ""),
    ));
  }

  function paintCert() {
    if (!certCard) return;
    const { data, state } = readEdgeState("certificates");
    if (!data) {
      patch(certStatsHost, node("div", { className: "a-dash-edge-stats" },
        edgeKPIStat("有效", node("strong", {}, "—")),
        edgeKPIStat("14 天内到期", node("strong", {}, "—")),
        edgeKPIStat("异常", node("strong", {}, "—")),
      ));
      patch(certViz, node("div", { className: "a-dash-edge-viz" },
        muted(state === "failed" ? "读取失败" : state === "denied" ? "无权限" : "读取中…"),
      ));
      return;
    }

    const summary = summarizeExpiringCertificates(data);
    patch(certStatsHost, node("div", { className: "a-dash-edge-stats" },
      edgeKPIStat("有效", node("strong", {}, String(summary.totalOwned))),
      edgeKPIStat("14 天内到期", node("strong", { className: summary.expiringSoon ? "is-warn-txt" : "" }, String(summary.expiringSoon))),
      edgeKPIStat("异常", node("strong", { className: summary.abnormal ? "is-bad" : "" }, String(summary.abnormal))),
    ));

    patch(certViz, node("div", { className: "a-dash-edge-viz" },
      (summary.expiringSoon || summary.abnormal)
        ? node("div", { className: "a-dash-cert-alerts" },
            summary.expiringSoon ? muted(`告警明细 · 14 天内到期 ${summary.expiringSoon}`) : null,
            ...summary.urgent.map((item) => node("div", { className: "a-dash-cert-row" },
              node("span", { className: "a-dash-ep" }, item.name),
              badge(item.abnormal ? "异常" : `${item.daysLeft ?? 0} 天到期`, item.abnormal ? "bad" : "warn"),
            )),
          )
        : node("div", { className: "a-dash-edge-status-bar" },
            metricTrack(100, true, false),
            muted(summary.totalOwned ? "全部有效" : "暂无证书"),
          ),
    ));
  }

  function paintIPQ() {
    if (!ipqCard) return;
    const { data, state } = readEdgeState("ipQuality");
    if (!data) {
      patch(ipqStatsHost, node("div", { className: "a-dash-edge-stats" },
        edgeKPIStat("已检测", node("strong", {}, "—")),
        edgeKPIStat("完成", node("strong", {}, "—")),
        edgeKPIStat("异常/失败", node("strong", {}, "—")),
      ));
      patch(ipqViz, node("div", { className: "a-dash-edge-viz" },
        muted(state === "failed" ? "读取失败" : state === "denied" ? "无权限" : "读取中…"),
      ));
      return;
    }

    const summary = summarizeIPQualityFleet(data);
    const passPct = summary.total ? (summary.succeeded / summary.total) * 100 : 0;
    patch(ipqStatsHost, node("div", { className: "a-dash-edge-stats" },
      edgeKPIStat("已检测", node("strong", {}, String(summary.total))),
      edgeKPIStat("完成", node("strong", {}, String(summary.succeeded))),
      edgeKPIStat("异常/失败", node("strong", { className: summary.failed ? "is-bad" : "" }, String(summary.failed))),
    ));
    patch(ipqViz, node("div", { className: "a-dash-edge-viz" },
      summary.total > 0
        ? node("div", { className: "a-dash-edge-status-bar" },
            metricTrack(passPct, true, false),
            node("div", { className: "a-chart-meta" },
              muted(`通过 ${Math.round(passPct)}%`),
              summary.running ? muted(`进行中 ${summary.running}`) : null,
            ),
          )
        : muted("暂无记录"),
    ));
  }

  function paint() {
    paintConn();
    paintCert();
    paintIPQ();
  }

  return { element: grid, visible: cards.length > 0, paint };
}
