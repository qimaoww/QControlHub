// Preserve the panel's existing display policy for Taiwan in every view.
export function geoRegionDetails(value) {
  const code = String(value || "").trim().toUpperCase();
  if (!/^[A-Z]{2}$/.test(code)) return null;
  const flagCode = code === "TW" ? "CN" : code;
  let name = code === "TW" ? "中国台湾" : code;
  if (code !== "TW") {
    try {
      name = new Intl.DisplayNames(["zh-CN"], { type: "region" }).of(code) || code;
    } catch { /* Keep the code in browsers without DisplayNames. */ }
  }
  return { code, flagCode, name };
}

export function regionAvatarMarkup(agent, esc, editable = false, className = "machine-avatar") {
  const tag = editable ? "button" : "span";
  return `<${tag} class="${className}" data-region-avatar="${esc(agent.id)}" ${editable ? 'type="button" data-region-edit aria-haspopup="dialog"' : 'role="img"'} aria-label="${editable ? "选择国家/地区旗帜：节点地区未知" : "节点地区未知"}">●</${tag}>`;
}

export function updateRegionAvatar(avatar, region) {
  if (!avatar) return;
  const editable = avatar.hasAttribute("data-region-edit");
  const name = region ? `${region.name} (${region.code})` : "节点地区未知";
  avatar.title = editable ? `${name} · 点击选择国家/地区旗帜` : name;
  avatar.setAttribute("aria-label", editable ? `选择国家/地区旗帜：${name}` : region?.name || name);
  if (avatar.dataset.regionCode === (region?.code || "") && avatar.firstChild) return;
  avatar.dataset.regionCode = region?.code || "";
  avatar.replaceChildren();
  avatar.classList.toggle("has-region", Boolean(region));
  if (!region) {
    avatar.textContent = "●";
    return;
  }
  const image = document.createElement("img");
  image.src = `/api/v1/region-flags/${region.flagCode.toLowerCase()}`;
  image.alt = "";
  image.decoding = "async";
  image.draggable = false;
  image.addEventListener("error", () => {
    if (image.parentNode !== avatar) return;
    avatar.textContent = "●";
    avatar.classList.remove("has-region");
  }, { once: true });
  avatar.append(image);
}

// Per-avatar request identity prevents a slow automatic lookup from replacing
// a newer manual choice. Reconciled views reuse pending/completed lookups.
export function createRegionDisplay({ api, can }) {
  const displays = new WeakMap();
  return (agent, root) => {
    const avatar = root?.querySelector?.("[data-region-avatar]");
    if (!avatar || !can("agents.read")) return;
    const manual = geoRegionDetails(agent.labels?.region_code);
    const metrics = agent.metrics || {};
    const key = JSON.stringify([agent.id, manual?.code, metrics.public_ipv4, metrics.public_ipv6, metrics.observed_public_ip, metrics.network_interfaces]);
    const previous = displays.get(avatar);
    if (previous?.key === key) {
      if (previous.ready) updateRegionAvatar(avatar, previous.region);
      return;
    }
    const entry = { key, ready: Boolean(manual), region: manual };
    displays.set(avatar, entry);
    updateRegionAvatar(avatar, manual);
    if (manual) return;
    api(`/agents/${encodeURIComponent(agent.id)}/region`)
      .then((payload) => {
        entry.ready = true;
        entry.region = geoRegionDetails(payload?.country_code);
        if (avatar.isConnected && displays.get(avatar) === entry)
          updateRegionAvatar(avatar, entry.region);
      })
      .catch(() => {
        if (displays.get(avatar) === entry) displays.delete(avatar);
      });
  };
}

