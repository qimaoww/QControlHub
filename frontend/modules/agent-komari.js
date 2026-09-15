function monthBoundary(year, month, resetDay) {
  const lastDay = new Date(year, month + 1, 0).getDate();
  return new Date(year, month, Math.min(resetDay, lastDay));
}

export function komariResetDay(server = {}) {
  const explicit = Number(server.traffic_reset_day);
  if (Number.isInteger(explicit) && explicit >= 1 && explicit <= 31)
    return explicit;
  const billingCycle = Number(server.billing_cycle);
  if (billingCycle < 27 || billingCycle > 32) return 0;
  const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(
    String(server.expired_at || "").trim(),
  );
  const expirationDay = Number(match?.[3]);
  return Number.isInteger(expirationDay) && expirationDay >= 1 && expirationDay <= 31
    ? expirationDay
    : 0;
}

export function komariCycleRange(resetDay, now = new Date()) {
  resetDay = Number(resetDay);
  if (
    !Number.isInteger(resetDay) ||
    resetDay < 1 ||
    resetDay > 31 ||
    !(now instanceof Date) ||
    Number.isNaN(now.getTime())
  )
    return "";
  const year = now.getFullYear();
  const month = now.getMonth();
  const currentBoundary = monthBoundary(year, month, resetDay);
  const startsThisMonth = now.getDate() >= currentBoundary.getDate();
  const start = startsThisMonth
    ? currentBoundary
    : monthBoundary(year, month - 1, resetDay);
  const end = startsThisMonth
    ? monthBoundary(year, month + 1, resetDay)
    : currentBoundary;
  const label = (value) => `${value.getMonth() + 1}.${value.getDate()}`;
  return `${label(start)}–${label(end)}`;
}

export function createAgentKomariDisplay({ api, state, esc, bytes }) {
  const komariUUIDFor = (agent) => String(agent?.labels?.komari_uuid || "").trim();
  const komariNetworkMarkup = (agent) => {
    const uuid = komariUUIDFor(agent);
    if (!uuid) return "";
    return `<div class="node-card-komari-cycle" data-komari-link="${esc(agent.id)}"><small><b data-komari-traffic>读取中…</b><i data-komari-cycle>周期读取中…</i></small><progress data-komari-progress aria-label="Komari 月流量使用率" max="100" value="0"></progress></div>`;
  };
  const updateKomariDisplay = (root, link, error = "") => {
    const card = root?.querySelector?.("[data-komari-link]");
    if (!card) return;
    const traffic = card.querySelector("[data-komari-traffic]");
    const cycle = card.querySelector("[data-komari-cycle]");
    const progress = card.querySelector("[data-komari-progress]");
    if (error || !link?.server) {
      if (traffic) traffic.textContent = error || "Komari 读取失败";
      if (cycle) cycle.textContent = "检查联动设置";
      if (progress) {
        progress.value = 0;
        progress.hidden = false;
      }
      card.classList.add("unavailable");
      return;
    }
    const server = link.server;
    const hasEffectiveLimit = server.effective_traffic_limit_available === true;
    const limit = Number(hasEffectiveLimit ? server.effective_traffic_limit : server.traffic_limit || 0);
    const usedAvailable = server.traffic_used_available === true;
    const used = Math.max(0, Number(server.traffic_used || 0));
    const resetDay = komariResetDay(server);
    if (traffic)
      traffic.textContent = limit > 0
        ? `${usedAvailable ? bytes(used) : "—"} / ${bytes(limit)}`
        : "不限流量";
    if (cycle)
      cycle.textContent = komariCycleRange(resetDay) || "周期日期未设置";
    if (progress) {
      progress.hidden = !(limit > 0);
      progress.value = limit > 0 && usedAvailable
        ? Math.min(100, (used / limit) * 100)
        : 0;
      progress.setAttribute(
        "aria-label",
        limit > 0 && usedAvailable
          ? `Komari 月流量使用率 ${progress.value.toFixed(1)}%`
          : "Komari 月流量使用率暂不可用",
      );
    }
    card.classList.toggle("usage-unavailable", limit > 0 && !usedAvailable);
    card.classList.remove("unavailable");
  };
  // Komari resolves through an external provider, so each node's monthly
  // traffic is read at most once per account session: the node list inlines the
  // resource the control plane already cached, and a rebuilt card reuses the
  // account-scoped result instead of repeating a round trip that costs about a
  // second.
  const loadKomariDisplay = (agent, root) => {
    const uuid = komariUUIDFor(agent);
    if (!uuid || !root) return;
    const cache = (state.data.agentKomari ||= {});
    const known = cache[agent.id];
    if (known?.uuid === uuid) {
      if (known.ready) updateKomariDisplay(root, known.link, known.error);
      return;
    }
    if (agent.komari?.uuid === uuid) {
      const inline = { uuid, server: agent.komari };
      cache[agent.id] = { uuid, ready: true, link: inline, error: "" };
      updateKomariDisplay(root, inline);
      return;
    }
    const entry = { uuid, ready: false, link: null, error: "" };
    cache[agent.id] = entry;
    api(`/agents/${encodeURIComponent(agent.id)}/komari`)
      .then((link) => {
        entry.ready = true;
        entry.link = link;
        updateKomariDisplay(root, link);
      })
      .catch((error) => {
        if (cache[agent.id] === entry) delete cache[agent.id];
        updateKomariDisplay(root, null, error?.message || "Komari 读取失败");
      });
  };
  return { komariUUIDFor, komariNetworkMarkup, loadKomariDisplay };
}
