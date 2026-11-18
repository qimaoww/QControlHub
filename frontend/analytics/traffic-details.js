import { bindPreferenceFields } from './preferences.js';
import { dialogContext, loadingTable } from "./loading.js";
import { node, button, actions, stack, field, query, list, notice } from './ui.js';
import { bytes, date, trafficDayColumns, trafficDayRows } from './operations.js';
import { protocolName, cycleName, dateInputValue, trafficAccountingState } from '../modules/traffic-model.js';
import { patch } from './live-dom.js';

export function trafficDetails(ctx, policy, agent, options = {}) {
  const summary = stack(), management = actions();
  let historyKey, lastSample, awaitingPeak = false;
  function update(nextPolicy = policy, nextAgent = agent) {
    // The page reconciles policies in place, so retain scalar values across polls.
    const nextKey = JSON.stringify([nextPolicy.received_bytes, nextPolicy.sent_bytes, nextPolicy.reset_generation,
      (nextPolicy.last_collected_at || nextPolicy.last_reported_at || '').slice(0, 10)]);
    const sampled = nextPolicy.last_collected_at || nextPolicy.last_reported_at;
    const peakMayHaveArrived = awaitingPeak && sampled && lastSample && sampled !== lastSample;
    const changed = historyKey !== undefined && (historyKey !== nextKey || peakMayHaveArrived);
    lastSample = sampled;
    historyKey = nextKey;
    policy = nextPolicy; agent = nextAgent;
    patch(management, actions(...(options.canManage?.(policy) ? [
      button('清零', event => void options.change(policy, event.currentTarget, 'reset')),
      policy.quota_enabled !== false && button('取消配额', event => void options.change(policy, event.currentTarget, 'quota')),
      button('删除', event => void options.change(policy, event.currentTarget, 'monitoring'), { tone: 'danger' }),
    ].filter(Boolean) : [])));
    const { accounting, dual, raw, title, hint, partialEgress } = trafficAccountingState(policy);
    const shared = policy.shared_quota;
    const source = { 'core-api': '内核计数', 'nft-dual': '网络层双链路', 'listener': '监听端口', 'nft-listener': '监听端口' }[accounting?.source] || accounting?.source || '监听端口';
    const facts = [
      ['节点', agent?.name || policy.agent_id], ['内核 / 协议', `${policy.engine} / ${protocolName(policy.protocol)}`],
      ['监听端口', String(policy.port)], ['所属用户', policy.owner_name || '当前用户'],
      ['统计周期', `${cycleName(policy.cycle)} · ${policy.period_start && policy.period_end ? `${dateInputValue(policy.period_start)} 至 ${dateInputValue(policy.period_end)}` : '等待周期确认'}`],
      ['接收 / 发送', `${bytes(policy.received_bytes)} / ${bytes(policy.sent_bytes)}`],
      ['已用 / 配额', `${bytes(policy.used_bytes)} / ${policy.quota_enabled === false ? '持续统计' : bytes(policy.limit_bytes)}`],
      ['超额处理', policy.quota_enabled === false ? '未设置配额' : policy.auto_block === false ? '仅统计' : '自动封禁'],
      ['统计来源', source], ['最近采样', date(policy.last_collected_at || policy.last_reported_at)],
    ];
    if (shared) facts.push(['共享额度', `${bytes(shared.used_bytes)} / ${shared.limit_bytes ? bytes(shared.limit_bytes) : '不限量'}`], ['共享授权', shared.revoked ? '已撤销' : '有效']);
    facts.push(['统计口径', title], ['统计说明', hint]);
    if (partialEgress) facts.push(['统计范围', '回环等非全局单播目标仅计入口，出口不补算或翻倍。此为 Mihomo 能力限制。']);
    if (accounting) {
      if (dual) facts.push(['链路明细', '本计量代次累计，不等同于本月总量']);
      for (const [key, label] of [['client_received', '客户端至代理'], ['client_sent', '代理至客户端'], ['target_received', '目标至代理'], ['target_sent', '代理至目标']])
        if (dual && accounting[key] !== undefined) facts.push([label, bytes(accounting[key])]);
      if (accounting.inbound) facts.push(['入站标签', accounting.inbound]);
      if (accounting.outbounds?.length) facts.push(['出站标签', accounting.outbounds.join('、')]);
    }
    const error = node('details', { hidden: !raw }, node('summary', {}, 'Agent 诊断'), node('pre', { className: 'a-code' }, raw));
    patch(summary, stack(node('dl', { className: 'a-traffic-facts' }, facts.map(([label, value]) => node('div', { 'data-refresh-key': label }, node('dt', {}, label), node('dd', {}, value)))), error));
    if (changed && input.value === new Date().toISOString().slice(0, 7)) {
      awaitingPeak = false;
      if (request) pending = true;
      else void read();
    }
  }
  const month = field('月份（UTC）', 'month', new Date().toISOString().slice(0, 7), { type: 'month', required: true });
  const input = month.querySelector('input');
  const days = loadingTable(trafficDayColumns, '每日流量', { emptyText: '此月暂无流量记录', rowKey: row => row[0] });
  const feedback = node('div', { hidden: true });
  const box = ctx.dialog(`${policy.name || '端口流量'} · ${policy.port}`, stack(summary, month, days, feedback, management), { wide: true });
  box.setAttribute('data-traffic-details', policy.id);
  const local = dialogContext(ctx, box);
  let request, pending = false;
  function changeMonth() {
    request?.abort(); request = null; pending = false;
    days.updateRows([]); days.setLoading(input.checkValidity());
    feedback.hidden = true;
    days.removeAttribute('aria-busy');
    void read();
  }
  async function read() {
    if (!local.current() || !input.checkValidity() || !input.value) return;
    const active = new AbortController();
    request = active;
    const signal = AbortSignal.any([local.signal, active.signal]);
    days.setAttribute('aria-busy', 'true');
    try {
      const result = await local.api(query('/traffic-usage', { policy_id: policy.id, agent_id: policy.agent_id, month: input.value }), { signal });
      if (signal.aborted || !local.current()) return;
      const rows = list(result.days);
      const latestDay = lastSample?.slice(0, 10);
      const today = rows.find(row => row.day === latestDay);
      // Read the first valid zero interval too. Silence changes no byte totals.
      awaitingPeak = today?.peak_sampled === false && latestDay === new Date().toISOString().slice(0, 10);
      days.updateRows(trafficDayRows(rows));
      feedback.hidden = true;
    } catch (error) {
      if (signal.aborted || !local.current()) return;
      if ([401, 403, 404].includes(error.status)) days.updateRows([]);
      days.setReadError();
      feedback.replaceChildren(notice(error.message), actions(button('重试', () => { if (!request) void read(); })));
      feedback.hidden = false;
    } finally {
      if (request === active) {
        days.removeAttribute('aria-busy');
        request = null;
        if (pending) { pending = false; void read(); }
      }
    }
  }
  update();
  month.addEventListener('change', changeMonth);
  bindPreferenceFields(local, 'traffic', [month], { restore: changeMonth });
  const stop = () => box.close();
  ctx.signal.addEventListener('abort', stop, { once: true });
  box.addEventListener('close', () => ctx.signal.removeEventListener('abort', stop), { once: true });
  void read();
  return { update, box, policyID: policy.id };
}
