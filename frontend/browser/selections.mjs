import { bindLiveConfigNavigation } from "../modules/live-config-navigation.js";
import { assert, delay, waitFor } from "./assertions.mjs";

const ready = () => !document.querySelector(".is-route-pending");
const route = async (hash, selector) => {
  location.hash = hash;
  await waitFor(() => ready() && document.querySelector(selector), `missing route ${hash}`);
  await waitFor(() => !document.querySelector('[data-motion-ready="false"]') && !document.getAnimations().some(animation => animation.id === "qch-route"), `route did not settle ${hash}`);
};
const emit = (element, type) => element.dispatchEvent(new Event(type, { bubbles: true, cancelable: true }));
const selected = () => document.getAnimations().filter(animation => animation.id === "qch-selection");

export async function testSelectionRuntime({ mode, testAPI }, preview) {
  await waitFor(() => document.querySelector(".node-card-grid"), "nodes missing");
  const reduced = mode.endsWith("reduced"), records = [];
  const nativeAnimate = Element.prototype.animate;
  Element.prototype.animate = function (...args) {
    const animation = nativeAnimate.apply(this, args);
    queueMicrotask(() => { if (animation.id === "qch-selection") records.push(this.dataset.motionRegion); });
    return animation;
  };
  const fallback = window.fetch.bind(window);
  let slowMonth, releaseMonth, failedMonth, quotaReads = 0;
  const sources = testAPI.agents.map(agent => ({ agent_id: agent.id, agent_name: agent.name, status: "ok", updated_at: new Date().toISOString() }));
  const users = ["one", "two"].map(id => ({ id, username: id, role: "user" }));
  const targets = ["one", "two"].map(id => ({ id, display_name: id, subscription_name: id, sync_format: "url", sync_mode: "incremental" }));
  const json = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
  window.fetch = async (input, options = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, location.href);
    const path = url.pathname.replace(/^\/api\/v1/, "");
    if (path === "/agent-access") { quotaReads++; return json({ isolated: true, shares: [] }); }
    if (path === "/traffic-usage") {
      const month = url.searchParams.get("month");
      if (month === failedMonth) return json({ error: "月份读取测试失败" }, 503);
      if (month === slowMonth) await new Promise(resolve => { releaseMonth = resolve; });
      // Deliberately return after abort; production still rejects this response.
      return json({ month, days: [] });
    }
    if (path === "/client-access") return json(testAPI.agents.flatMap(agent => ["mihomo", "xray"].map(engine => ({
      agent_id: agent.id, agent_name: agent.name, engine, address: `${agent.id}.example.test`,
      profiles: [{ tag: `${agent.id}-${engine}`, port: 443, protocol: "test", profile: { format: "URI", uri: `test://${agent.id}`, fields: [] } }],
    }))));
    if (path === "/client-connections") return json({ records: [], sources, ips: 0, flows: 0, next_cursor: url.searchParams.has("cursor") ? "" : "page-two" });
    if (path === "/ip-quality") return json({ records: [], schedules: [], dates: [] });
    if (path === "/substore-sync") return json({ settings: { configured: false }, targets,
      target_id: url.searchParams.get("target_id") || "one",
      profiles: testAPI.agents.map(agent => ({ agent_id: agent.id, agent_name: agent.name, agent_status: agent.status,
        profile_tag: `${agent.id}-entry`, engine: "mihomo", protocol: "test", port: 443, available: true, selected: false })),
    });
    if (path === "/users") return json(users);
    if (/^\/users\/[^/]+\/agent-access$/.test(path)) return json({ revision: 1, shares: [], owned_agent_ids: [] });
    return fallback(input, options);
  };
  if (preview) await new Promise(() => {});
  const checkFeedback = async (region, start) => {
    await delay(20);
    assert.equal(records.slice(start).filter(name => name === region).length, reduced ? 0 : 1, `${region} receives one completed selection fade`);
    assert.ok(selected().every(animation => !animation.effect.target.querySelector('[data-motion-region]')), "only one region owns feedback");
    await delay(250);
    assert.equal(selected().length, 0, "selection feedback settles");
  };

  await route("#traffic", "[data-traffic-filter=status]");
  const trafficMain = document.querySelector(".workspace-main"), sidebar = document.querySelector(".context-sidebar");
  const status = document.querySelector("[data-traffic-filter=status]"), trafficGrid = document.querySelector('[data-motion-region="traffic-results"]');
  status.focus();
  let start = records.length;
  status.value = "error"; emit(status, "change");
  assert.equal(document.activeElement, status, "status filter retains focus");
  assert.equal(document.querySelector(".workspace-main"), trafficMain, "filter keeps workspace mounted");
  assert.equal(document.querySelector('[data-motion-region="traffic-results"]'), trafficGrid, "empty/nonempty result keeps its region");
  await checkFeedback("traffic-results", start);
  start = records.length; emit(status, "change"); await delay(250);
  assert.equal(records.length, start, "same status does not replay");
  status.value = "blocked"; emit(status, "change");
  status.value = ""; emit(status, "change");
  await delay(250); assert.equal(selected().length, 0);
  const trafficNode = document.querySelector('[data-context-traffic-agent="alpha"]');
  trafficNode.focus(); trafficNode.click();
  await waitFor(() => ready() && trafficNode.classList.contains("active"), "node filter missing");
  assert.equal(document.querySelector(".context-sidebar"), sidebar, "node switch retains sidebar DOM");
  assert.equal(document.activeElement, trafficNode, "node switch retains sidebar focus");

  await route("#client-access", "[data-filter-engine]");
  const engine = document.querySelector('[data-filter-engine="xray"]');
  engine.focus(); start = records.length; engine.click();
  assert.equal(document.activeElement, engine); await checkFeedback("client-results", start);
  const search = document.querySelector('#client-search input');
  search.value = "no matching profile"; search.focus(); search.setSelectionRange(3, 5);
  emit(search.form, "submit");
  assert.equal(document.activeElement, search); assert.equal(search.selectionStart, 3); assert.equal(search.selectionEnd, 5);
  assert.ok(document.querySelector('.client-access-node-grid.empty'), "filter reports empty results");

  await route("#core-logs", "[data-core-log-level]");
  const level = document.querySelector('[data-core-log-level][value="error"]');
  start = records.length; level.focus(); level.click(); await checkFeedback("core-log-results", start);
  level.click(); await delay(250); assert.equal(selected().length, 0);
  const keyword = document.querySelector('#core-log-filters input[name=q]');
  keyword.focus(); keyword.value = "pressure"; keyword.setSelectionRange(2, 4); start = records.length;
  emit(keyword, "input"); await delay(25);
  assert.equal(records.length, start, "live search does not pulse results");
  assert.equal(keyword.selectionStart, 2); assert.equal(keyword.selectionEnd, 4);
  const logNode = document.querySelector('[data-core-log-agent="bravo"]');
  logNode.focus(); start = records.length; logNode.click();
  await waitFor(() => logNode.classList.contains("active") && document.querySelector('[data-motion-region="core-log-results"]').dataset.motionReady === "true", "log node missing");
  await checkFeedback("core-log-results", start);
  assert.equal(document.activeElement, logNode);
  start = records.length; window.dispatchEvent(new HashChangeEvent("hashchange")); await delay(450);
  assert.equal(records.length, start, "same log scope refresh stays quiet");

  await route("#tasks", "[data-task-page]");
  const taskStatus = document.querySelector('[data-task-status-filter="failed"]');
  start = records.length; taskStatus.focus(); taskStatus.click();
  await waitFor(() => taskStatus.classList.contains("active"), "task filter missing");
  await checkFeedback("task-results", start); assert.equal(document.activeElement, taskStatus);
  assert.equal(document.querySelector("#task-status").value, "failed");

  await route("#client-connections", "[data-connection-next]");
  const connection = document.querySelector('[data-connection-agent="bravo"]');
  start = records.length; connection.focus(); connection.click();
  await waitFor(() => connection.classList.contains("active") && !document.querySelector('[data-connection-refresh]').disabled, "connection filter missing");
  await checkFeedback("connection-results", start); assert.equal(document.activeElement, connection);
  start = records.length; document.querySelector('[data-connection-next]').click();
  await waitFor(() => document.querySelector('.connection-pagination').textContent.includes("第 2 页"), "connection page missing");
  await checkFeedback("connection-results", start);

  await route("#ip-quality", "[data-ip-quality-agent]");
  const quality = document.querySelector('[data-ip-quality-agent="bravo"]');
  start = records.length; quality.focus(); quality.click(); await checkFeedback("ip-quality-results", start);
  assert.equal(document.activeElement, quality);

  await route("#substore-sync", "[data-substore-query]");
  const query = document.querySelector('[data-substore-query]');
  query.value = "alpha"; query.focus(); query.setSelectionRange(1, 3); start = records.length;
  emit(query, "input"); await delay(25);
  assert.equal(document.activeElement, query); assert.equal(query.selectionStart, 1); assert.equal(query.selectionEnd, 3);
  assert.equal(records.length, start, "Sub-Store live search stays readable");
  query.value = ""; emit(query, "input");
  const syncNode = document.querySelector('[data-substore-agent="bravo"]');
  start = records.length; syncNode.focus(); syncNode.click(); await checkFeedback("substore-results", start); assert.equal(document.activeElement, syncNode);
  const target = document.querySelector('[data-substore-target="two"]');
  start = records.length; target.focus(); target.click();
  await waitFor(() => target.classList.contains("active"), "sync group switch missing");
  await checkFeedback("substore-results", start); assert.equal(document.activeElement, target);

  await route("#users", "[data-user-mobile-select]");
  const mobileUser = document.querySelector('[data-user-mobile-select]');
  const userControl = mode.includes("mobile") ? mobileUser : document.querySelector('[data-user-select="two"]');
  start = records.length; userControl.focus();
  if (userControl === mobileUser) { mobileUser.value = "two"; emit(mobileUser, "change"); }
  else userControl.click();
  await waitFor(() => document.querySelector('.users-title h2')?.textContent === "two", "user switch missing");
  await checkFeedback("user-allocations", start); assert.equal(document.activeElement, userControl); assert.equal(mobileUser.value, "two");

  await route("#my-quota", "[data-quota-refresh]");
  const quotaMain = document.querySelector('.workspace-main'), quotaRefresh = document.querySelector('[data-quota-refresh]');
  const beforeQuotaReads = quotaReads;
  quotaRefresh.focus(); quotaRefresh.click();
  await waitFor(() => quotaReads > beforeQuotaReads && !quotaRefresh.disabled, "quota refresh missing");
  assert.equal(document.querySelector('.workspace-main'), quotaMain, "same quota refresh retains workspace");
  assert.equal(document.activeElement, quotaRefresh, "quota refresh retains focus");
  assert.equal(quotaMain.getAnimations().length, 0, "same quota response does not replay route entrance");

  await route("#dashboard", "[data-dashboard-traffic-month]");
  const picker = document.querySelector('[data-dashboard-traffic-month]');
  const choices = [...picker.querySelectorAll('[data-dashboard-month-option]')].filter(button => !button.disabled);
  // Always provide two past months, even when the current date is January.
  if (choices.length < 2) picker.querySelector('[data-dashboard-month-year-shift="-1"]').click();
  const months = [...picker.querySelectorAll('[data-dashboard-month-option]')].filter(button => !button.disabled);
  slowMonth = months[0].dataset.dashboardMonth;
  start = records.length; picker.open = true; months[0].click();
  await waitFor(() => releaseMonth, "first month was not requested");
  picker.open = true; months[1].click();
  const latest = months[1].dataset.dashboardMonth;
  await waitFor(() => document.querySelector('[data-motion-region="dashboard-month"]').dataset.motionKey === latest, "latest month missing");
  releaseMonth(); await delay(60);
  assert.equal(document.querySelector('[data-motion-region="dashboard-month"]').dataset.motionKey, latest, "late month cannot replace latest selection");
  assert.equal(document.querySelector('[data-dashboard-traffic-month]'), picker, "month selection retains picker DOM");
  assert.equal(document.activeElement, picker.querySelector('summary'), "month selection retains launcher focus");
  await checkFeedback("dashboard-month", start);
  failedMonth = months[2].dataset.dashboardMonth;
  picker.open = true; months[2].click();
  await waitFor(() => document.querySelector('[data-spa-notice]')?.textContent.includes("月份读取测试失败"), "month read failure missing");
  assert.equal(document.querySelector('[data-motion-region="dashboard-month"]').dataset.motionKey, latest, "failed month preserves displayed chart");
  assert.equal(document.querySelector('.dashboard-traffic-chart').getAttribute("aria-busy"), null);
  failedMonth = ""; start = records.length;
  picker.open = true; months[2].click();
  await waitFor(() => document.querySelector('[data-motion-region="dashboard-month"]').dataset.motionKey === months[2].dataset.dashboardMonth, "failed month cannot be retried");
  await checkFeedback("dashboard-month", start);
  slowMonth = months[0].dataset.dashboardMonth; releaseMonth = undefined;
  const livePicker = document.querySelector('[data-dashboard-traffic-month]');
  livePicker.open = true; livePicker.querySelector('[data-dashboard-month-option][data-month-index="1"]').click();
  await waitFor(() => releaseMonth, "late departing month was not requested");
  await route("#tasks", "[data-task-page]");
  releaseMonth(); await delay(40);
  assert.equal(document.querySelector('.dashboard-workspace'), null, "late month cannot restore a departed route");
  // A confirmation belongs to the selection and route that opened it.
  // Resolving an older prompt must never start a new config read.
  const host = document.createElement("section");
  host.innerHTML = '<article class="live-config-workspace"><a href="#" data-live-agent="bravo">bravo</a><button data-live-source="import">import</button><form id="live-config-form"><div data-code-editor><textarea data-code-input>original</textarea></div></form></article>';
  document.body.append(host);
  host.querySelector("textarea").value = "unsaved";
  const configState = { route: "live-config", navigationEpoch: 1, data: { liveAgent: "alpha", liveEngine: "mihomo", liveConfigSource: "qagent" } };
  const confirmations = []; let configReads = 0;
  bindLiveConfigNavigation({ state: configState, engineName: value => value, notify: () => {},
    confirmAction: () => new Promise(resolve => confirmations.push(resolve)) }, {
    agent: { id: "alpha" }, engine: "mihomo", accountData: configState.data, sourceMode: "qagent", current: { content: "original" },
    workspaceElement: host.firstElementChild, liveConfig: async () => { configReads++; },
  });
  host.querySelector('[data-live-agent]').click(); host.querySelector('[data-live-source]').click();
  assert.equal(confirmations.length, 2);
  confirmations[0](true); confirmations[1](false); await delay(10);
  assert.equal(configReads, 0, "superseded config selection cannot commit");
  assert.equal(configState.data.liveAgent, "alpha");
  host.querySelector('[data-live-agent]').click();
  configState.route = "settings"; configState.navigationEpoch++;
  confirmations[2](true); await delay(10);
  assert.equal(configReads, 0, "config confirmation cannot navigate after route departure");
  host.remove();
  for (const theme of ["light", "dark"]) {
    document.documentElement.dataset.theme = theme;
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "selection must not overflow viewport");
  }
  assert.equal(document.querySelector('.modal-backdrop'), null);
  if (reduced) assert.equal(document.getAnimations().length, 0);
  Element.prototype.animate = nativeAnimate;
}
