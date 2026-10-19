import { assert, waitFor } from "./assertions.mjs";
import { accountStorage } from "../modules/account-storage.js";

export async function testLargeLogRuntime({ testAPI }) {
await waitFor(() => document.querySelector(".desktop-app"), "initial shell missing");
  let releaseAgents;
  testAPI.agentsGate = new Promise((resolve) => { releaseAgents = resolve; });
  const began = performance.now();
  location.hash = "#core-logs";
  await waitFor(() => document.querySelectorAll(".core-log-row").length === 200, "default log page missing");
  assert.match(document.querySelector(".core-log-status").textContent, /已加载 4000 条/);
  const initial = performance.now() - began;
  testAPI.agentsGate = null;
  releaseAgents();
  document.querySelector("[data-toggle-core-log-refresh]").click();
  const selectedAt = performance.now();
  const limit = document.querySelector('#core-log-filters select[name="limit"]');
  limit.value = "2000";
  limit.dispatchEvent(new Event("change", { bubbles: true }));
  await waitFor(() => document.querySelector(".core-log-status")?.textContent.includes("已加载 8000 条"), "2000/engine result missing");
  const expanded = performance.now() - selectedAt;
  assert.equal(document.querySelectorAll(".core-log-row").length, 200, "8000 entries must not create 8000 DOM rows");
  assert.equal(document.querySelector(".core-log-stream .core-log-pagination"), null, "pagination must not split headings and log rows");
  assert.equal(document.querySelectorAll(".core-log-result-toolbar .core-log-pagination, .core-log-result-footer .core-log-pagination").length, 2);
  const toolbarBox = document.querySelector(".core-log-result-toolbar .core-log-pagination").getBoundingClientRect();
  const streamBox = document.querySelector(".core-log-stream").getBoundingClientRect();
  assert.ok(toolbarBox.bottom <= streamBox.top && Math.abs(toolbarBox.right - streamBox.right) < 10, "top pagination must sit outside and align with the table's right edge");
  const reads = testAPI.calls.filter((call) => call.path === "/core-logs").length;
  const pageAt = performance.now();
  document.querySelector('[data-core-log-page-index="1"]').click();
  await waitFor(() => document.querySelector(".core-log-pagination")?.textContent.includes("第 2 / 40 页"), "second page missing");
  assert.match(document.querySelector(".core-log-row pre").textContent, /entry 200/);
  const pageTime = performance.now() - pageAt;
  const filterAt = performance.now();
  const search = document.querySelector('#core-log-filters input[name="q"]');
  search.value = "pressure entry 1999";
  search.dispatchEvent(new Event("input", { bubbles: true }));
  await waitFor(() => document.querySelectorAll(".core-log-row").length === 4, "filter must search beyond the displayed page");
  const filterTime = performance.now() - filterAt;
  assert.equal(testAPI.calls.filter((call) => call.path === "/core-logs").length, reads, "local pagination/filtering must not query the remote database");
  document.querySelector("[data-reset-core-logs]").click();
  const gated = () => {
    let resolve;
    const promise = new Promise((yes) => { resolve = yes; });
    return { promise, resolve };
  };
  const preview = gated(), full = gated();
  testAPI.logGates = { "bravo:200": preview.promise, "bravo:2000": full.promise };
  const switchAt = performance.now();
  document.querySelector('[data-core-log-agent="bravo"]').click();
  assert.ok(document.querySelector('[data-core-log-agent="bravo"]').classList.contains("active"), "sidebar selection must update before network completion");
  assert.equal(document.querySelectorAll(".core-log-row").length, 0, "old node's logs must disappear immediately");
  const acknowledgement = performance.now() - switchAt;
  const previewAt = performance.now();
  preview.resolve();
  await waitFor(() => document.querySelector(".core-log-status")?.textContent.includes("已加载 800 条"), "node preview did not render before full window");
  const previewTime = performance.now() - previewAt;
  assert.match(document.querySelector(".core-log-row pre").textContent, /bravo/);
  assert.match(document.querySelector("[data-core-log-refresh-label]").textContent, /正在补齐/);
  full.resolve();
  await waitFor(() => document.querySelector(".core-log-status")?.textContent.includes("已加载 8000 条"), "node's requested window was truncated");
  document.querySelector('[data-core-log-agent=""]').click();
  await waitFor(() => document.querySelector(".core-log-row pre")?.textContent.includes("alpha"), "return to all logs failed");
  const refreshGate = gated();
  testAPI.logGates["bravo:2000"] = refreshGate.promise;
  const cachedAt = performance.now();
  document.querySelector('[data-core-log-agent="bravo"]').click();
  assert.equal(document.querySelectorAll(".core-log-row").length, 0, "scope changes hide cached rows until grants are revalidated");
  await waitFor(() => document.querySelector(".core-log-row pre")?.textContent.includes("bravo"), "authorized cached node did not render before the pending log refresh");
  assert.match(document.querySelector("[data-core-log-refresh-label]").textContent, /已显示缓存/);
  const cachedTime = performance.now() - cachedAt;
  refreshGate.resolve();
  await waitFor(() => document.querySelector("[data-core-log-refresh-label]")?.textContent === "自动更新已暂停", "cache revalidation did not settle");
  const storedPreference = JSON.parse(accountStorage.getItem("qcontrolhub:core-log-preferences"));
  assert.equal(storedPreference.agent_id, "bravo", "the selected node must be remembered");
  assert.equal(storedPreference.limit, 2000, "the expanded per-engine window must be remembered");
  assert.equal(storedPreference.auto_refresh, false, "a paused live stream must be remembered");
  assert.equal("engine" in storedPreference || "level" in storedPreference || "q" in storedPreference, false, "cleared local filters must not be remembered");
  window.logPressureResult = { loaded: 8000, domRows: 200, initialMs: Math.round(initial), expandedMs: Math.round(expanded), pageMs: Math.round(pageTime), filterMs: Math.round(filterTime), switchAckMs: Math.round(acknowledgement), previewRenderMs: Math.round(previewTime), cachedSwitchMs: Math.round(cachedTime) };
  assert.ok(initial < 5000 && expanded < 5000 && pageTime < 2000 && filterTime < 2000,
    `large log UI exceeded smoke responsiveness budget: ${JSON.stringify(window.logPressureResult)}`);
  assert.ok(acknowledgement < 500 && previewTime < 1000 && cachedTime < 500,
    `node switch exceeded local rendering budget: ${JSON.stringify(window.logPressureResult)}`);
  await testStableLogRuntime({ testAPI });
}

