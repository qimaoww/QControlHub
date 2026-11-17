import { rankDashboardTrafficView } from "../modules/dashboard-model.js";
import { bindPreferenceFields } from "./preferences.js";
import { patch } from "./live-dom.js";
import { metricTrack } from "./metric-track.js";
import { nodeRegionFlag } from "./node-region.js";
import { badge, empty, field, muted, node, pathID, section } from "./ui.js";

const SVG_NS = "http://www.w3.org/2000/svg";

function svgRect(attrs) {
  const el = document.createElementNS(SVG_NS, "rect");
  for (const [k, v] of Object.entries(attrs)) {
    if (v != null && v !== false) el.setAttribute(k, String(v));
  }
  return el;
}

export function trafficSplitBar(received, sent, scaleTotal = 0) {
  const rx = Math.max(0, Number(received) || 0);
  const tx = Math.max(0, Number(sent) || 0);
  const sum = rx + tx;
  const maxTotal = Math.max(scaleTotal || sum, sum, 1);
  const totalWidth = sum > 0 ? Math.max(4, Math.min(200, (sum / maxTotal) * 200)) : 0;
  const rxWidth = sum > 0 ? (rx / sum) * totalWidth : 0;
  const txWidth = Math.max(0, totalWidth - rxWidth);

  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("class", "a-dash-split-bar");
  svg.setAttribute("viewBox", "0 0 200 6");
  svg.setAttribute("preserveAspectRatio", "none");
  svg.setAttribute("aria-hidden", "true");
  svg.append(svgRect({ x: 0, y: 0, width: 200, height: 6, rx: 3, class: "a-dash-split-track" }));
  if (rxWidth > 0) svg.append(svgRect({ x: 0, y: 0, width: rxWidth.toFixed(1), height: 6, rx: txWidth > 0 ? 0 : 3, class: "a-dash-split-rx" }));
  if (txWidth > 0) svg.append(svgRect({ x: rxWidth.toFixed(1), y: 0, width: txWidth.toFixed(1), height: 6, rx: rxWidth > 0 ? 0 : 3, class: "a-dash-split-tx" }));
  return svg;
}

