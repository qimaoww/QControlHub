// Ignore heartbeat metrics and other runtime fields that this page never shows.
// Names, membership, presence and collection availability still update at once.
export function coreLogAgentPresentation(agents) {
  return JSON.stringify(agents.map(agent => [agent.id, agent.name, agent.status,
    (agent.features || []).includes("core-logs-v1"),
    (agent.features || []).includes("core-log-status-v1"),
    Object.entries(agent.runtime || {}).map(([engine, runtime]) =>
      [engine, runtime?.installed, runtime?.core_log_status, runtime?.core_log_source, runtime?.core_log_last_at]),
  ]));
}

// Log collection is reported per (node, engine, instance) source. The control
// plane already scopes this directory by permission, so one broken engine or
// shared instance must never speak for the sources beside it.
const sourceTimestamp = (source) => source?.last_log_at || source?.core_log_last_at || "";
const sourceTransport = (source) => source?.source || source?.core_log_source || "";

const TRANSPORT_NAMES = {
  journal: "journal",
  "journal-host": "journal（主机）",
  openrc: "OpenRC",
  "systemd-fallback": "systemd（回退）",
  file: "日志文件",
};

export const coreLogTransportName = (value) => TRANSPORT_NAMES[value] || value || "";

export const coreLogInstanceName = (source) =>
  !source?.instance_id || source.instance_id === "main"
    ? "主实例"
    : `${source.username || "共享实例"} · ${source.instance_id}`;

const sourceLocation = (source, engineName) =>
  [source?.name, engineName(source?.engine || ""), coreLogInstanceName(source)]
    .filter(Boolean)
    .join(" · ");

const parseTime = (value) => {
  const time = Date.parse(value || "");
  return Number.isFinite(time) ? time : 0;
};

// A missing or invalid timestamp stays silent instead of inventing an age.
export function coreLogLastSeen(lastAt, now = Date.now()) {
  const time = typeof lastAt === "number" ? lastAt : parseTime(lastAt);
  if (!time) return "";
  const seconds = Math.max(0, Math.floor((now - time) / 1000));
  if (seconds < 60) return "刚刚";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  return `${Math.floor(hours / 24)} 天前`;
}

// The four reportable states plus the legacy capability states the directory
// can still return for nodes that have not adopted core-log-status-v1.
export function coreLogSourceState(source, { now = Date.now(), engineName = String } = {}) {
  const where = sourceLocation(source, engineName);
  const transport = coreLogTransportName(sourceTransport(source));
  const failed = (reason) => `${where} 读取失败${reason ? `（${reason}）` : ""}。`;
  switch (source?.status || "") {
    case "active": {
      const lastAt = parseTime(sourceTimestamp(source));
      return lastAt
        ? { key: "active", title: `采集正常，最近日志 ${coreLogLastSeen(lastAt, now)}`, detail: "", lastAt, transport }
        : { key: "ready", title: "采集已就绪，尚未收到日志", detail: "", lastAt: 0, transport };
    }
    case "waiting":
      return { key: "waiting", title: "等待日志", detail: `${where} 已就绪，等待首条日志。`, lastAt: 0, transport };
    case "pending":
    case "":
      return { key: "pending", title: "等待日志", detail: `${where} 采集状态尚未上报。`, lastAt: 0, transport };
    case "not-configured":
      return { key: "not-configured", title: "未配置", detail: `${where} 尚无托管配置，部署后自动采集。`, lastAt: 0, transport };
    case "failed":
      return { key: "failed", title: "日志采集失败", detail: failed(source?.error), lastAt: 0, transport };
    case "offline":
      return { key: "offline", title: "节点离线", detail: "采集已暂停，可查看保留期内的历史日志。", lastAt: 0, transport };
    case "unsupported":
      return { key: "unsupported", title: "Agent 功能不足", detail: "请升级 Agent 以采集并区分共享实例日志。", lastAt: 0, transport };
    case "status-unavailable":
      return { key: "status-unavailable", title: "日志状态能力不可用", detail: "此 Agent 无法核验来源状态，请升级 Agent。", lastAt: 0, transport };
    case "not-installed":
      return { key: "not-installed", title: "内核尚未安装", detail: `${where} 未安装，可查看保留期内的历史日志。`, lastAt: 0, transport };
    default:
      return { key: "pending", title: "等待日志", detail: `${where} 采集状态尚未上报。`, lastAt: 0, transport };
  }
}

