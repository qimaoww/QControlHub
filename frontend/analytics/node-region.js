import { bindPreferenceFields } from "./preferences.js";
import { geoRegionDetails } from '../modules/region-model.js';
import { node, button, actions, field, muted, notice, list, pathID } from './ui.js';

export function regionFlag(region, lazy = false) {
  const flag = node('span', { className: 'a-probe-flag', role: 'img', 'aria-label': region?.name || '国家／地区未知', title: region ? `${region.name} (${region.code})` : '国家／地区未知' });
  if (!region) { flag.textContent = '—'; return flag; }
  flag.dataset.regionCode = region.code;
  const image = node('img', { src: `/api/v1/region-flags/${region.flagCode.toLowerCase()}`, alt: '', decoding: 'async', loading: lazy ? 'lazy' : 'eager', draggable: 'false' });
  image.addEventListener('error', () => { image.parentNode?.replaceChildren(region.code); }, { once: true });
  flag.append(image);
  return flag;
}

export function nodeRegionFlag(ctx, agent) {
  const inline = geoRegionDetails(agent?.labels?.region_code) || geoRegionDetails(agent?.region_code);
  if (!agent || inline || Object.hasOwn(agent, 'region_code') || !ctx.can('agents.read')) return regionFlag(inline);
  const cache = ctx.state.data.agentRegions ||= new Map();
  const key = JSON.stringify([agent.id, agent.metrics?.public_ipv4, agent.metrics?.public_ipv6, agent.metrics?.observed_public_ip, agent.metrics?.network_interfaces]);
  let entry = cache.get(key);
  if (!entry) {
    entry = {};
    entry.request = ctx.api(`/agents/${pathID(agent.id)}/region`).then(value => {
      entry.region = geoRegionDetails(value?.country_code); entry.ready = true;
      return entry.region;
    }).catch(() => { if (cache.get(key) === entry) cache.delete(key); return null; });
    cache.set(key, entry);
  }
  const flag = regionFlag(entry.region);
  if (!entry.ready) void entry.request.then(region => {
    if (ctx.current() && flag.isConnected && !geoRegionDetails(agent.labels?.region_code) && !Object.hasOwn(agent, 'region_code')) {
      const fresh = regionFlag(region);
      flag.replaceChildren(...fresh.childNodes);
      flag.setAttribute('aria-label', fresh.getAttribute('aria-label'));
      flag.title = fresh.title;
      if (region) flag.dataset.regionCode = region.code;
    }
  });
  return flag;
}

export function nodeName(ctx, agent) {
  return node('span', { className: 'a-probe-name' }, nodeRegionFlag(ctx, agent), node('strong', {}, agent.name));
}

export function nodeRegion(ctx, agent, onSaved) {
  const search = field('搜索国家／地区', 'region_search', '', { type: 'search', placeholder: '中文、英文名称或代码', autocomplete: 'off' });
  const choices = node('div', { className: 'a-region-options', role: 'group', 'aria-label': '国家／地区' });
  const feedback = notice(''), selectedText = node('div', { className: 'a-actions a-muted' });
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
    for (const key of ctx.state.data.agentRegions?.keys() || []) if (JSON.parse(key)[0] === agent.id) ctx.state.data.agentRegions.delete(key);
    await onSaved(saved.country_code || '');
    if (!ctx.current() || !box.open) return;
    changed = false; ctx.setDirty(false); box.close(); ctx.announce(saved.country_code ? '国家／地区已保存' : '已恢复自动识别');
  });
  const submit = form.querySelector('button[type=submit]');
  const box = ctx.dialog(`${agent.name} · 国家／地区`, form);
  bindPreferenceFields(ctx, 'node-region', [search], { restore: () => { if (box.open && !loading) draw(); } });
  function select(code) {
    selected = code; changed = selected !== initial; ctx.setDirty(changed);
    automatic.setAttribute('aria-pressed', String(!selected));
    selectedText.replaceChildren(...(selected ? [regionFlag(geoRegionDetails(selected)), `当前选择 · ${geoRegionDetails(selected).name}`] : ['跟随节点公网 IP']));
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
