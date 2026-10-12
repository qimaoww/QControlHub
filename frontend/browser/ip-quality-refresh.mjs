import { assert, delay, waitFor } from "./assertions.mjs";
import { createShellFeedback } from "../modules/shell-feedback.js";

function painted(element) {
  if (!element?.isConnected || !element.checkVisibility({ opacityProperty: true, visibilityProperty: true })) return false;
  const rect = element.getBoundingClientRect();
  if (rect.width < 2 || rect.height < 2) return false;
  for (let parent = element; parent; parent = parent.parentElement) {
    const style = getComputedStyle(parent);
    if (parent.hidden || parent.inert || parent.getAttribute("aria-hidden") === "true" || Number(style.opacity) === 0) return false;
    if (parent.tagName === "DETAILS" && !parent.open && element !== parent.querySelector("summary")) return false;
  }
  return true;
}

export async function testIPQualityRefresh({ fixture, refresh, card, reportDay, olderDay, historicalRecords, mode }) {
  const record = structuredClone(fixture.records[0]);
  const feedback = createShellFeedback({});
  const main = () => document.querySelector(".workspace-main");
  const selectedDate = () => document.querySelector("[data-ip-quality-date]")?.dataset.selectedDate;
  const reads = () => fixture.calls.filter(call => call.path === "/ip-quality" && call.method === "GET").length;
  const details = () => card()?.querySelector(".ip-quality-details");
  const openDetails = async () => {
    if (!details().open) details().querySelector("summary").click();
    await waitFor(() => painted(card().querySelector(".ip-quality-report")) &&
      getComputedStyle(details(), "::details-content").opacity === "1", "expanded report did not become visible");
  };
  const assertContent = label => {
    for (const element of [main(), document.querySelector(".ip-quality-workspace"), card(), card()?.querySelector("h3"),
      card()?.querySelector(".ip-quality-addresses"), card()?.querySelector(".ip-quality-report"),
      ...card()?.querySelectorAll(".ip-quality-archive img") || []]) {
      assert.ok(painted(element), `${label}: content is not actually visible (${element?.tagName}.${element?.className}, opacity=${element && getComputedStyle(element).opacity}, height=${element?.getBoundingClientRect().height}, details=${details()?.open})`);
      if (element.tagName === "IMG") assert.ok(element.complete && element.naturalWidth > 0, `${label}: image has no decoded report`);
    }
  };
  await openDetails();
  await waitFor(() => main().getAnimations({ subtree: true }).every(animation => !animation.id.startsWith("qch-")),
    "report motion did not settle");
  // The success notice and production poll both use five seconds. Keep the
  // original live nodes through the notice's complete exit and three polls.
  feedback.notify("刷新稳定性验证");
  await refresh();
  await openDetails();
  const originalPanel = card(), originalImage = card().querySelector("img"), originalDetails = details();
  document.querySelector("[data-ip-quality-date]").click();
  const calendar = document.querySelector("[data-ip-quality-calendar]");
  const month = calendar.querySelector("header strong").textContent;
  main().scrollTop = 120;
  const count = reads(), start = performance.now();
  let scroll, readingTop;
  const samples = [];
  const sampler = setInterval(() => samples.push({ at: performance.now() - start,
    workspace: painted(document.querySelector(".ip-quality-workspace")), panel: painted(card()),
    text: painted(card()?.querySelector(".ip-quality-report")),
    images: [...card()?.querySelectorAll(".ip-quality-archive img") || []].map(image => painted(image) && image.naturalWidth > 0),
    height: card()?.getBoundingClientRect().height || 0,
    detailOpen: details()?.open, detailKey: details()?.dataset.refreshKey,
    sameDetails: details() === originalDetails, samePanel: card() === originalPanel,
    detailPaint: details() && { opacity: getComputedStyle(details(), "::details-content").opacity,
      height: getComputedStyle(details(), "::details-content").blockSize },
    retired: [...card()?.querySelectorAll("[inert],[aria-hidden=true]") || []].map(element => element.className),
  }), 50);
  try {
    await delay(5500);
    // Dismissing the notice legitimately removes layout height. Measure
    // unchanged reading position after that exit, across the next two polls.
    main().scrollTop = 120;
    scroll = main().scrollTop;
    readingTop = card().querySelector(".ip-quality-report").getBoundingClientRect().top;
    await delay(10000);
  } finally { clearInterval(sampler); }
  assert.ok(reads() >= count + 3, "three real five-second automatic polls did not complete");
  assert.ok(samples.every(sample => sample.workspace && sample.panel && sample.text && sample.images.length === 2 && sample.images.every(Boolean)),
    `automatic refresh erased content: ${JSON.stringify(samples.find(sample => !sample.workspace || !sample.panel || !sample.text))}`);
  assert.equal(card(), originalPanel, "unchanged polling replaced the node panel");
  assert.equal(card().querySelector("img"), originalImage, "unchanged polling reset an image");
  assert.equal(details(), originalDetails, "unchanged polling reset expanded details");
  assert.ok(details().open && calendar.open, "polling closed a disclosure or calendar");
  assert.equal(calendar.querySelector("header strong").textContent, month, "polling moved the calendar month");
  assert.equal(main().scrollTop, scroll, "polling moved the reading position");
  assert.ok(Math.abs(card().querySelector(".ip-quality-report").getBoundingClientRect().top - readingTop) < 1,
    "unchanged polling moved the report's reading anchor");
  window.ipQualityMeasurements = { mode, intervalMs: 5000, polls: reads() - count, samples, requests: fixture.calls };
  document.querySelector("[data-ip-quality-date]").click();

  const failureSamples = [];
  const moved = [], preservedRegion = card().parentElement;
  const observer = new MutationObserver(records => {
    for (const record of records) for (const node of record.addedNodes)
      if ([originalDetails, originalPanel, preservedRegion].includes(node)) moved.push({ target: record.target.className, node: node.className });
  });
  observer.observe(main(), { childList: true, subtree: true });
  const failureSampler = setInterval(() => failureSamples.push({ visible: painted(card()?.querySelector(".ip-quality-report")),
    open: details()?.open, sameDetails: details() === originalDetails, samePanel: card() === originalPanel,
    opacity: details() && getComputedStyle(details(), "::details-content").opacity,
    height: details() && getComputedStyle(details(), "::details-content").blockSize }), 50);
  fixture.failed = true;
  await refresh();
  await openDetails();
  await delay(400);
  assertContent("503 retains content");
  assert.equal(card().querySelector("img"), originalImage, "read failure reset the loaded image");
  fixture.failed = false;
  await refresh();
  await delay(400);
  clearInterval(failureSampler);
  observer.disconnect();
  assert.ok(failureSamples.every(sample => sample.visible), `failure/recovery replayed a hiding disclosure entrance: ${JSON.stringify({sample:failureSamples.find(sample => !sample.visible),moved})}`);
  assert.ok(document.querySelector(".ip-quality-alert").hidden, "recovery kept the read error");
  assert.ok(details().open, "recovery closed report details");
  fixture.invalid = true;
  await refresh();
  await openDetails();
  assertContent("malformed response retains content");
  assert.ok(document.querySelector(".ip-quality-alert"), "malformed response looked like no records");
  fixture.invalid = false;
  await refresh();

  for (const status of ["pending", "running", "failed", "canceled"]) {
    fixture.records = [{ task_id: "quality-refresh-task", agent_id: "quality-a", status,
      created_at: new Date().toISOString(), last_successful: record }];
    await refresh();
    await openDetails();
    assertContent(`${status} retains report`);
    assert.ok(card().querySelector("img").src.includes(`${record.task_id}/archives`), "active task claimed another task's report");
  }
  fixture.records = [record];
  await refresh();

  let release, oldSignal;
  fixture.readGate = (response, signal) => { oldSignal = signal; return new Promise(resolve => { release = () => resolve(response); }); };
  document.querySelector("[data-ip-quality-refresh]").click();
  await waitFor(() => release, "slow read did not start");
  await openDetails();
  assertContent("slow read retains content");
  document.querySelector('[data-ip-quality-agent="quality-c"]').click();
  assert.ok(document.querySelector('[data-ip-quality-panel="quality-c"]'), "node switch blocked on a slow read");
  document.querySelector('[data-ip-quality-agent="quality-a"]').click();
  document.querySelector("[data-ip-quality-latest]").click();
  await waitFor(() => !document.querySelector("[data-ip-quality-refresh]").disabled, "latest read did not finish");
  assert.ok(oldSignal.aborted, "superseded read was not canceled");
  release();
  await delay(30);
  assert.equal(selectedDate(), "", "stale slow response changed the date");
  assert.ok(card(), "stale response changed the node");

  const historical = historicalRecords.find(item => item.created_at.startsWith(reportDay));
  const goDate = day => {
    document.querySelector("[data-ip-quality-date]").click();
    const calendar = document.querySelector("[data-ip-quality-calendar]");
    for (let i = 0; i < 3 && !calendar.querySelector(`[data-ip-quality-date-option="${day}"]`); i++) {
      const visible = calendar.querySelector("[data-ip-quality-date-option]").dataset.ipQualityDateOption.slice(0, 7);
      const direction = visible < day.slice(0, 7) ? "下个月" : "上个月";
      calendar.querySelector(`[data-ip-quality-month][aria-label="${direction}"]`).click();
    }
    calendar.querySelector(`[data-ip-quality-date-option="${day}"]`).click();
  };
  release = null;
  fixture.readGate = response => new Promise(resolve => { release = () => resolve(response); });
  goDate(olderDay);
  await waitFor(() => release, "slow history did not start");
  goDate(reportDay);
  await waitFor(() => selectedDate() === reportDay && card()?.querySelector("img")?.src.includes(historical.task_id), "newest date did not win");
  release(); await delay(30);
  assert.equal(selectedDate(), reportDay, "obsolete history changed the selected date");
  document.querySelector("[data-ip-quality-latest]").click();
  await waitFor(() => selectedDate() === "" && !document.querySelector("[data-ip-quality-refresh]").disabled, "return to latest did not finish");

  // visibilityState is controlled here because the CDP smoke runner has one
  // page; the manual Playwright validation also hides/restores a real tab.
  const descriptor = Object.getOwnPropertyDescriptor(document, "hidden");
  Object.defineProperty(document, "hidden", { configurable: true, value: true });
  document.dispatchEvent(new Event("visibilitychange"));
  const hiddenReads = reads();
  await delay(5100);
  assert.equal(reads(), hiddenReads, "hidden tab kept polling");
  Object.defineProperty(document, "hidden", { configurable: true, value: false });
  document.dispatchEvent(new Event("visibilitychange"));
  await waitFor(() => reads() === hiddenReads + 1, "visible tab did not refresh once");
  if (descriptor) Object.defineProperty(document, "hidden", descriptor); else delete document.hidden;

  release = null;
  fixture.readGate = response => new Promise(resolve => { release = () => resolve(response); });
  document.querySelector("[data-ip-quality-refresh]").click();
  await waitFor(() => release, "leaving-page read did not start");
  location.hash = "#dashboard";
  await waitFor(() => document.body.classList.contains("page-dashboard"), "page did not leave IP quality");
  const leftReads = reads();
  release(); await delay(5100);
  assert.equal(reads(), leftReads, "old page resumed polling after navigation");
  location.hash = "#ip-quality";
  await waitFor(() => card() && !document.querySelector("[data-ip-quality-refresh]").disabled, "page did not reenter");
  await openDetails(); assertContent("reentry");

  fixture.readStatus = 403;
  await refresh();
  assert.ok(document.querySelector(".ip-quality-alert").textContent.includes("权限"), "permission denial looked like empty records");
  assert.equal(document.querySelector(".ip-quality-archive img"), null, "permission denial retained protected reports");
  fixture.readStatus = 0;
  await refresh();
  fixture.agents = [];
  fixture.failed = true;
  await refresh();
  assert.equal(document.querySelector("[data-ip-quality-panel]"), null, "node permission removal retained a panel during a failed report read");
  fixture.failed = false;
  fixture.agents = null;
  await refresh();
  fixture.records = [];
  await refresh();
  assert.ok(card().textContent.includes("已删除或不再可用"), "deleted records looked like an unread or first-time result");
  const otherNode = { ...record, agent_id: "quality-c", task_id: "quality-other-node" };
  fixture.records = [record, otherNode];
  await refresh();
  // A response above the cache cap must invalidate the old smaller snapshot,
  // otherwise a return navigation can resurrect its deleted node report.
  fixture.records = [{ ...otherNode, cache_size_probe: "x".repeat(2 * 1024 * 1024) }];
  await refresh();
  assert.ok(card().textContent.includes("已删除或不再可用"), "one deleted node record looked like a first-time result while another node retained records");
  assert.equal(card().querySelector("img"), null, "one deleted node retained its obsolete report");
  goDate(reportDay);
  await waitFor(() => selectedDate() === reportDay && !document.querySelector("[data-ip-quality-refresh]").disabled,
    "history read before large-snapshot return did not complete");
  release = null;
  fixture.readGate = response => new Promise(resolve => { release = () => resolve(response); });
  document.querySelector("[data-ip-quality-latest]").click();
  await waitFor(() => release && card(), "large-snapshot return read did not start");
  assert.equal(card().querySelector("img"), null, "oversized response left a stale cached report that returned during a slow read");
  release();
  await waitFor(() => !document.querySelector("[data-ip-quality-refresh]").disabled, "large-snapshot return read did not complete");
  fixture.records = [record];
  await refresh();
  await openDetails(); assertContent("final recovery");
  fixture.records = [{ ...record, task_id: `quality-image-failure-${mode}-${crypto.randomUUID().replace(/[^a-f]/g, "")}` }];
  await refresh();
  await openDetails();
  const failedImage = card().querySelector("img");
  const imageError = failedImage.closest("figure").querySelector(".ip-quality-archive-error");
  failedImage.scrollIntoView({ block: "center" });
  await waitFor(() => failedImage.complete && !failedImage.naturalWidth && !imageError.hidden, "failed report image had no retry feedback");
  assert.ok(painted(card().querySelector(".ip-quality-report")), "image failure hid the textual report");
  await refresh();
  assert.equal(card().querySelector("img"), failedImage, "polling needlessly reset the failed image");
  assert.ok(!imageError.hidden, "polling erased image failure feedback");
  imageError.querySelector("button").click();
  await waitFor(() => failedImage.complete && failedImage.naturalWidth > 0 && imageError.hidden, "report image retry did not recover");
  fixture.records = [record];
  await refresh();
  await openDetails();
  await waitFor(() => [...card().querySelectorAll("img")].every(image => image.complete && image.naturalWidth > 0), "final images did not load");
  for (const element of [card().querySelector("h3"), card().querySelector(".ip-quality-report h4"), ...card().querySelectorAll("img")]) {
    element.scrollIntoView({ block: "center", behavior: "instant" });
    await waitFor(() => element.getBoundingClientRect().bottom > 0 && element.getBoundingClientRect().top < innerHeight,
      "report cannot be read in the viewport");
    assert.ok(painted(element), "scrolling/lazy load hid report content");
    const rect = element.getBoundingClientRect();
    assert.ok(rect.bottom > 0 && rect.top < innerHeight, "report cannot be read in the viewport");
  }
  feedback.resetFeedback();
}