async function testStableLogRuntime({ testAPI }) {
  const search = document.querySelector('#core-log-filters input[name="q"]');
  search.focus();
  // A preceding ordinary input may still be queued when IME composition
  // starts in the same frame. It must not filter the unfinished composition.
  search.value = "pressure entry 1999";
  search.dispatchEvent(new InputEvent("input", { bubbles: true }));
  search.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
  search.value = "中文";
  search.dispatchEvent(new InputEvent("input", { bubbles: true, isComposing: true }));
  await new Promise(resolve => requestAnimationFrame(resolve));
  assert.equal(document.querySelectorAll(".core-log-row").length, 200, "composition must not filter intermediate Chinese input");
  search.value = "pressure entry 1999";
  search.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true }));
  await waitFor(() => document.querySelectorAll(".core-log-row").length === 4, "completed composition must filter the full window");
  assert.equal(document.activeElement, search);
  search.setSelectionRange(4, 8);

  let resolveMetadata;
  testAPI.agentsGate = new Promise(resolve => { resolveMetadata = resolve; });
  const originalTimer = window.setTimeout;
  const delays = [];
  window.setTimeout = (callback, delay, ...args) => {
    if (delay >= 10_000 && delay <= 60_000) {
      delays.push(delay);
      return originalTimer(callback, 40, ...args);
    }
    return originalTimer(callback, delay, ...args);
  };
  const stream = document.querySelector(".core-log-stream");
  let mutations = 0;
  const observer = new MutationObserver(records => { mutations += records.length; });
  observer.observe(stream, { childList: true, subtree: true, characterData: true });
  try {
    const reads = () => testAPI.calls.filter(call => call.path === "/core-logs").length;
    const before = reads();
    const responsesBefore = testAPI.logResponses.length;
    document.querySelector("[data-toggle-core-log-refresh]").click();
    await waitFor(() => reads() >= before + 3, "continuous log polling did not run");
    assert.equal(document.activeElement, search, "live refresh preserves search focus");
    assert.equal(search.selectionStart, 4, "live refresh preserves the caret");
    assert.equal(search.selectionEnd, 8);
    assert.equal(mutations, 0, "identical logs must not mutate the stream");
    assert.ok(testAPI.logResponses.slice(responsesBefore).filter(response => response.status === 304).length >= 2,
      "unchanged polls must use HTTP validators instead of downloading the log window");
    testAPI.agents = testAPI.agents.map(agent => ({ ...agent, metrics: { ...agent.metrics, cpu_percent: 73 } }));
    resolveMetadata();
    testAPI.agentsGate = null;
    await waitFor(() => reads() >= before + 5, "metadata/live refresh did not settle");
    assert.equal(mutations, 0, "metrics-only metadata must not redraw logs");
    testAPI.logRevision = 1;
    await waitFor(() => document.querySelector(".core-log-row pre")?.textContent.includes("corrected"), "same-ID content correction must invalidate the validator");
    assert.equal(document.activeElement, search, "changed refresh preserves search focus");
    assert.equal(search.selectionStart, 4);
    assert.equal(search.selectionEnd, 8);

    testAPI.logFailure = 503;
    await waitFor(() => document.querySelector('[data-core-log-refresh-label]')?.textContent.includes("刷新失败"), "failed refresh must retain the stream and show feedback");
    assert.equal(document.querySelectorAll(".core-log-row").length, 4);
    await waitFor(() => delays.some(delay => delay >= 20_000), "failures must back off");
    testAPI.logFailure = 0;
    await waitFor(() => document.querySelector('[data-core-log-refresh-label]')?.textContent === "正在实时更新", "polling must recover");

    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    document.dispatchEvent(new Event("visibilitychange"));
    const hiddenReads = reads();
    await new Promise(resolve => originalTimer(resolve, 120));
    assert.equal(reads(), hiddenReads, "hidden tabs stop polling");
    Object.defineProperty(document, "hidden", { configurable: true, value: false });
    document.dispatchEvent(new Event("visibilitychange"));
    await waitFor(() => reads() > hiddenReads, "foregrounding revalidates immediately");
    document.querySelector("[data-toggle-core-log-refresh]").click();

    document.querySelector("[data-reset-core-logs]").click();
    testAPI.longLogs = true;
    // Re-enter to fetch long snapshots using the same 8000-entry fixture.
    location.hash = "#node-settings";
    await waitFor(() => !document.querySelector("[data-core-log-page]"), "leaving logs failed");
    location.hash = "#core-logs";
    await waitFor(() => document.querySelector(".core-log-row pre")?.textContent.includes("长日志"), "long log window did not load");
    assert.equal(document.querySelectorAll(".core-log-row").length, 200);
    assert.ok(document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1, "long logs overflow the desktop/mobile viewport");
    const began = performance.now();
    document.querySelector('[data-core-log-page-index="1"]').click();
    await waitFor(() => document.querySelector(".core-log-pagination")?.textContent.includes("第 2 / 40 页"), "long log second page missing");
    window.logPressureResult.longPageMs = Math.round(performance.now() - began);
    const main = document.querySelector(".workspace-main");
    const workspace = main.scrollHeight > main.clientHeight ? main : document.scrollingElement;
    workspace.scrollTo({ top: 1200, behavior: "instant" });
    // Conditional polls can complete before the route's 480 ms entrance
    // animation. A moving parent is not a stable reading anchor; await the
    // actual finite animations instead of depending on JSON parsing delay.
    const entrance = [main, document.querySelector("[data-core-log-page]")]
      .flatMap(element => element.getAnimations()).filter(animation => Number.isFinite(animation.effect.getComputedTiming().endTime));
    await Promise.all(entrance.map(animation => animation.finished.catch(() => {})));
    // content-visibility resolves newly visible long rows on the next frame.
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const position = workspace.scrollTop;
    document.querySelector("[data-toggle-core-log-refresh]").click();
    const pageReads = reads();
    await waitFor(() => reads() >= pageReads + 2, "long log background refresh missing");
    assert.match(document.querySelector(".core-log-pagination").textContent, /第 2 \/ 40 页/, "refresh preserves the page");
    assert.equal(workspace.scrollTop, position, "unchanged refresh preserves the reading position");
    const top = workspace === document.scrollingElement ? 0 : Math.max(0, workspace.getBoundingClientRect().top);
    const anchor = [...document.querySelectorAll(".core-log-row")].find(row => row.getBoundingClientRect().bottom > top);
    const anchorTop = anchor.getBoundingClientRect().top;
    testAPI.logNewCount = 1;
    const appendReads = reads();
    await waitFor(() => reads() >= appendReads + 2, "new log refresh missing");
    assert.equal(anchor.isConnected, true, "retained rows keep their DOM identity after new logs arrive");
    assert.ok(Math.abs(anchor.getBoundingClientRect().top - anchorTop) < 2, `new logs must preserve the visible reading anchor: before=${anchorTop} after=${anchor.getBoundingClientRect().top}`);
    assert.match(document.querySelector(".core-log-pagination").textContent, /第 2 \/ 40 页/);
    document.querySelector("[data-toggle-core-log-refresh]").click();
    location.hash = "#node-settings";
    await waitFor(() => !document.querySelector("[data-core-log-page]"), "route leave did not stop logs");
    const leftReads = reads();
    await new Promise(resolve => originalTimer(resolve, 120));
    assert.equal(reads(), leftReads, "route leave must stop polling");
  } finally {
    observer.disconnect();
    window.setTimeout = originalTimer;
    delete document.hidden;
    testAPI.agentsGate = null;
    testAPI.logFailure = 0;
    testAPI.logNewCount = 0;
    testAPI.logRevision = 0;
  }
}

