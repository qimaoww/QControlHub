import { assert, delay, waitFor } from "./assertions.mjs";

function painted(element) {
  return Boolean(element?.isConnected && element.checkVisibility({ opacityProperty: true, visibilityProperty: true }) && element.getBoundingClientRect().height > 1);
}

export async function testIPQualityRefresh({ fixture, refresh, card, reportDay, olderDay, historicalRecords, mode, goDate, latest, sidebarLink, selectedDate, settled, refreshButton }) {
  const record = structuredClone(fixture.records[0]);
  const main = () => document.querySelector(".a-main");
  const reads = () => fixture.calls.filter(call => call.path === "/ip-quality" && call.method === "GET").length;
  const loaded = async () => {
    for (const image of card()?.querySelectorAll("img") || []) image.scrollIntoView({ block: "center", behavior: "instant" });
    await waitFor(() => [...card()?.querySelectorAll("img") || []].every(image => image.complete && image.naturalWidth), "inline report did not load");
  };
  const assertContent = label => {
    for (const element of [main(), card(), card()?.querySelector("h2"), ...card()?.querySelectorAll("img") || []]) {
      assert.ok(painted(element), `${label}: report is hidden`);
      if (element.tagName === "IMG") assert.ok(element.complete && element.naturalWidth, `${label}: report not decoded`);
    }
  };
  await loaded();
  const originalPanel = card().querySelector("section"), originalImage = card().querySelector("img");
  selectedDate().focus(); document.scrollingElement.scrollTop = 120;
  const count = reads(), scroll = document.scrollingElement.scrollTop, readingTop = originalImage.getBoundingClientRect().top;
  const samples = [], start = performance.now();
  const sampler = setInterval(() => samples.push({ at: performance.now()-start, panel: painted(card()),
    samePanel: card()?.querySelector("section") === originalPanel, anchor:originalImage.getBoundingClientRect().top, scroll:document.scrollingElement.scrollTop, header:originalPanel.textContent,
    images: [...card()?.querySelectorAll("img") || []].map(image => painted(image) && image.naturalWidth > 0) }), 50);
  try { await delay(15300); } finally { clearInterval(sampler); }
  assert.ok(reads() >= count + 3, "three real five-second polls did not complete");
  assert.ok(samples.every(sample => sample.panel && sample.samePanel && sample.images.length === 2 && sample.images.every(Boolean)), "polling hid or rebuilt report");
  assert.equal(card().querySelector("img"), originalImage, "polling replaced image");
  assert.equal(document.scrollingElement.scrollTop, scroll, "polling moved scroll");
  assert.ok(Math.abs(originalImage.getBoundingClientRect().top-readingTop)<1, `polling moved reading anchor: ${JSON.stringify({before:readingTop,after:originalImage.getBoundingClientRect().top,first:samples[0],last:samples.at(-1)})}`);
  assert.equal(document.activeElement, selectedDate(), "polling stole date focus");
  window.ipQualityMeasurements = { mode, intervalMs:5000, polls:reads()-count, samples, requests:fixture.calls };
  selectedDate().blur();
  const retained = async label => { await refresh(); await loaded(); assertContent(label); assert.equal(card().querySelector("img"), originalImage, `${label}: image replaced`); };
  fixture.failed = true; await retained("503");
  fixture.failed = false; await retained("recovery");
  fixture.invalid = true; await retained("malformed response");
  assert.ok(main().textContent.includes("读取失败"), "invalid response looked empty");
  fixture.invalid = false; await retained("valid recovery");
  for (const status of ["pending", "running", "failed", "canceled"]) {
    fixture.records = [{task_id:"quality-refresh-task",agent_id:"quality-a",status,created_at:new Date().toISOString(),last_successful:record}];
    await refresh(); await loaded(); assertContent(status);
    assert.ok(card().querySelector("img").src.includes(`${record.task_id}/archives`), "task claimed prior report");
  }
  fixture.records = [record]; await refresh();
  let release, oldSignal;
  fixture.readGate = (response, signal) => { oldSignal=signal; return new Promise(resolve => {release=()=>resolve(response);}); };
  refreshButton().click(); await waitFor(()=>release,"slow read missing");
  assertContent("slow read"); sidebarLink("quality-c").click();
  assert.ok(document.querySelector('[data-ip-quality-panel="quality-c"]'), "slow read blocked node switch");
  sidebarLink("quality-a").click(); await latest();
  assert.ok(oldSignal.aborted, "superseded read not aborted"); release(); await delay(30);
  assert.equal(selectedDate().value,"", "old read changed date");
  assert.ok(document.querySelector('[data-ip-quality-panel="quality-a"]'), "old read changed node");
  const historical = historicalRecords.find(item=>item.created_at.startsWith(reportDay));
  release=null;fixture.readGate=response=>new Promise(resolve=>{release=()=>resolve(response);});
  selectedDate().value=olderDay;selectedDate().dispatchEvent(new Event("change",{bubbles:true})); await waitFor(()=>release,"slow historical read missing");
  await goDate(reportDay); release(); await delay(30);
  assert.equal(selectedDate().value,reportDay,"old date replaced newer date");
  assert.ok(card().querySelector("img").src.includes(historical.task_id)); await latest();
  const descriptor=Object.getOwnPropertyDescriptor(document,"hidden");
  Object.defineProperty(document,"hidden",{configurable:true,value:true});document.dispatchEvent(new Event("visibilitychange"));
  const hiddenReads=reads();await delay(5100);assert.equal(reads(),hiddenReads,"hidden tab polled");
  Object.defineProperty(document,"hidden",{configurable:true,value:false});document.dispatchEvent(new Event("visibilitychange"));
  await waitFor(()=>reads()===hiddenReads+1,"visible tab failed to refresh");
  if(descriptor)Object.defineProperty(document,"hidden",descriptor);else delete document.hidden;
  release=null;fixture.readGate=response=>new Promise(resolve=>{release=()=>resolve(response);});
  refreshButton().click();await waitFor(()=>release,"leave-page read missing");
  location.hash="#dashboard";await waitFor(()=>document.title.startsWith("仪表盘"),"page did not leave");
  const leftReads=reads();release();await delay(5100);assert.equal(reads(),leftReads,"old page resumed polling");
  location.hash="#ip-quality";await waitFor(()=>card()&&settled(),"page did not reenter");await loaded();assertContent("reentry");
  fixture.readStatus=403;await refresh();assert.equal(card().querySelector("img"),null,"denial retained report");
  assert.ok(main().textContent.includes("读取失败"),"denial looked empty");
  fixture.readStatus=0;await refresh();fixture.agents=[];fixture.failed=true;await refresh();
  assert.equal(document.querySelector("[data-ip-quality-panel]"),null,"ownership loss retained panel during failed report read");
  fixture.failed=false;fixture.agents=null;await refresh();fixture.records=[];await refresh();
  assert.ok(card().textContent.includes("记录已移除"),"deleted record looked unread");
  const otherNode={...record,agent_id:"quality-c",task_id:"quality-other-node"};
  fixture.records=[record,otherNode];await refresh();
  fixture.records=[{...otherNode,cache_size_probe:"x".repeat(2*1024*1024)}];await refresh();
  assert.ok(card().textContent.includes("记录已移除"),"deleted node resurrected report");assert.equal(card().querySelector("img"),null);
  await goDate(reportDay);release=null;fixture.readGate=response=>new Promise(resolve=>{release=()=>resolve(response);});
  const returning=latest();await waitFor(()=>release&&card(),"large snapshot return missing");
  assert.equal(card().querySelector("img"),null,"oversized cache resurrected stale report");release();await returning;
  fixture.records=[record];await refresh();await loaded();assertContent("final recovery");
  fixture.records=[{...record,task_id:`quality-image-failure-${mode}-${crypto.randomUUID().replace(/[^a-f]/g,"")}`}];await refresh();
  const failedImage=card().querySelector("img");failedImage.scrollIntoView({block:"center"});
  const reportSection=failedImage.closest("section"), retry=()=>reportSection.querySelector("[data-report-retry]");
  await waitFor(()=>failedImage.complete&&!failedImage.naturalWidth&&!retry().hidden,"image error/retry missing");
  assert.ok(painted(card().querySelector("h2")),"image failure hid node heading");
  await refresh();assert.equal(card().querySelector("img"),failedImage,"polling reset failed image");assert.ok(!retry().hidden,"polling erased image error");
  retry().click();await waitFor(()=>failedImage.complete&&failedImage.naturalWidth&&!retry().checkVisibility(),"retry did not recover image");
  fixture.records=[record];await refresh();await loaded();
  for(const element of [card().querySelector("h2"),...card().querySelectorAll("img")]){
    element.scrollIntoView({block:"center",behavior:"instant"});assert.ok(painted(element),"scroll/lazy load hid report");
    const box=element.getBoundingClientRect();assert.ok(box.bottom>0&&box.top<innerHeight,"report cannot be read in viewport");
  }
}
