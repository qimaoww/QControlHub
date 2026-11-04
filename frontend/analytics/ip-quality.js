import { ipQualityReportRecord, ipQualityBlockReason, ipQualityValue, ipQualityEntries } from "../modules/ip-quality-model.js";
import { node, button, actions, stack, grid, section, table, badge, field, muted, list, pathID, notice, query, code } from "./ui.js";
import { date, status, taskDetail } from "./operations.js";

export async function ipQualityPage(ctx) {
  const agents = list(await ctx.api("/agents"));
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  let history = await ctx.api(query("/ip-quality", { timezone }));
  const results = node("div", { className: "a-stack" });
  const filter = ctx.form([
    field("检测日期", "date", "", { options: [["", "最新检测"], ...list(history.dates).map(day => [day, day])] }),
  ], "查看记录", async values => {
    history = await ctx.api(query("/ip-quality", { date: values.date, timezone }));
    draw();
  });
  function reportView(report) {
    const pairs = data => table(["项目", "结果"], ipQualityEntries(data).map(([key, value]) => [key, ipQualityValue(value)]), { paginate: 0 });
    const details = node("details", {}, node("summary", {}, "上游原始 JSON"), code(JSON.stringify(report, null, 2)));
    return stack(
      section(report.Head?.IP || "IP 检测报告", table(["项目", "内容"], [
        ["ASN", report.Info?.ASN], ["运营商", report.Info?.Organization], ["地区", report.Info?.Region?.Name],
        ["城市", report.Info?.City?.Name], ["检测时间", report.Head?.Time], ["脚本发行版本", report.Head?.Version],
      ])),
      grid(section("风险评分", pairs(report.Score)), section("IP 用途", pairs(report.Type?.Usage))),
      section("风险因子", grid(ipQualityEntries(report.Factor).map(([name, data]) => section(
        ({ CountryCode: "国家 / 地区", Proxy: "代理", Tor: "Tor", VPN: "VPN", Server: "数据中心", Abuser: "滥用", Robot: "机器人" })[name] || name, pairs(data))))),
      section("流媒体与 AI", table(["平台", "状态", "区域", "类型"], ipQualityEntries(report.Media).map(([name, value]) => [
        name, ipQualityValue(value?.Status), ipQualityValue(value?.Region), ipQualityValue(value?.Type),
      ]))),
      section("邮件连通性", pairs(Object.fromEntries(ipQualityEntries(report.Mail).filter(([name]) => name !== "DNSBlacklist")))),
      section("DNS 黑名单", pairs(report.Mail?.DNSBlacklist)), details);
  }
  function draw() {
    const byID = new Map(list(history.records).map(record => [record.agent_id, record]));
    const schedules = new Map(list(history.schedules).map(schedule => [schedule.agent_id, schedule]));
    results.replaceChildren(...agents.map(agent => {
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
        return stack(image, error, retry);
      });
      return section(agent.name, node("div", { className: "a-card-body" }, stack(
        record ? actions(status(record.status), muted(date(record.finished_at || record.created_at))) : muted("暂无检测记录"),
        record?.error && notice(record.error),
        report?.last_successful && muted("显示最近一次成功报告"),
        archiveViews,
        report && button("查看结构化报告", () => ctx.dialog(`${agent.name} · IP 质量`,
          stack(list(report.result?.reports).map(reportView)), { wide: true })),
        !report && record?.status === "succeeded" && muted("检测已完成，暂无可用报告"),
      )), tools);
    }));
  }
  draw();
  return stack(section("历史记录", node("div", { className: "a-card-body" }, filter)), results);
}