export function openRegionPicker(agent, { api, esc, onSave, onClose }) {
  const dialog = document.createElement("dialog");
  dialog.className = "region-picker-dialog";
  dialog.setAttribute("aria-labelledby", "region-picker-title");
  const globe = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18M5 6.5h14M5 17.5h14"/></svg>';
  dialog.innerHTML = `<header><div><h2 id="region-picker-title">国家 / 地区旗帜</h2><p><b>${esc(agent.name)}</b><span>同步显示在节点与客户端卡片</span></p></div><button type="button" data-region-close aria-label="关闭旗帜选择">×</button></header><form><div class="region-picker-controls"><label class="region-picker-search"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/></svg><input type="search" name="query" aria-label="搜索国家/地区" placeholder="搜索国家、地区或代码" autocomplete="off"></label><button class="region-auto-choice" type="button" data-region-auto aria-pressed="false"><span class="region-auto-icon">${globe}</span><span><strong>自动识别</strong><small>跟随节点公网 IP</small></span><i aria-hidden="true">✓</i></button></div><div class="region-picker-catalog"><div class="region-picker-list-heading"><span data-region-list-label>全部国家 / 地区</span><small data-region-count></small></div><div class="region-picker-grid" role="group" aria-label="选择国家/地区" data-region-grid></div><p data-region-empty hidden>没有匹配的国家或地区<br><small>试试中文、英文名称或两位代码</small></p></div><p data-region-status role="status">正在加载国家/地区…</p><footer><div class="region-picker-preview"><span class="region-selected-flag" data-region-preview role="img"></span><span><small>当前选择</small><strong data-region-description></strong></span></div><div class="region-picker-actions"><button class="button" type="button" data-region-close>取消</button><button class="button primary" type="submit" disabled>保存旗帜</button></div></footer></form>`;
  const form = dialog.querySelector("form");
  const grid = form.querySelector("[data-region-grid]");
  const auto = form.querySelector("[data-region-auto]");
  const search = form.elements.namedItem("query");
  const submit = form.querySelector('[type="submit"]');
  const status = form.querySelector("[data-region-status]");
  let selected = geoRegionDetails(agent.labels?.region_code)?.code || "";
  let regions = [];
  let saving = false;
  const preview = () => {
    const region = geoRegionDetails(selected);
    updateRegionAvatar(form.querySelector("[data-region-preview]"), region);
    if (!region) {
      const icon = form.querySelector("[data-region-preview]");
      icon.innerHTML = globe;
      icon.setAttribute("aria-label", "自动识别");
      icon.title = "自动识别";
    }
    form.querySelector("[data-region-description]").textContent = region ? `${region.name} · ${region.code}` : "自动识别";
    auto.setAttribute("aria-pressed", String(!selected));
    grid.querySelectorAll("[data-region-choice]").forEach((button) => {
      button.setAttribute("aria-pressed", String(button.dataset.regionChoice === selected));
    });
  };
  const renderOptions = () => {
    const query = search.value.trim().toLowerCase();
    const matches = regions.filter((region) => `${region.code} ${region.name} ${region.english}`.toLowerCase().includes(query));
    grid.innerHTML = matches.map((region) => `<button type="button" class="region-choice" data-region-choice="${esc(region.code)}" aria-label="${esc(region.name)} (${esc(region.code)})" aria-pressed="${region.code === selected}" title="${esc(region.name)} (${esc(region.code)})"><span class="region-choice-flag"><img src="/api/v1/region-flags/${region.flagCode.toLowerCase()}" alt="" loading="lazy" decoding="async" draggable="false"><i aria-hidden="true">✓</i></span><strong>${esc(region.name)}</strong><small>${esc(region.code)}</small></button>`).join("");
    grid.scrollTop = 0;
    form.querySelector("[data-region-list-label]").textContent = query ? "搜索结果" : "全部国家 / 地区";
    form.querySelector("[data-region-count]").textContent = `${matches.length} 个${query ? "" : " · 常用优先"}`;
    form.querySelector("[data-region-empty]").hidden = matches.length > 0;
    grid.hidden = !matches.length;
    grid.querySelectorAll("img").forEach((image) => image.addEventListener("error", () => {
      image.hidden = true;
      image.parentNode.classList.add("unavailable");
    }, { once: true }));
  };
  const close = () => { if (!saving) dialog.close(); };
  const navigate = () => dialog.close();
  dialog.querySelectorAll("[data-region-close]").forEach((button) => { button.onclick = close; });
  dialog.addEventListener("cancel", (event) => { if (saving) event.preventDefault(); });
  dialog.addEventListener("close", () => {
    window.removeEventListener("hashchange", navigate);
    dialog.remove();
    onClose?.();
  }, { once: true });
  window.addEventListener("hashchange", navigate);
  search.oninput = renderOptions;
  search.onkeydown = (event) => {
    if (event.key === "Enter") event.preventDefault();
  };
  grid.onclick = (event) => {
    const button = event.target.closest("[data-region-choice]");
    if (!button || saving) return;
    selected = button.dataset.regionChoice;
    preview();
  };
  auto.onclick = () => {
    selected = "";
    search.value = "";
    renderOptions();
    preview();
  };
  form.onsubmit = async (event) => {
    event.preventDefault();
    if (saving || submit.disabled) return;
    const code = selected;
    saving = true;
    dialog.querySelectorAll("input, button").forEach((control) => { control.disabled = true; });
    status.textContent = "正在保存…";
    try {
      await api(`/agents/${encodeURIComponent(agent.id)}/region`, { method: "PUT", body: JSON.stringify({ country_code: code }) });
      onSave(code);
      dialog.close();
    } catch (error) {
      status.textContent = error.message || "保存失败，请重试。";
    } finally {
      saving = false;
      dialog.querySelectorAll("input, button").forEach((control) => { control.disabled = false; });
    }
  };
  document.body.append(dialog);
  dialog.showModal();
  preview();
  search.focus();
  api("/regions").then((codes) => {
    if (!dialog.isConnected) return;
    const common = ["CN", "HK", "MO", "TW", "SG", "JP", "US", "GB", "DE", "FR", "KR", "CA", "AU", "NL", "IN"];
    const rank = (code) => common.includes(code) ? common.indexOf(code) : common.length;
    regions = codes.map(geoRegionDetails).filter(Boolean).map((region) => {
      let english = region.code;
      try { english = new Intl.DisplayNames(["en"], { type: "region" }).of(region.code); } catch { /* Code search still works. */ }
      return { ...region, english };
    }).sort((a, b) => rank(a.code) - rank(b.code) || a.name.localeCompare(b.name, "zh-CN"));
    renderOptions();
    status.textContent = "";
    submit.disabled = false;
  }).catch((error) => { if (dialog.isConnected) status.textContent = `国家/地区加载失败：${error.message}，请关闭后重试。`; });
}