// Aggregate only the sources inside the active filter scope, so a failure on
// one engine or shared instance is reported by name instead of hiding or
// failing the healthy sources that share the page.
export function coreLogSourceStatus(sources, { now = Date.now(), engineName = String, filtered = false } = {}) {
  const states = (sources || []).map((source) => coreLogSourceState(source, { now, engineName }));
  const of = (key) => states.filter((state) => state.key === key);
  const healthy = of("active");
  const ready = of("ready");
  const failed = of("failed");
  const waiting = [...of("waiting"), ...of("pending")];
  const unconfigured = of("not-configured");
  const offline = of("offline");
  const unsupported = of("unsupported");
  const unavailable = of("status-unavailable");
  const missing = of("not-installed");
  const allMissing = missing.length > 0 && missing.length === states.length;
  const usable = healthy.length + ready.length;
  const named = (items, more) =>
    items.slice(0, 2).map((state) => state.detail.replace(/。$/, "")).join("；") +
    (items.length > 2 ? `；另有 ${items.length - 2} ${more}` : "");
  const newest = healthy.slice().sort((a, b) => b.lastAt - a.lastAt)[0] || null;

  let emptyTitle = "";
  let emptyDetail = "";
  let noticeTitle = "";
  let noticeDetail = "";
  if (filtered && healthy.length) {
    // Only a delivered source can have rows hidden by the keyword or level;
    // waiting sources have nothing to match yet.
    emptyTitle = "没有匹配日志";
  } else if (newest) {
    emptyTitle = newest.title;
  } else if (ready.length) {
    emptyTitle = "采集已就绪，尚未收到日志";
  } else if (failed.length) {
    emptyTitle = "日志采集失败";
    emptyDetail = `${named(failed, "个来源读取失败")}。`;
  } else if (offline.length) {
    emptyTitle = "节点离线";
    emptyDetail = "采集已暂停，可查看保留期内的历史日志。";
  } else if (unsupported.length) {
    emptyTitle = "Agent 功能不足";
    emptyDetail = "请升级 Agent 以采集并区分共享实例日志。";
  } else if (unavailable.length) {
    emptyTitle = "日志状态能力不可用";
    emptyDetail = "此 Agent 无法核验来源状态，请升级 Agent。";
  } else if (unconfigured.length) {
    emptyTitle = "未配置";
    emptyDetail = `${named(unconfigured, "个来源尚未配置")}。`;
  } else if (waiting.length) {
    emptyTitle = "等待日志";
    emptyDetail = `${named(waiting, "个来源等待首条日志")}。`;
  } else if (allMissing) {
    emptyTitle = "内核尚未安装";
    emptyDetail = `${named(missing, "个来源未安装")}。`;
  }
  // A healthy headline must not hide a broken or unconfigured sibling when no
  // rows are shown; the notice carries the same location once rows exist.
  if (failed.length && emptyTitle !== "日志采集失败") {
    emptyDetail = `${named(failed, "个来源读取失败")}。`;
  } else if (!failed.length && unconfigured.length && emptyTitle !== "未配置") {
    emptyDetail = `${named(unconfigured, "个来源尚未配置")}。`;
  }

  if (failed.length) {
    noticeTitle = usable ? "部分来源采集失败" : "日志采集失败";
    noticeDetail = `${named(failed, "个来源读取失败")}。`;
  } else if (offline.length) {
    noticeTitle = "节点离线";
    noticeDetail = "采集已暂停，可查看保留期内的历史日志。";
  } else if (unsupported.length) {
    noticeTitle = "Agent 功能不足";
    noticeDetail = "请升级 Agent 以采集并区分共享实例日志。";
  } else if (unavailable.length) {
    noticeTitle = "日志状态能力不可用";
    noticeDetail = "此 Agent 无法核验来源状态，请升级 Agent。";
  } else if (allMissing) {
    // Installation is a source fact, independent of collection readiness and
    // historical rows. Only a wholly uninstalled scope can use this headline.
    noticeTitle = "内核尚未安装";
    noticeDetail = `${named(missing, "个来源未安装")}。`;
  } else if (unconfigured.length && !usable) {
    noticeTitle = "未配置";
    noticeDetail = `${named(unconfigured, "个来源尚未配置")}。`;
  } else if (waiting.length && !usable) {
    noticeTitle = "等待日志";
    noticeDetail = `${named(waiting, "个来源等待首条日志")}。`;
  }

  const reported = states.filter((state) => state.transport);
  const transports = [...new Set(reported.map((state) => state.transport))];
  const newestAt = Math.max(0, ...states.map((state) => state.lastAt));
  const meta = [
    reported.length === states.length && transports.length === 1 ? `来源 ${transports[0]}` : "",
    newestAt ? `最近日志 ${coreLogLastSeen(newestAt, now)}` : "",
  ].filter(Boolean).join(" · ");

  return { emptyTitle, emptyDetail, noticeTitle, noticeDetail, meta, states };
}

// Node-level facts that no single source can report.
export function coreLogGuardStatus(selectedAgent, filters = {}, can = () => true) {
  if (filters.agent_id && !can("agents.read"))
    return { title: "无法核验采集状态", detail: "无权读取节点状态；可按现有权限查看历史日志。" };
  if (filters.agent_id && !selectedAgent)
    return { title: "节点状态不可用", detail: "无法读取所选节点状态；历史日志仍可查看。" };
  if (selectedAgent?.status === "offline")
    return { title: "节点离线", detail: "采集已暂停；仅可查看保留期内的历史记录。" };
  if (selectedAgent && selectedAgent.status !== "online")
    return { title: "节点状态不可用", detail: "无法确认当前节点是否仍在采集日志。" };
  return { title: "", detail: "" };
}