export async function testLogPreferenceRestoreRuntime({ testAPI }) {
await waitFor(() => document.querySelector(".desktop-app"), "initial shell missing");
  location.hash = "#core-logs";
  await waitFor(() => document.querySelector(".core-log-status")?.textContent.includes("已加载 8000 条"), "restored per-engine window did not load");
  const logCalls = testAPI.calls.filter((call) => call.path === "/core-logs");
  assert.ok(logCalls.length > 0, "the restored page must still read logs");
  assert.ok(logCalls.every((call) => call.query.includes("agent_id=bravo")), "every log read must use the restored node scope");
  assert.ok(logCalls.some((call) => call.query.includes("limit=2000")), "the restored per-engine window must be requested");
  assert.ok(document.querySelector('[data-core-log-agent="bravo"]').classList.contains("active"), "the restored node must be selected in the sidebar");
  assert.equal(document.querySelector('[data-core-log-agent=""]').classList.contains("active"), false, "the aggregate node link must not stay active");
  assert.equal(document.querySelector('[data-core-log-engine][value="xray"]').getAttribute("aria-pressed"), "true", "the restored engine filter must render as pressed");
  assert.equal(document.querySelector('[data-core-log-level][value="warning"]').getAttribute("aria-pressed"), "true", "the restored level filter must render as pressed");
  assert.equal(document.querySelector('#core-log-filters input[name="q"]').value, "pressure entry 1999", "the restored keyword must fill the search box");
  assert.equal(document.querySelector('#core-log-filters select[name="limit"]').value, "2000", "the restored window must select its option");
  assert.equal(document.querySelector("[data-toggle-core-log-refresh]").getAttribute("aria-checked"), "false", "a paused live stream must stay paused");
  assert.match(document.querySelector("[data-core-log-refresh-label]").textContent, /自动更新已暂停/, "a restored pause must stay visible");
  assert.match(document.querySelector(".core-log-status").textContent, /显示 1 条结果/);
  assert.match(document.querySelector(".core-log-row pre").textContent, /pressure entry 1999/);
  document.querySelector("[data-toggle-core-log-refresh]").click();
  assert.equal(JSON.parse(accountStorage.getItem("qcontrolhub:core-log-preferences")).auto_refresh, true, "resuming the restored stream must be remembered");
}
