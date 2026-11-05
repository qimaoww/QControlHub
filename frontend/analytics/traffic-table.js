import { node, button, actions, muted } from './ui.js';

// Live samples update cells without rebuilding focused controls or pagination.
export function trafficTable(columns) {
  const body = node('tbody');
  const scroll = node('div', { className: 'a-table-scroll a-traffic-list', tabindex: '0', role: 'region', 'aria-label': '端口流量' },
    node('table', {}, node('thead', {}, node('tr', {}, columns.map(name => node('th', { scope: 'col' }, name)))), body));
  const empty = node('p', { className: 'a-empty' });
  const count = muted('');
  let items = [], page = 1;
  const rows = new Map();
  const previous = button('上一页', () => { page--; paint(); });
  const next = button('下一页', () => { page++; paint(); });
  const pages = node('div', { className: 'a-pagination' }, count, actions(previous, next));
  const root = node('div', {}, scroll, empty, pages);
  function paint() {
    const last = Math.max(1, Math.ceil(items.length / 12));
    page = Math.min(last, Math.max(1, page));
    const visible = items.slice((page - 1) * 12, page * 12);
    const keys = new Set(visible.map(item => item.key));
    for (const [key, row] of rows) if (!keys.has(key)) { row.remove(); rows.delete(key); }
    visible.forEach((item, index) => {
      let row = rows.get(item.key);
      if (!row) { row = node('tr', { 'data-traffic-key': item.key }); rows.set(item.key, row); }
      item.cells.forEach((content, column) => {
        const next = node('td', { 'data-column': columns[column] }, content);
        const old = row.children[column];
        if (!old) row.append(next);
        else if (!old.isEqualNode(next)) old.replaceWith(next);
      });
      if (body.children[index] !== row) body.insertBefore(row, body.children[index] || null);
    });
    scroll.hidden = !items.length; empty.hidden = !!items.length; pages.hidden = last <= 1;
    count.textContent = `第 ${page} / ${last} 页`;
    previous.disabled = page <= 1; next.disabled = page >= last;
  }
  return { root, update(values, reset, emptyText) { items = values; if (reset) page = 1; empty.textContent = emptyText; paint(); } };
}
