import { node, notice, actions, button, table, stack, field } from './ui.js';

// A read owns only its region. Navigation and dialog closing never acquire the
// write lock, and a superseded or closed view cannot accept a late response.
export function readRegion(ctx, label, read, apply, { active = () => true, clear = () => {}, view } = {}) {
  const message = notice(`正在读取${label}…`, '');
  message.removeAttribute('role');
  const retry = button('重新读取', () => void load());
  retry.hidden = true;
  const recovery = actions();
  const element = node('div', {}, message, recovery);
  let controller, generation = 0, ready = false, pending;
  const current = expected => ctx.current() && active() && expected === generation;
  function load() {
    if (pending) return pending;
    pending = run().finally(() => { pending = null; });
    return pending;
  }
  async function run() {
    controller?.abort();
    controller = new AbortController();
    const expected = ++generation;
    const signal = AbortSignal.any([ctx.signal, controller.signal]);
    retry.remove(); retry.hidden = true; element.hidden = ready;
    element.setAttribute('aria-busy', 'true');
    if (!message.isConnected) element.prepend(message);
    message.className = 'a-notice'; message.removeAttribute('role');
    message.textContent = ready ? '正在更新…' : `正在读取${label}…`;
    try {
      const value = await read(signal);
      if (!current(expected) || signal.aborted) return;
      apply(value); ready = true; element.hidden = true; message.textContent = ''; message.removeAttribute('role'); message.remove();
      return value;
    } catch (error) {
      if (!current(expected) || signal.aborted || error.intentionalCancellation === true) return;
      await ctx.recordFailure?.(error, label, "region_loading");
      if (!current(expected)) return;
      if ([401, 403, 404].includes(error.status)) { ready = false; clear(); }
      if (!ready && view) {
        const root = typeof view === 'function' ? view() : view;
        const tables = [root, ...root.querySelectorAll('[data-reading-table]')];
        tables.forEach(table => table.setReadError?.());
      }
      element.hidden = false;
      message.className = 'a-notice error'; message.setAttribute('role', 'alert');
      message.textContent = `${error.message}${ready ? '（保留上次数据）' : ''}`;
      retry.hidden = false; recovery.replaceChildren(retry);
    } finally {
      if (current(expected)) element.removeAttribute('aria-busy');
    }
  }
  return { element, load, get ready() { return ready; }, stop() { generation++; controller?.abort(); } };
}

export const loadingTable = (columns, label, options = {}) => table(columns, [], { ...options, label, loading: true });

export function dialogContext(ctx, box) {
  const controller = new AbortController();
  const signal = AbortSignal.any([ctx.signal, controller.signal]);
  box.addEventListener('close', () => controller.abort(), { once: true });
  return { ...ctx, signal, current: () => ctx.current() && box.open && !signal.aborted,
    api: (path, options = {}) => ctx.api(path, { ...options, signal: options.signal || signal }) };
}

export function loadingForm(labels, submit = '保存') {
  return stack(node('fieldset', { className: 'a-form-fields a-stack', disabled: true },
    labels.map(label => field(label, '', '', { disabled: true, ...(/正文|源码|输出|出站配置/.test(label) ? {multiline: 8} : {}) }))),
  actions(button(submit, () => {}, { disabled: true })));
}

// Keep a single business dialog alive while its required baseline is read.
// The builder receives a dialog-bound context; its first dialog() fills this
// box, so closing during a read cannot open a second, late dialog.
export function preparedDialog(ctx, title, labels, build, { wide = true, submit = '保存并部署' } = {}) {
  const content = stack(loadingForm(labels, submit));
  const body = stack(content);
  const box = ctx.dialog(title, body, { wide });
  box.setContent = element => content.replaceChildren(element);
  const local = dialogContext(ctx, box);
  let filled = false;
  local.dialog = (heading, element, options) => {
    if (!local.current()) return box;
    if (filled) return ctx.dialog(heading, element, options);
    filled = true;
    box.querySelector('.a-dialog-head h2').textContent = heading;
    content.replaceChildren(element);
    return box;
  };
  const read = readRegion(local, title, () => { filled = false; return build(local); }, () => { if (!filled && box.open) box.close(); });
  body.prepend(read.element);
  void read.load();
  return box;
}
