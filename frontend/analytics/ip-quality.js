import { ipQualityReportRecord, ipQualityBlockReason, ipQualityValue, ipQualityEntries, ipQualityStatusName } from "../modules/ip-quality-model.js";
import { autoFilter, node, button, actions, stack, grid, section, table, badge, field, muted, list, pathID, notice, query, code } from "./ui.js";
import { date, status, taskDetail } from "./operations.js";

export async function ipQualityPage(ctx) {
  const agents = list(await ctx.api("/agents"));
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  let history = await ctx.api(query("/ip-quality", { timezone }));
  let selectedID = agents.some(agent => agent.id === ctx.state.data.ipQualityNode) ? ctx.state.data.ipQualityNode : agents[0]?.id;
  const navigation = node("nav", { className: "a-ipq-nodes", "aria-label": "检测节点" });
  const results = node("div", { className: "a-ipq-detail a-stack" });
  const filter = autoFilter(ctx, [
    field("检测日期", "date", "", { options: [["", "最新检测"], ...list(history.dates).map(day => [day, day])] }),
  ], async (values, _, request) => {
    const received = await request.api(query("/ip-quality", { date: values.date, timezone }));
    if (!request.current()) return;
    history = received;
    draw();
  });
  filter.classList.add("a-ipq-filter");
  const display = value => ipQualityValue(value).replace(/(?:\x1b|\\x1b|x1b)\[[0-?]*[ -/]*[@-~]/gi, "");
  const facts = entries => node("dl", { className: "a-ipq-facts" }, entries.map(([key,value]) => node("div", {}, node("dt", {}, key), node("dd", {}, display(value)))));
  function reportView(report) {
    const pairs = data => table(["来源", "结果"], ipQualityEntries(data).map(([name,value]) => [name,display(value)]));
    const factorNames = { CountryCode: "国家 / 地区", Proxy: "代理", Tor: "Tor", VPN: "VPN", Server: "数据中心", Abuser: "滥用", Robot: "机器人" };
    const factors = ipQualityEntries(report.Factor);
    const providers = [...new Set(factors.flatMap(([,values]) => ipQualityEntries(values).map(([name]) => name)))];
    const factorTable = table(["来源", ...factors.map(([name]) => factorNames[name] || name)],
      providers.map(provider => [provider, ...factors.map(([,values]) => display(values?.[provider]))]));
    const details = node("details", {}, node("summary", {}, "上游原始 JSON"), code(JSON.stringify(report, null, 2)));
    return stack(
      section(report.Head?.IP || "IP 检测报告", facts([
        ["ASN", report.Info?.ASN], ["运营商", report.Info?.Organization], ["地区", report.Info?.Region?.Name],
        ["城市", report.Info?.City?.Name], ["检测时间", report.Head?.Time], ["脚本发行版本", report.Head?.Version],
      ])),
      grid(section("风险评分", pairs(report.Score)), section("IP 用途", pairs(report.Type?.Usage))),
      section("风险因子", factorTable),
      section("流媒体与 AI", table(["平台", "状态", "区域", "类型"], ipQualityEntries(report.Media).map(([name, value]) => [
        name, display(value?.Status), display(value?.Region), display(value?.Type),
      ]))),
      section("邮件连通性", pairs(Object.fromEntries(ipQualityEntries(report.Mail).filter(([name]) => name !== "DNSBlacklist")))),
      section("DNS 黑名单", pairs(report.Mail?.DNSBlacklist)), details);
  }
  function draw() {
    const byID = new Map(list(history.records).map(record => [record.agent_id, record]));
    const schedules = new Map(list(history.schedules).map(schedule => [schedule.agent_id, schedule]));
    navigation.replaceChildren(...agents.map(agent => {
      const record = byID.get(agent.id);
      return button(stack(node("strong", {}, agent.name), muted(record ? ipQualityStatusName(record.status) : "暂无检测记录")), () => {
        selectedID = agent.id; ctx.state.data.ipQualityNode = agent.id; draw();
      }, { className: "a-ipq-node", "aria-pressed": String(agent.id === selectedID) });
    }));
    const agent = agents.find(agent => agent.id === selectedID);
    if (!agent) { results.replaceChildren(node("p", { className: "a-empty" }, "暂无可检测的节点")); return; }
    {
      const record = byID.get(agent.id);
      const report = ipQualityReportRecord(record, history.date || "latest", timezone);
      const blocked = ipQualityBlockReason(agent, record);
      const editable = ctx.can("agents.manage", agent) && ctx.can("tasks.execute");
      const tools = actions(
        editable && button("立即检测", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm(`在 ${agent.name} 运行 IP 质量检测？`, "开始检测") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            const task = await ctx.api("/ip-quality", { method: "POST", body: { agent_id: agent.id } });
            ctx.announce("检测任务已提交");
            if (ctx.can("tasks.read")) await taskDetail(ctx, task.id);
          });
        }, { disabled: Boolean(blocked), title: blocked }),
        editable && button(schedules.get(agent.id)?.enabled ? "关闭定时检测" : "启用定时检测", event => void ctx.mutate(event.currentTarget, async () => {
          const saved = await ctx.api(`/ip-quality/schedules/${pathID(agent.id)}`, { method: "PUT", body: { enabled: !schedules.get(agent.id)?.enabled } });
          schedules.set(agent.id, saved);
          history = await ctx.api(query("/ip-quality", { date: history.date, timezone }));
          draw();
          ctx.announce("定时检测设置已保存");
        })));
      const archives = report?.archives || [];
      const archiveViews = archives.map(archive => {
        const link = `/api/v1/ip-quality/${pathID(report.task_id)}/archives/${archive.family}`;
        const image = node("img", { src: link, alt: `${agent.name} IPv${archive.family} 检测报告`, loading: "lazy", className: "a-report-image" });
        const error = notice("", "error");
        const retry = button("重新加载图片", () => { image.src = `${link}?reload=${Date.now()}`; error.textContent = ""; ctx.visible(retry, false); });
        ctx.visible(retry, false);
        image.addEventListener("error", () => { error.textContent = "报告图片加载失败，可重试或查看结构化报告。"; ctx.visible(retry, true); });
        return button(`IPv${archive.family} 报告图片`, () => ctx.dialog(`${agent.name} · IPv${archive.family}`, stack(image, error, retry), { wide: true }));
      });
      results.replaceChildren(section(agent.name, node("div", { className: "a-card-body" }, stack(
        actions(status(agent.status), record ? status(record.status) : badge("尚未检测"),
          badge(schedules.get(agent.id)?.enabled ? "定时检测已启用" : "定时检测已关闭")),
        record && muted(`检测时间 · ${date(record.finished_at || record.created_at)}`),
        blocked && muted(blocked),
        record?.error && notice(record.error),
        report && report !== record && muted("显示最近一次成功报告"),
        archiveViews.length > 0 && actions(archiveViews),
        !report && node("p", { className: "a-empty" }, record?.status === "succeeded" ? "检测已完成，暂无可用报告" : ["pending", "running"].includes(record?.status) ? "检测进行中，可在任务详情查看状态" : "暂无报告，可运行检测或切换日期查看记录"),
      )), tools), ...list(report?.result?.reports).map(reportView));
    }
  }

  draw();
  return stack(filter, node("div", { className: "a-ipq-layout" }, navigation, results));
}
