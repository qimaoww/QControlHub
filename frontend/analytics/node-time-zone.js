import { agentTimeZoneStatus, agentTimeZoneEffective, agentClockSyncPresentation } from '../modules/agent-time-zone-model.js';
import { node, section, field, actions, muted, pathID } from './ui.js';

// Keep the draft's compare-and-swap revision separate from live acknowledgments.
export function nodeTimeZone(ctx, agent, canManage, form) {
  let latest = { ...agent.time_zone }, baseline = { ...latest }, busy = false, failure = '', focus;
  const state = muted(''), effective = muted(''), clockStatus = muted(''), clockOffset = muted('');
  state.dataset.timezoneStatus = ''; effective.dataset.timezoneApplied = '';
  clockStatus.dataset.clockSyncStatus = ''; clockOffset.dataset.clockSyncOffset = '';
  const current = () => ctx.current() && form.isConnected;
  const values = () => ({ mode: form.elements.mode.value, time_zone: form.elements.time_zone.value });
  const same = (a, b) => (a.mode || 'auto') === (b.mode || 'auto') && (a.time_zone || '') === (b.time_zone || '');
  const fields = [
    node('div', { className: 'a-grid' },
      field('模式', 'mode', latest.mode || 'auto', { options: [['auto', '自动检测'], ['custom', '手动设置']] }),
      field('IANA 时区', 'time_zone', latest.time_zone || '', { maxlength: 100, 'aria-label': 'IANA 时区',
        suggestions: ['UTC','Asia/Shanghai','Asia/Hong_Kong','Asia/Tokyo','Asia/Singapore','Europe/London','Europe/Berlin','America/New_York','America/Los_Angeles','Australia/Sydney'] })),
    actions(state, effective),
    actions(muted('系统时钟同步'), clockStatus, clockOffset),
  ];
  async function save() {
    const input = values();
    if (!canManage()) return;
    if (input.mode === 'custom' && !input.time_zone.trim()) throw new Error('请输入 IANA 时区，例如 Asia/Shanghai');
    const submitted = values();
    busy = true; failure = ''; sync();
    try {
      const received = await ctx.api(`/agents/${pathID(agent.id)}/time-zone`, {
        method: 'PUT', body: { mode: input.mode, time_zone: input.mode === 'custom' ? input.time_zone.trim() : '', revision: baseline.revision || 0, retry: input.mode === 'auto' },
        signal: AbortSignal.any([ctx.signal, AbortSignal.timeout(12000)]),
      });
      if (!current() || !canManage()) return;
      const confirmed = latest.revision === received.revision && latest.status === 'applied' && same(latest, received);
      if (Number(received.revision || 0) > Number(latest.revision || 0) || !confirmed && Number(received.revision || 0) === Number(latest.revision || 0)) latest = received;
      agent.time_zone = latest;
      if (same(values(), submitted)) {
        form.elements.mode.value = latest.mode || 'auto';
        form.elements.time_zone.value = latest.time_zone || '';
      }
      baseline = { ...latest };
    } catch (error) {
      if (current()) failure = error.message;
      throw error;
    } finally { busy = false; if (current()) sync(); }
  }
  form.dataset.agentTimezoneForm = agent.id;
  const submit = form.querySelector('button[type=submit]');
  submit.dataset.timezoneSubmit = '';
  const view = section('Agent 进程时区', node('div', { className: 'a-card-body a-stack' }, fields));
  view.dataset.agentTimezone = agent.id;
  function sync() {
    const mode = form.elements.mode, input = form.elements.time_zone, allowed = canManage();
    if (latest.status === 'failed') void ctx.recordFailure?.(Object.assign(new Error(latest.error || 'Agent 时区应用失败，来源未返回具体原因。'),{agentID:agent.id,resource:`/agents/${agent.id}/time-zone`}), `Agent 时区 · ${agent.name || agent.id}`, 'time_zone_application');
    const pending = busy || form.getAttribute('aria-busy') === 'true';
    mode.disabled = !allowed || pending;
    input.disabled = !allowed || pending || mode.value !== 'custom';
    input.required = mode.value === 'custom';
    input.placeholder = mode.value === 'auto' ? '等待检测' : 'Asia/Shanghai';
    input.setCustomValidity(mode.value === 'custom' && !input.value.trim() ? '请输入 IANA 时区，例如 Asia/Shanghai' : '');
    submit.disabled = !allowed || pending;
    mode.dataset.timezoneMode = ''; input.dataset.timezoneInput = '';
    form.dataset.timezoneRevision = String(baseline.revision || 0);
    state.textContent = busy ? mode.value === 'auto' ? '检测中' : '保存中' : failure || agentTimeZoneStatus({ ...agent, time_zone: latest });
    effective.textContent = agentTimeZoneEffective(latest); effective.hidden = !effective.textContent;
    const clock = agentClockSyncPresentation(agent.metrics?.clock_sync);
    clockStatus.textContent = clock.status; clockOffset.textContent = clock.offset;
  }
  function update() {
    const received = agent.time_zone || {};
    if (Number(received.revision || 0) >= Number(latest.revision || 0)) {
      const pristine = same(values(), baseline) || same(values(), received);
      latest = { ...received };
      if (!busy && !failure && pristine) {
        form.elements.mode.value = latest.mode || 'auto';
        form.elements.time_zone.value = latest.time_zone || '';
        baseline = { ...latest };
      }
    }
    sync();
  }
  form.addEventListener('submit', () => { focus = document.activeElement; }, true);
  form.addEventListener('input', event => { if (['mode', 'time_zone'].includes(event.target.name)) failure = ''; sync(); });
  form.addEventListener('change', event => { if (['mode', 'time_zone'].includes(event.target.name)) failure = ''; sync(); });
  submit.addEventListener('mutationend', () => {
    if (!current()) return;
    sync();
    if (focus?.isConnected && form.contains(focus) && document.activeElement === document.body) focus.focus({ preventScroll: true });
  });
  return { view, update, save, dirty: () => !same(values(), baseline) || Boolean(failure) };
}
