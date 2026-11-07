import { agentTimeZoneStatus, agentTimeZoneEffective, agentClockSyncPresentation } from '../modules/agent-time-zone-model.js';
import { node, section, field, actions, muted, pathID } from './ui.js';

// Keep the draft's compare-and-swap revision separate from live acknowledgments.
export function nodeTimeZone(ctx, agent, canManage) {
  let latest = { ...agent.time_zone }, baseline = { ...latest }, busy = false, failure = '', focus;
  const state = muted(''), effective = muted(''), clockStatus = muted(''), clockOffset = muted('');
  state.dataset.timezoneStatus = ''; effective.dataset.timezoneApplied = '';
  clockStatus.dataset.clockSyncStatus = ''; clockOffset.dataset.clockSyncOffset = '';
  const current = () => ctx.current() && form.isConnected;
  const values = () => ({ mode: form.elements.mode.value, time_zone: form.elements.time_zone.value });
  const same = (a, b) => (a.mode || 'auto') === (b.mode || 'auto') && (a.time_zone || '') === (b.time_zone || '');
  const form = ctx.form([
    node('div', { className: 'a-grid' },
      field('模式', 'mode', latest.mode || 'auto', { options: [['auto', '自动检测'], ['custom', '手动设置']] }),
      field('IANA 时区', 'time_zone', latest.time_zone || '', { maxlength: 100, 'aria-label': 'IANA 时区',
        suggestions: ['UTC','Asia/Shanghai','Asia/Hong_Kong','Asia/Tokyo','Asia/Singapore','Europe/London','Europe/Berlin','America/New_York','America/Los_Angeles','Australia/Sydney'] })),
    actions(state, effective),
    actions(muted('系统时钟同步'), clockStatus, clockOffset),
  ], '应用', async input => {
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
      ctx.announce('时区设置已保存');
    } catch (error) {
      if (current()) failure = error.message;
      throw error;
    } finally { busy = false; if (current()) sync(); }
  });
  form.dataset.agentTimezoneForm = agent.id;
  const submit = form.querySelector('button[type=submit]');
  submit.dataset.timezoneSubmit = '';
  form.elements.mode.dataset.timezoneMode = '';
  form.elements.time_zone.dataset.timezoneInput = '';
  const view = section('Agent 进程时区', node('div', { className: 'a-card-body' }, form));
  view.dataset.agentTimezone = agent.id;
  function sync() {
    const mode = form.elements.mode, input = form.elements.time_zone, allowed = canManage();
    const pending = busy || form.getAttribute('aria-busy') === 'true';
    mode.disabled = !allowed || pending;
    input.disabled = !allowed || pending || mode.value !== 'custom';
    input.required = mode.value === 'custom';
    input.placeholder = mode.value === 'auto' ? '等待检测' : 'Asia/Shanghai';
    input.setCustomValidity(mode.value === 'custom' && !input.value.trim() ? '请输入 IANA 时区，例如 Asia/Shanghai' : '');
    submit.disabled = !allowed || pending;
    submit.textContent = mode.value === 'auto' && latest.time_zone ? '重新检测' : '应用';
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
  form.addEventListener('input', () => { failure = ''; sync(); });
  form.addEventListener('change', () => { failure = ''; sync(); });
  submit.addEventListener('mutationend', () => {
    if (!current()) return;
    sync();
    if (focus?.isConnected && form.contains(focus) && document.activeElement === document.body) focus.focus({ preventScroll: true });
  });
  sync();
  return { view, update };
}