export function createTrafficRankingCard(ctx, { bytes, getUsage, getUsageState, getPolicies, getPoliciesState, getAgents, getOrderIDs, getMonth }) {
  const state = { period: "month", limit: "5", scope: "all" };
  const periodField = field("周期", "traffic_rank_period", state.period, {
    options: [["month", "月度"], ["cycle", "计费周期"]],
    "aria-label": "排行统计周期",
  });
  const limitField = field("数量", "traffic_rank_limit", state.limit, {
    options: [["5", "Top 5"], ["10", "Top 10"], ["all", "全部"]],
    "aria-label": "显示节点数量",
  });
  const scopeField = field("范围", "traffic_rank_scope", state.scope, {
    options: [["all", "全部"], ["current", "当前用户"]],
    "aria-label": "统计范围",
  });
  periodField.classList.add("a-dash-inline-field");
  limitField.classList.add("a-dash-inline-field");
  scopeField.classList.add("a-dash-inline-field");
  scopeField.hidden = true;

  const periodSelect = periodField.querySelector("select");
  const limitSelect = limitField.querySelector("select");
  const scopeSelect = scopeField.querySelector("select");
  const rankHost = node("div", { className: "a-dash-rank-list", "aria-label": "节点流量排行" });
  const statusNode = muted("");
  statusNode.setAttribute("role", "status");
  let lastSignature = "";
  const keyed = (key, el) => {
    if (el?.setAttribute) el.setAttribute("data-refresh-key", key);
    return el;
  };

  const syncFromInputs = () => {
    state.period = periodSelect.value || "month";
    state.limit = limitSelect.value || "5";
    state.scope = scopeSelect.value || "all";
  };

  for (const select of [periodSelect, limitSelect, scopeSelect]) {
    select.addEventListener("change", () => {
      syncFromInputs();
      paint();
    }, { signal: ctx.signal });
  }

  bindPreferenceFields(ctx, "dashboard", [periodField, limitField, scopeField], {
    restore: () => {
      syncFromInputs();
      paint();
    },
  });

  function paint() {
    const usage = getUsage();
    const policiesRaw = getPolicies();
    const policies = Array.isArray(policiesRaw) ? policiesRaw : Array.isArray(policiesRaw?.policies) ? policiesRaw.policies : [];
    const pageAgents = !Array.isArray(policiesRaw) && Array.isArray(policiesRaw?.agents) ? policiesRaw.agents : [];
    const agents = getAgents();
    const orderIDs = getOrderIDs();
    const month = getMonth();
    const usageState = getUsageState();
    const policyState = getPoliciesState();

    const ranked = rankDashboardTrafficView({
      period: state.period,
      rows: usage?.days || [],
      month: usage?.month || month,
      agents,
      policies,
      pageAgents,
      scope: state.scope,
      limit: state.limit,
      orderIDs,
    });

    if (!ranked.hasCrossOwner && state.scope !== "all") {
      state.scope = "all";
      scopeSelect.value = "all";
    }

    let nextStatus = "";
    if (state.period === "cycle" && !policies.length && policyState === "loading") {
      nextStatus = "读取中…";
    } else if (state.period === "cycle" && policyState === "denied") {
      nextStatus = "无权限";
    } else if (state.period === "cycle" && policyState === "failed" && !policies.length) {
      nextStatus = "读取失败";
    } else if (state.period === "month" && !usage && usageState === "loading") {
      nextStatus = "读取中…";
    } else if (usageState === "denied" && state.period === "month") {
      nextStatus = "无权限";
    } else {
      nextStatus = ranked.totalCount
        ? `${state.period === "cycle" ? "当前周期" : month} · 合计 ${bytes(ranked.totalUsed)} · ${ranked.items.length}/${ranked.totalCount}`
        : "暂无数据";
    }

    const sig = JSON.stringify([
      state.period, state.limit, state.scope, month,
      usageState, policyState, usage?.month || "", Boolean(usage),
      nextStatus, ranked,
    ]);
    if (sig === lastSignature) return;
    lastSignature = sig;

    scopeField.hidden = !ranked.hasCrossOwner;
    if (statusNode.textContent !== nextStatus) statusNode.textContent = nextStatus;

    const headerNode = ranked.items.length
      ? node("div", { className: "a-dash-rank-headbar" },
          muted("#"),
          muted("节点"),
          muted("↓ / ↑ 流量"),
          muted("总计 / 占比"),
          muted("配额 / 速率"),
        )
      : null;

    const rowNodes = ranked.items.length
      ? ranked.items.map((item, idx) => {
          const agentMeta = item.agent || { id: item.agentID, name: item.agentID, region_code: "" };
          const displayName = agentMeta.name || item.agentID || "关联节点";
          const ownerTag = item.hasOtherUser
            ? (item.owners.length === 1 && !item.hasCurrentUser ? `共享 · ${item.owners[0]}` : item.hasCurrentUser ? "多归属" : "共享资源")
            : "";
          const sharePercent = ranked.totalUsed > 0 ? (item.used / ranked.totalUsed) * 100 : 0;
          const quotaText = item.quotaPercent != null
            ? `${item.quotaPercent.toFixed(1)}% · ${bytes(item.limitedUsedBytes)}/${bytes(item.limitBytes)}`
            : "";
          const hasRate = item.receiveBPS > 0 || item.sendBPS > 0;
          const rateNode = hasRate
            ? node("div", { className: "a-dash-rank-rate a-muted" },
                node("span", { className: "a-dash-rx" }, `↓ ${bytes(item.receiveBPS)}/s`),
                node("span", { className: "a-dash-tx" }, `↑ ${bytes(item.sendBPS)}/s`),
              )
            : null;

          return node("div", { className: "a-dash-rank-row", "data-refresh-key": item.agentID },
            node("div", { className: "a-dash-rank-col-idx", "data-refresh-key": "idx" },
              node("span", { className: `a-dash-rank-index${idx < 3 ? " is-top" : ""}` }, String(idx + 1).padStart(2, "0")),
            ),
            node("div", { className: "a-dash-rank-col-node", "data-refresh-key": "node" },
              node("div", { className: "a-probe-name" },
                nodeRegionFlag(ctx, agentMeta),
                node("a", { className: "a-dash-node-link", href: `#traffic?agent=${pathID(item.agentID)}` }, displayName),
              ),
              (ownerTag || item.blocked || item.portCount > 1)
                ? node("div", { className: "a-dash-rank-tags", "data-refresh-key": "tags" },
                    ownerTag ? keyed("owner", badge(ownerTag)) : null,
                    item.blocked ? keyed("blocked", badge("已封禁", "bad")) : null,
                    item.portCount > 1 ? keyed("ports", badge(`${item.portCount} 端口`)) : null,
                  )
                : null,
            ),
            node("div", { className: "a-dash-rank-col-flow", "data-refresh-key": "flow" },
              node("div", { className: "a-dash-rank-flow-nums" },
                node("span", { className: "a-dash-rx" }, `↓ ${bytes(item.received)}`),
                node("span", { className: "a-dash-tx" }, `↑ ${bytes(item.sent)}`),
              ),
              trafficSplitBar(item.received, item.sent, ranked.maxUsed),
            ),
            node("div", { className: "a-dash-rank-col-share", "data-refresh-key": "share" },
              node("div", { className: "a-dash-rank-share-head" },
                node("strong", {}, bytes(item.used)),
                muted(`${sharePercent.toFixed(1)}%`),
              ),
              metricTrack(sharePercent, true, false),
            ),
            node("div", { className: "a-dash-rank-col-quota", "data-refresh-key": "quota" },
              quotaText
                ? node("div", { className: "a-dash-rank-quota", "data-refresh-key": "bar" },
                    muted(quotaText),
                    metricTrack(item.quotaPercent, true, true),
                  )
                : null,
              rateNode ? keyed("rate", rateNode) : (!quotaText ? keyed("empty", muted("—")) : null),
            ),
          );
        })
      : [empty("暂无数据")];

    patch(rankHost, node("div", { className: "a-dash-rank-list", "aria-label": "节点流量排行" },
      headerNode,
      ...rowNodes,
    ));
  }

  const headLegend = node("div", { className: "a-actions" },
    node("span", { className: "a-dash-swatch-badge" },
      node("span", { className: "a-dash-swatch-rx" }),
      node("span", {}, "↓ 接收"),
      node("span", { className: "a-dash-swatch-tx" }),
      node("span", {}, "↑ 发送"),
    ),
    statusNode,
  );
  const subbar = node("div", { className: "a-dash-subbar" },
    node("div", { className: "a-dash-rank-fields" }, periodField, limitField, scopeField),
  );

  const element = section("流量累计排行", node("div", { className: "a-dash-rank-wrap" }, subbar, rankHost), headLegend);
  return { element, paint };
}
