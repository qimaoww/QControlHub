import { actions, badge, button, dragHandle, field, muted, node, stack } from './ui.js';
import { bindNodeOrdering } from './node-ordering.js';

export function outboundFallbackPicker(ctx, entries, initial, primary, multiple) {
  let tags = [...initial];
  const byTag = new Map(entries.map(entry => [entry.tag, entry]));
  const rows = node('div', { className: 'a-fallback-list', role: 'list', 'aria-label': '备用出口顺序' });
  const chooser = field('选择备用出口', '', '', { options: [['', '请选择出口']] });
  const select = chooser.querySelector('select');
  const add = button('＋ 添加备用出口', () => {
    add.hidden = true;
    chooser.hidden = false;
    select.focus();
    try { select.showPicker?.(); } catch { /* The focused select also opens with the keyboard. */ }
  });
  const element = stack(node('strong', {className:'a-field-label'}, '备用出口'),
    muted(multiple ? '主出口不可用时，按从上到下的顺序选择首个可用出口。' : 'Xray 支持一个备用出口，需要先配置 observatory 或 burstObservatory。'),
    rows, actions(add), chooser);

  function change(next) {
    tags = next;
    draw();
    element.dispatchEvent(new Event('input', { bubbles: true }));
  }
  function draw() {
    rows.hidden = !tags.length;
    rows.replaceChildren(...tags.map((tag, i) => {
      const entry = byTag.get(tag);
      return node('div', {className:'a-fallback-row', role:'listitem', 'data-order-row':true},
        multiple && dragHandle(tag, tag, tags.length === 1), multiple && badge(String(i + 1)),
        node('div', {className:'a-fallback-name'}, node('strong', {}, tag), muted(entry?.kind || entry?.section || '不可用')),
        button('移除', () => change(tags.filter(value => value !== tag)), {'aria-label': '移除 ' + tag}));
    }));
    const available = entries.filter(entry => entry.tag !== primary && !tags.includes(entry.tag));
    select.replaceChildren(select.querySelector('button'), node('option', {value:''}, '请选择出口'),
      ...available.map(entry => node('option', {value:entry.tag}, entry.tag + ' · ' + entry.kind)));
    select.value = '';
    chooser.hidden = true;
    add.hidden = !multiple && tags.length > 0;
    add.disabled = available.length === 0;
  }
  select.addEventListener('change', () => {
    if (select.value) change([...tags, select.value]);
    else draw();
    if (!add.hidden && !add.disabled) add.focus();
    else rows.lastElementChild?.querySelector('button:last-child')?.focus();
  });
  if (multiple) bindNodeOrdering(ctx, rows, {
    enabled: () => !rows.closest('fieldset')?.disabled,
    order: () => [...tags], save: change, redraw: draw, saved: '备用出口顺序已调整',
  });
  draw();
  return { element, values: () => [...tags], setPrimary(tag) {
    primary = tag;
    tags = tags.filter(value => value !== tag);
    draw();
  }};
}
