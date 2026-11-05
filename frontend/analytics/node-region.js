import { geoRegionDetails } from '../modules/region-model.js';
import { node, button, actions, field, muted, notice, list, pathID } from './ui.js';

export function regionFlag(region, lazy = false) {
  const flag = node('span', { className: 'a-probe-flag', role: 'img', 'aria-label': region?.name || '国家／地区未知' });
  if (!region) { flag.textContent = '—'; return flag; }
  flag.dataset.regionCode = region.code;
  const image = node('img', { src: `/api/v1/region-flags/${region.flagCode.toLowerCase()}`, alt: '', decoding: 'async', loading: lazy ? 'lazy' : 'eager', draggable: 'false' });
  image.addEventListener('error', () => { flag.replaceChildren(region.code); }, { once: true });
  flag.append(image);
  return flag;
}

export function nodeRegion(ctx, agent, onSaved) {
  const search = field('搜索国家／地区', 'region_search', '', { type: 'search', placeholder: '中文、英文名称或代码', autocomplete: 'off' });
  const choices = node('div', { className: 'a-region-options', role: 'group', 'aria-label': '国家／地区' });
  const feedback = notice(''), selectedText = muted('');
  const automatic = button('自动识别', () => select(''), { 'aria-pressed': 'false' });
  const retry = button('重试加载', () => void load()); retry.hidden = true;
  const initial = geoRegionDetails(agent.labels?.region_code)?.code || '';
  let selected = initial, regions = [], loading = true, changed = false;
  const controller = new AbortController();
  const signal = AbortSignal.any([ctx.signal, controller.signal]);
  const form = ctx.form([actions(automatic, selectedText), search, choices, feedback, actions(retry)], '保存国家／地区', async () => {
    if (loading || (selected && !regions.some(region => region.code === selected))) throw new Error('请选择可用的国家／地区。');
    const saved = await ctx.api(`/agents/${pathID(agent.id)}/region`, { method: 'PUT', body: { country_code: selected }, signal });
    if (!ctx.current() || !box.open) return;
    await onSaved(saved.country_code || '');
    if (!ctx.current() || !box.open) return;
    changed = false; ctx.setDirty(false); box.close(); ctx.announce(saved.country_code ? '国家／地区已保存' : '已恢复自动识别');
  });
  const submit = form.querySelector('button[type=submit]');
  const box = ctx.dialog(`${agent.name} · 国家／地区`, form);
  function select(code) {
    selected = code; changed = selected !== initial; ctx.setDirty(changed);
    automatic.setAttribute('aria-pressed', String(!selected));
    selectedText.textContent = selected ? `当前选择 · ${geoRegionDetails(selected).name}` : '跟随节点公网 IP';
    choices.querySelectorAll('[data-region-choice]').forEach(control => control.setAttribute('aria-pressed', String(control.dataset.regionChoice === selected)));
  }
  function draw() {
    const query = search.querySelector('input').value.trim().toLowerCase();
    const matches = regions.filter(region => `${region.name} ${region.english} ${region.code}`.toLowerCase().includes(query));
    choices.replaceChildren(...matches.map(region => {
      const control = button('', () => select(region.code), { className: 'a-region-option', 'data-region-choice': region.code, 'aria-pressed': String(selected === region.code), 'aria-label': `${region.name} (${region.code})` });
      control.append(regionFlag(region, true), node('span', {}, region.name), muted(region.code));
      return control;
    }));
    if (!matches.length) choices.append(muted(query ? '没有匹配的国家／地区' : '暂无可用国家／地区'));
  }
  async function load() {
    loading = true; submit.disabled = true; retry.hidden = true; feedback.textContent = '';
    choices.replaceChildren(muted('正在加载国家／地区…'));
    try {
      const codes = list(await ctx.api('/regions', { signal }));
      if (!ctx.current() || !box.open) return;
      const common = ['CN', 'HK', 'MO', 'TW', 'SG', 'JP', 'US', 'GB', 'DE', 'FR', 'KR', 'CA', 'AU', 'NL', 'IN'];
      const rank = code => common.indexOf(code) < 0 ? common.length : common.indexOf(code);
      const english = globalThis.Intl?.DisplayNames ? new Intl.DisplayNames(['en'], { type: 'region' }) : null;
      regions = codes.map(geoRegionDetails).filter(Boolean).map(region => ({ ...region, english: english?.of(region.code) || region.code }))
        .sort((left, right) => rank(left.code) - rank(right.code) || left.name.localeCompare(right.name, 'zh-CN'));
      loading = false; draw(); select(selected); submit.disabled = !regions.length;
    } catch (error) {
      if (!ctx.current() || !box.open || error.name === 'AbortError') return;
      choices.replaceChildren(); feedback.textContent = error.message; retry.hidden = false;
    }
  }
  search.addEventListener('input', () => { if (!loading) draw(); });
  search.addEventListener('keydown', event => { if (event.key === 'Enter') event.preventDefault(); });
  submit.addEventListener('mutationend', () => { submit.disabled = loading; });
  box.onRequestClose = async () => { if (!changed || await ctx.confirm('存在未保存的更改，确定关闭？', '放弃更改')) box.close(); };
  const close = () => { controller.abort(); box.close(); };
  ctx.signal.addEventListener('abort', close, { once: true });
  box.addEventListener('close', () => { ctx.setDirty(false); controller.abort(); ctx.signal.removeEventListener('abort', close); }, { once: true });
  select(selected); void load();
  return box;
}
