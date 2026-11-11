import { node, button, actions, muted } from './ui.js';
import { recordPages } from './page-size.js';
import { patch } from './live-dom.js';

// Live samples update cells without rebuilding focused controls or pagination.
export function trafficTable(columns, { view = 'cards', onViewChange = () => {}, canSwitch = () => true } = {}) {
  const body = node('tbody');
  const scroll = node('div', { className: 'a-table-scroll a-traffic-list', tabindex: '0', role: 'region', 'aria-label': '端口流量' },
    node('table', {}, node('thead', {}, node('tr', {}, columns.map(name => node('th', { scope: 'col' }, name)))), body));
  const cards = node('div', { className: 'a-probe-grid' });
  const listSection = node('section', { className: 'a-card' }, scroll);
  const records = muted('');
  const cardButton = button('卡片', () => setView('cards'), { 'aria-pressed': String(view === 'cards') });
  const listButton = button('列表', () => setView('list'), { 'aria-pressed': String(view === 'list') });
  const tools = actions(cardButton, listButton);
  const switcher = node('div', { className: 'a-probe-toolbar' }, records, tools);
  let viewTouched = false;
  function setView(next, restoring = false) {
    if (!canSwitch()) return;
    if (!restoring) viewTouched = true;
    if (next === view) { if (!restoring) onViewChange(view, false); return; }
    view = next; cardButton.setAttribute('aria-pressed', String(view === 'cards')); listButton.setAttribute('aria-pressed', String(view === 'list'));
    onViewChange(view, restoring); rows.clear(); body.replaceChildren(); cards.replaceChildren(); paint();
  }
  const empty = node('p', { className: 'a-empty' });
  const count = muted('');
  let items = [];
  const paging = recordPages(() => paint());
  const rows = new Map();
  const previous = button('上一页', () => { paging.setPage(paging.page - 1); });
  const next = button('下一页', () => { paging.setPage(paging.page + 1); });
  const pages = node('div', { className: 'a-pagination' }, count, actions(previous, next));
  const root = node('div', { className: 'a-stack' }, switcher, listSection, cards, empty, pages);
  function paint() {
    const paged = paging.slice(items);
    const visible = view === 'cards' ? items : paged;
    const keys = new Set(visible.map(item => item.key));
    for (const [key, row] of rows) if (!keys.has(key)) { row.remove(); rows.delete(key); }
    const container = view === 'cards' ? cards : body;
    visible.forEach((item, index) => {
      if (view === 'cards') {
        const fresh = item.card();
        let card = rows.get(item.key);
        if (card) patch(card, fresh); else { card = fresh; rows.set(item.key, card); }
        if (container.children[index] !== card) {
          if (container.moveBefore && card.parentNode === container) container.moveBefore(card, container.children[index] || null);
          else container.insertBefore(card, container.children[index] || null);
        }
        return;
      }
      let row = rows.get(item.key);
      if (!row) { row = node('tr', { 'data-traffic-key': item.key }); rows.set(item.key, row); }
      (typeof item.cells === 'function' ? item.cells() : item.cells).forEach((content, column) => {
        const next = node('td', { 'data-column': columns[column] }, content);
        const old = row.children[column];
        if (!old) row.append(next);
        else patch(old, next);
      });
      if (body.children[index] !== row) {
        if (body.moveBefore && row.parentNode === body) body.moveBefore(row, body.children[index] || null);
        else body.insertBefore(row, body.children[index] || null);
      }
    });
    listSection.hidden = view !== 'list' || !items.length; cards.hidden = view !== 'cards' || !items.length;
    records.textContent = `${items.length} 个端口 · 当前周期`;
    scroll.hidden = !items.length; empty.hidden = !!items.length; pages.hidden = view === 'cards' || paging.last <= 1;
    count.textContent = paging.summary();
    previous.disabled = paging.page <= 1; next.disabled = paging.page >= paging.last;
  }
  return { root, tools, restoreView(value) { if (!viewTouched) setView(value, true); }, setPageSize: value => paging.setSize(value), update(values, reset, emptyText) { items = values; if (reset) paging.setPage(1); empty.textContent = emptyText; paint(); } };
}
