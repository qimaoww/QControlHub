import { createSVGEl as svg } from "./dashboard-panel.js";
import { probeDirectionStatus } from "../modules/dashboard-model.js";

const colors = { ok: "#155731", warn: "#b45309", bad: "#a41a2b", "": "#a1a1aa" };
const metric = dir => dir?.reached && typeof dir.avg_ms === "number" ? `${dir.avg_ms.toFixed(1)} ms · ${Number((dir.loss_percent || 0).toFixed(1))}%` : dir?.error || dir?.state === "failed" ? "探测失败" : probeDirectionStatus(dir).label;

// Geometry belongs to this single ordered chain. Nodes never merge by address,
// label, country or ownership, and all measurements retain their hop index.
export function renderChainGraph(pair, endpoints, width) {
  const w = Math.max(280, Math.round(width));
  const cols = Math.min(endpoints.length, Math.max(1, Math.min(4, Math.floor(w / 300))));
  const rows = Math.ceil(endpoints.length / cols);
  const cell = w / cols;
  const boxWidth = Math.min(190, cell - 80);
  const height = 156 + (rows - 1) * 168;
  const graph = svg("svg", { class: "a-dash-topo-svg", viewBox: `0 0 ${w} ${height}`, role: "img", "aria-label": `${pair.group_name || "监测分组"}链路拓扑`, "data-refresh-key": `${pair.id}-graph` });
  const points = endpoints.map((ep, index) => {
    const row = Math.floor(index / cols), col = index % cols;
    return { x: cell * ((row % 2 ? cols - col - 1 : col) + .5), y: 58 + row * 168 };
  });
  const active = ["preparing", "running"].includes(pair.active_state) || pair.availability?.running;
  const stopped = ["stopping", "cleanup_pending"].includes(pair.active_state);
  const edges = svg("g");
  for (let i = 0; i < points.length - 1; i++) {
    const a = points[i], b = points[i + 1];
    const vertical = a.x === b.x;
    const sign = b.x > a.x ? 1 : -1;
    const sample = pair.latest?.hops?.find(hop => hop.hop_index === i);
    const directions = [["去", sample?.forward || (endpoints.length === 2 ? pair.latest?.forward : null)], ...(pair.bidirectional ? [["回", sample?.reverse || (endpoints.length === 2 ? pair.latest?.reverse : null)]] : [])];
    directions.forEach(([label, dir], direction) => {
      const offset = pair.bidirectional ? direction ? 5 : -5 : 0;
      let x1 = vertical ? a.x + offset : a.x + sign * boxWidth / 2;
      let y1 = vertical ? a.y + 38 : a.y + offset;
      let x2 = vertical ? b.x + offset : b.x - sign * boxWidth / 2;
      let y2 = vertical ? b.y - 38 : b.y + offset;
      if (direction) [x1, y1, x2, y2] = [x2, y2, x1, y1];
      const status = probeDirectionStatus(dir, pair.availability);
      const color = stopped ? colors[""] : active ? colors.warn : colors[status.tone] || colors[""];
      const line = `M${x1} ${y1} L${x2} ${y2}`;
      const group = svg("g", { "data-refresh-key": `${pair.id}-edge-${i}-${direction}`, "data-hop": i, "data-direction": direction ? "reverse" : "forward" });
      group.append(svg("path", { d: line, fill: "none", stroke: color, "stroke-width": 2, opacity: .3 }));
      group.append(svg("path", { d: line, fill: "none", stroke: color, "stroke-width": 3, "stroke-linecap": "round", "stroke-dasharray": "2 34", class: !stopped && pair.availability?.ready !== false && (active || pair.auto_enabled && dir?.reached) ? "a-dash-topo-flow-pulse" : "" }));
      const lx = vertical ? a.x + (direction ? 18 : -18) : (a.x + b.x) / 2;
      const ly = vertical ? (a.y + b.y) / 2 - 4 : a.y + (direction ? 76 : 58);
      const state = stopped ? "停止中" : active ? "探测中" : metric(dir);
      const text = svg("text", { x: lx, y: ly, fill: color, "font-size": 11, "text-anchor": vertical ? direction ? "start" : "end" : "middle" });
      if (vertical && !active && !stopped && dir?.reached) {
        text.append(svg("tspan", { x: lx }, `${label} ${dir.avg_ms?.toFixed(1) || "—"} ms`), svg("tspan", { x: lx, dy: 16 }, `丢包 ${Number((dir.loss_percent || 0).toFixed(1))}%`));
      } else text.textContent = `${label} ${state}`;
      group.append(text);
      edges.append(group);
    });
  }
  graph.append(edges);
  endpoints.forEach((ep, index) => {
    const { x, y } = points[index];
    const state = !ep.agent_id ? "外部 IP" : ep.granted === false ? "未授权" : ep.online === false ? "离线" : ep.ping === false ? "无 Ping 能力" : "在线";
    const color = ["离线", "未授权", "无 Ping 能力"].includes(state) ? colors.bad : colors.ok;
    const group = svg("g", { "data-refresh-key": `${pair.id}-vertex-${index}` });
    group.append(svg("title", {}, `${ep.name || ep.address} · ${ep.address} · ${state}`));
    group.append(svg("rect", { x: x - boxWidth / 2, y: y - 38, width: boxWidth, height: 76, rx: 8, fill: "var(--paper)", stroke: "var(--border)" }));
    group.append(svg("circle", { cx: x - boxWidth / 2 + 14, cy: y - 23, r: 3, fill: color, class: active && !stopped ? "a-dash-topo-beacon-live" : "" }));
    group.append(svg("text", { x: x - boxWidth / 2 + 24, y: y - 19, fill: color, "font-size": 11 }, state));
    const name = Array.from(ep.name || ep.address || "节点");
    const limit = Math.floor((boxWidth - 24) / 12);
    group.append(svg("text", { x, y: y + 3, fill: "var(--ink)", "font-size": 12, "font-weight": 600, "text-anchor": "middle" }, name.length > limit ? name.slice(0, limit - 1).join("") + "…" : name.join("")));
    const address = ep.address || "";
    group.append(svg("text", { x, y: y + 23, fill: "var(--soft-ink)", "font-size": 10, "text-anchor": "middle" }, address.length > 26 ? address.slice(0, 23) + "…" : address));
    graph.append(group);
  });
  return graph;
}
