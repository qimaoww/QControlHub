import { readRegion } from './loading.js';
import { field } from './ui.js';

export const pageSizes = [30, 50, 100, 200];
export function pageSizePreference(ctx, page, onChange) {
  let preference, saving = false;
  const element = field('每页条数', 'page_size', 30, { options: pageSizes.map(size => [String(size), `${size} 条/页`]), 'aria-label': '每页条数', disabled: true });
  element.querySelector('label').hidden = true;
  const select = element.querySelector('select');
  const endpoint = `/preferences/page-size/${page}`;
  function accept(value) {
    if (!ctx.current()) return;
    preference = value;
    const size = pageSizes.includes(value.page_size) ? value.page_size : 30;
    select.value = String(size); onChange(size);
  }
  const read = readRegion(ctx, '每页条数', signal => ctx.api(endpoint, { signal }), value => { accept(value); select.disabled = false; });
  select.addEventListener('change', () => {
    if (!preference || saving) return;
    const size = Number(select.value);
    void ctx.mutate(select, async () => {
      saving = true; select.disabled = true;
      try {
        accept(await ctx.api(endpoint, { method: 'PUT', body: { page_size: size, revision: preference.revision } }));
      } catch (error) {
        if (error.status === 409) {
          // Read the authoritative value once; never replay a stale write.
          await read.load();
        }
        if (ctx.current()) select.value = String(preference.page_size);
        throw error;
      } finally { saving = false; if (ctx.current()) select.disabled = false; }
    });
  });
  void read.load();
  return { element, status: read.element };
}

export function recordPages(onChange) {
  let page = 1, size = 30, total = 0;
  return {
    get page() { return page; }, get size() { return size; },
    get last() { return Math.max(1, Math.ceil(total / size)); },
    get start() { return total ? (page - 1) * size + 1 : 0; },
    get end() { return Math.min(page * size, total); },
    setSize(value) { page = Math.floor((page - 1) * size / value) + 1; size = value; this.clamp(); onChange(); },
    setPage(value) { page = value; this.clamp(); onChange(); },
    clamp() { page = Math.max(1, Math.min(this.last, page)); },
    slice(values) { total = values.length; this.clamp(); return values.slice((page - 1) * size, page * size); },
    summary() { return `共 ${total} 条 · ${this.start}–${this.end} 条 · 第 ${page} / ${this.last} 页`; },
  };
}
