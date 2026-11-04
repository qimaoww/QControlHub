import { node, button, actions, stack, section, table, badge, field, muted, query, list, notice, code } from "./ui.js";
import { date } from "./operations.js";

const engines = [["", "全部内核"], "mihomo", "xray", "sing-box", "ss-rust"];

export async function logsPage(ctx) {
  const sources = list(await ctx.api("/core-log-sources"));
  const results = node("div");
  const metadata = node("div", { className: "a-card-body" });
  let filters = {}, records = [];
  const sourceOptions = sources.map((source, index) => [String(index),
    `${source.name || source.agent_name || source.agent_id} · ${source.engine} · ${source.instance_id === "main" ? "主实例" : source.username || source.instance_id}`]);
  const form = ctx.form([
    node("div", { className: "a-form-grid" },
      field("日志来源", "source", "", { options: [["", "全部获授权来源"], ...sourceOptions] }),
      field("级别", "level", "", { options: [["", "全部"], ["debug", "调试"], ["info", "信息"], ["warning", "警告"], ["error", "错误"], ["critical", "严重错误"]] }),
      field("搜索内容", "q", "", { type: "search" }),
      field("每个内核条数", "limit", 200, { type: "number", min: 1, max: 2000 })),
  ], "查询日志", async values => {
    const source = values.source === "" ? null : sources[Number(values.source)];
    filters = { level: values.level, q: values.q, limit: values.limit,
      agent_id: source?.agent_id, engine: source?.engine, instance_id: source?.instance_id, user_id: source?.owner_id };
    records = list(await ctx.api(query("/core-logs", filters)));
    draw();
  });
  function draw() {
    metadata.replaceChildren(muted(`已读取 ${records.length} 条日志`));
    results.replaceChildren(table(["时间", "节点", "实例", "内核", "级别", "内容"], records.map(record => [
      date(record.logged_at), record.agent_id, record.instance_id === "main" ? "主实例" : record.instance_id || "—",
      record.engine, badge(record.level, ["critical", "error"].includes(record.level) ? "bad" : ""),
      node("span", { className: "a-log-message" }, record.message),
    ]), { paginate: 50, label: "内核日志" }));
  }
  records = list(await ctx.api("/core-logs?limit=200"));
  draw();
  const older = button("读取更早日志", event => void ctx.mutate(event.currentTarget, async () => {
    if (!records.length) return;
    const before = Math.min(...records.map(record => record.id));
    const next = list(await ctx.api(query("/core-logs", { ...filters, before })));
    const ids = new Set(records.map(record => record.id));
    records.push(...next.filter(record => !ids.has(record.id)));
    draw();
    if (!next.length) older.disabled = true;
  }));
  return stack(section("日志筛选", node("div", { className: "a-card-body" }, form)),
    section("内核日志", stack(metadata, results, node("div", { className: "a-card-body" }, actions(older)))));
}

export async function connectionsPage(ctx) {
  const results = node("div");
  const summary = node("div", { className: "a-card-body a-actions" });
  const until = new Date(), since = new Date(until.getTime() - 24 * 3600_000);
  const localValue = value => new Date(value.getTime() - value.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
  let filters = { locations: "cached", limit: 100 };
  let nextCursor = "", nextBefore = 0;
  const previous = [];
  const pageTools = actions();
  const form = ctx.form([
    node("div", { className: "a-form-grid" },
      field("开始时间", "since", localValue(since), { type: "datetime-local", required: true }),
      field("结束时间", "until", localValue(until), { type: "datetime-local", required: true }),
      field("节点 ID", "agent_id", ""),
      field("内核", "engine", "", { options: engines }),
      field("客户端 IP", "client_ip", ""), field("端口", "port", "", { type: "number", min: 1, max: 65535 }),
      field("协议", "protocol", ""), field("入站", "inbound", ""),
      field("传输", "transport", ""),
      field("分组", "group_by", "", { options: [["", "逐条记录"], ["ip", "按客户端 IP"]] })),
    field("包含非公网地址", "include_non_public", false, { type: "checkbox" }),
  ], "查询连接", async (values, element) => {
    filters = { ...values, since: new Date(values.since).toISOString(), until: new Date(values.until).toISOString(),
      include_non_public: element.elements.include_non_public.checked, locations: "cached", limit: 100 };
    previous.length = 0;
    await load();
  });
  async function load(cursor = "", before = 0) {
    const data = await ctx.api(query("/client-connections", { ...filters, cursor, before: before || "" }));
    if (!ctx.current()) return;
    nextCursor = data.next_cursor || "";
    nextBefore = data.next_before || 0;
    summary.replaceChildren(badge(`连接记录 ${data.flows ?? 0}`), badge(`客户端 IP ${data.ips ?? 0}`),
      muted("根据内核日志观测，不代表当前在线会话数"));
    results.replaceChildren(table(["客户端", "属地", "节点", "内核", "协议 / 传输", "入站 / 端口", "首次发现", "最后发现"],
      list(data.records).map(record => [
        record.client_ip,
        [record.location?.country, record.location?.province].filter(Boolean).join(" · ") || "未知",
        record.agent_name || record.agent_id, record.engine,
        [record.protocol, record.transport].filter(Boolean).join(" / "),
        [record.inbound, record.local_port || ""].filter(Boolean).join(" / "),
        date(record.first_seen), date(record.last_seen),
      ]), { label: "客户端连接" }));
    pageTools.replaceChildren(
      button("上一页", event => void ctx.mutate(event.currentTarget, async () => {
        const target = previous.pop();
        if (target) await load(...target);
      }), { disabled: !previous.length }),
      button("下一页", event => void ctx.mutate(event.currentTarget, async () => {
        previous.push([cursor, before]);
        await load(nextCursor, nextCursor ? 0 : nextBefore);
      }), { disabled: !nextCursor && !nextBefore }));
    const failures = list(data.sources).filter(source => source.status !== "available" && source.detail);
    sourceState.replaceChildren(...failures.map(source => notice(`${source.name || source.agent_name || source.agent_id}：${source.detail}`, "warn")));
  }
  const sourceState = node("div", { className: "a-card-body a-stack" });
  await load();
  return stack(section("连接筛选", node("div", { className: "a-card-body" }, form)),
    section("客户端连接", stack(summary, results, sourceState, node("div", { className: "a-card-body" }, pageTools))));
}
