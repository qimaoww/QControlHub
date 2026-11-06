import { node, button, actions, muted } from './ui.js';
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
  function setView(next) {
    if (next === view || !canSwitch()) return;
    view = next; cardButton.setAttribute('aria-pressed', String(view === 'cards')); listButton.setAttribute('aria-pressed', String(view === 'list'));
    onViewChange(view); rows.clear(); body.replaceChildren(); cards.replaceChildren(); paint();
  }
  const empty = node('p', { className: 'a-empty' });
  const count = muted('');
  let items = [], page = 1;
  const rows = new Map();
  const previous = button('上一页', () => { page--; paint(); });
  const next = button('下一页', () => { page++; paint(); });
  const pages = node('div', { className: 'a-pagination' }, count, actions(previous, next));
  const root = node('div', { className: 'a-stack' }, switcher, listSection, cards, empty, pages);
  function paint() {
    const last = Math.max(1, Math.ceil(items.length / 12));
    page = Math.min(last, Math.max(1, page));
    const visible = items.slice((page - 1) * 12, page * 12);
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
      item.cells.forEach((content, column) => {
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
    scroll.hidden = !items.length; empty.hidden = !!items.length; pages.hidden = last <= 1;
    count.textContent = `第 ${page} / ${last} 页`;
    previous.disabled = page <= 1; next.disabled = page >= last;
  }
  return { root, tools, update(values, reset, emptyText) { items = values; if (reset) page = 1; empty.textContent = emptyText; paint(); } };
}
