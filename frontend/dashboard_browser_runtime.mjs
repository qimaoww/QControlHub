import { accountStorage, setStorageAccount } from "./modules/account-storage.js";

const assert = (condition, message) => {
  if (!condition) throw new Error(message);
};
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const waitFor = async (test, message) => {
  const deadline = performance.now() + 7000;
  while (!test()) {
    if (performance.now() > deadline) throw new Error(message);
    await delay(25);
  }
};
const GiB = 1024 ** 3;

export async function testDashboardRuntime(mode, preview = false) {
  const cspViolations = [];
  document.addEventListener("securitypolicyviolation", (event) => cspViolations.push(event.effectiveDirective));
  const limited = mode === "dashboard-limited";
  const readonly = mode === "dashboard-readonly";
  const hasPanel = !limited && !readonly;
  const session = { role: limited || readonly ? "user" : "admin", user_id: limited || readonly ? "dashboard-user" : undefined, csrf_token: "browser-test-csrf",
    permissions: limited ? ["overview.read"] : ["overview.read", "agents.read", "tasks.read", "traffic.read", "metrics.read", "settings.read", "agent-config.read"] };
  setStorageAccount(session);
  accountStorage.setItem("qcontrolhub:node-card-order", JSON.stringify(["tokyo", "hongkong", "singapore", "frankfurt"]));
  const agents = [
    { id: "hongkong", name: "香港 · HK-01", os: "Debian", arch: "amd64", status: "online", capabilities: ["xray", "sing-box"] },
    { id: "singapore", name: "新加坡 · SG-02", os: "Ubuntu", arch: "arm64", status: "online", capabilities: ["mihomo", "ss-rust"] },
    { id: "tokyo", name: "东京 · JP-03", os: "Debian", arch: "amd64", status: "online", capabilities: ["sing-box", "xray", "mihomo", "ss-rust"] },
    { id: "frankfurt", name: "法兰克福 · DE-04", os: "Alpine", arch: "amd64", status: "offline", capabilities: ["sing-box"] },
  ].map((agent) => ({ ...agent, last_seen: new Date(Date.now() - (agent.status === "offline" ? 12 * 60000 : 1000)).toISOString(),
    runtime: Object.fromEntries(agent.capabilities.map((engine, index) => [engine, { installed: true, service_status: index ? 'inactive' : 'active' }])),
    metrics: { network_available: true, network_rx_bps: 2048, network_tx_bps: 1024, collected_at: new Date().toISOString() }, features: [], labels: {} }));
  const tasks = [
    { id: "task-1", agent_id: "hongkong", action: "deploy", engine: "xray", status: "succeeded" },
    { id: "task-2", agent_id: "singapore", action: "upgrade-agent", engine: "", status: "running" },
    { id: "task-3", agent_id: "tokyo", action: "restart", engine: "sing-box", status: "pending" },
    { id: "task-4", agent_id: "frankfurt", action: "install", engine: "sing-box", status: "failed" },
  ].map((task, index) => ({ ...task, created_at: new Date(Date.now() - (index + 1) * 60000).toISOString() }));
  const fixture = {
    calls: [],
    failed: mode === "dashboard-unavailable",
    gate: null,
    metrics: {
      cpu_available: true, cpu_percent: 23.6,
      memory_available: true, memory_used_bytes: 3.48 * GiB, memory_total_bytes: 8 * GiB,
      disk_available: true, disk_used_bytes: 26.4 * GiB, disk_total_bytes: 80 * GiB,
      network_available: true, network_rx_bps: 1.6 * 1024 ** 2, network_tx_bps: 864 * 1024,
      network_rx_bytes: 68 * GiB, network_tx_bytes: 42 * GiB,
    },
  };
  window.__dashboardBrowserTestAPI = fixture;
  const json = (value, status = 200) => new Response(JSON.stringify(value), {
    status, headers: { "Content-Type": "application/json" },
  });
  window.fetch = async (input, options = {}) => {
    const url = new URL(input instanceof Request ? input.url : input, location.href);
    const path = url.pathname.replace(/^\/api\/v1/, "");
    fixture.calls.push({ path, query: url.search });
    if (path === "/preferences/node-order") return json({ ids: JSON.parse(accountStorage.getItem("qcontrolhub:node-card-order") || "[]"), revision: 1 });
    if (path === "/auth/session") return json(session);
    if (path === "/agent-access") return json({ isolated: false, shares: [] });
    if (path === "/overview") return json({ agents: 4, agents_online: 3, node_configs: 12, configs: 2, tasks_pending: 2, tasks_queued: 1, tasks_running: 1, tasks_failed: 1 });
    if (path === "/settings") return json({ panel_name: "QControlHub", ui_font_scale: 100, time_display: "relative", task_poll_interval_ms: 5000 });
    if (path === "/agents") return json(agents.map(agent => ({ ...agent, metrics: { ...agent.metrics, collected_at: new Date().toISOString() } })));
    if (path === "/tasks") return json(tasks);
    if (path === "/panel-metrics") {
      if (fixture.denied) return json({ error: "permission denied" }, 403);
      assert(hasPanel, "a user without panel-metrics.read requested global host counters");
      // Deliberately allow a late response after abort: the view must reject
      // it by route ownership even when a transport cannot cancel its work.
      const sample = { collected_at: new Date().toISOString(), ...fixture.metrics };
      if (fixture.gate) await fixture.gate;
      return fixture.failed ? json({ error: "test metrics temporarily unavailable" }, 503) : json(sample);
    }
    if (path === "/traffic-usage") {
      if (fixture.trafficFailed) return json({ error: "test traffic temporarily unavailable" }, 503);
      const month = url.searchParams.get("month");
      const current = new Date().toISOString().slice(0, 7);
      const dayCount = month === current ? new Date().getUTCDate() : 28;
      if (fixture.trafficGate) await fixture.trafficGate;
      return json({ month, timezone: "UTC", days: Array.from({ length: dayCount }, (_, index) => {
        const received = (1.2 + ((index * 7) % 11) * .43) * GiB * (fixture.trafficMultiplier || 1);
        const sent = (0.7 + ((index * 3) % 7) * .32) * GiB * (fixture.trafficMultiplier || 1);
        return { agent_id: agents[index % agents.length].id, day: `${month}-${String(index + 1).padStart(2, "0")}`, received_bytes: received, sent_bytes: sent, used_bytes: received + sent, peak_receive_bps: 3 * 1024 ** 2, peak_send_bps: 1.2 * 1024 ** 2 };
      }) });
    }
    throw new Error(`unexpected dashboard fixture request: ${path}`);
  };
  document.querySelector('link[href^="/assets/app.css"]')?.remove();
  location.hash = "#dashboard";
  const stylesheet = document.createElement("link"); stylesheet.rel = "stylesheet"; stylesheet.href = "/assets/analytics/theme.css";
  const styled = new Promise((resolve, reject) => { stylesheet.onload=resolve;stylesheet.onerror=reject; });
  document.head.append(stylesheet);await styled;
  (await import("./analytics/app.js")).startAnalytics({app:document.querySelector("#app"),session});
  const card = title => [...document.querySelectorAll(".a-main section.a-card")].find(card=>card.querySelector("h2")?.textContent===title);
  const panel = ()=>card("面板运行资源"), traffic = ()=>card("流量概况"), nodes = ()=>card("节点运行状态"), activity=()=>card("最近任务");
  const metric = label=>panel()?.querySelector(`[data-refresh-key="${label}"] strong`);
  const cpu = ()=>metric("CPU 使用率");
  const reads=path=>fixture.calls.filter(call=>call.path===path).length;
  const refreshButton=()=>document.querySelector(".a-top-inner > .a-actions > button");
  const settled=()=>!refreshButton()?.disabled;
  const refresh=async()=>{await waitFor(settled,"refresh remained busy");const count=reads("/overview");refreshButton().click();await waitFor(()=>reads("/overview")>count&&settled(),"manual refresh did not settle");};
  await waitFor(()=>document.title.startsWith("仪表盘")&&reads("/overview"),"dashboard did not render");
  assert(!document.body.textContent.includes("undefined"),"missing overview fields leaked");
  if(!hasPanel){
    assert(!panel(),"panel-host card leaked");assert(reads("/panel-metrics")===0,"unauthorized host request");
    if(limited){
      for(const path of ["/agents","/tasks","/traffic-usage","/settings","/preferences/node-order"])assert(reads(path)===0,`overview-only user requested ${path}`);
      assert(!nodes()&&!activity()&&!traffic(),"inaccessible shortcuts exposed");
      await waitFor(()=>document.querySelector(".a-main").textContent.includes("共 4 个节点"),"overview-only user lost allowed summary");
    }else assert(traffic(),"readonly user lost own traffic");
    return;
  }
  await waitFor(()=>fixture.failed?panel()?.textContent.includes("资源读取失败"):cpu()?.textContent==="23.6%","initial host metrics not settled");
  if(preview)return;
  if(fixture.failed){assert(cpu().textContent==="—","failed first sample became zero");assert(nodes()&&traffic(),"optional host failure blocked dashboard");fixture.failed=false;await refresh();}
  assert(cpu().textContent==="23.6%","CPU usage differs from panel API");
  assert(metric("内存使用率").textContent==="43.5%"&&metric("根目录磁盘").textContent==="33.0%","capacity percentages differ from the panel API");
  const summary = card('运行概况');
  assert(summary?.querySelector('[data-refresh-key="运行内核"] strong')?.textContent === '3', 'offline runtime counted as live or scoped installed cores lost');
  assert(summary?.querySelector('[data-refresh-key="节点速率合计"] strong')?.textContent !== '—', 'valid fleet rates missing');
  await waitFor(() => traffic()?.querySelectorAll('path[data-series]').length === 2, 'receive and send series are not independently plotted');
  const series = [...traffic().querySelectorAll('path[data-series]')];
  assert(series[0].getAttribute('stroke-dasharray') !== series[1].getAttribute('stroke-dasharray'), 'traffic directions rely only on color');
  assert(traffic().querySelector('[aria-label="节点流量排行"] a[href^="#traffic?agent="]'), 'ranked nodes cannot open their own traffic');
  await waitFor(()=>nodes()?.querySelector("tbody a")?.textContent.includes("东京"),"saved node order ignored");
  assert(!activity() && reads('/tasks') === 0, 'retired recent-task area still renders or polls');
  assert(traffic().querySelectorAll('[aria-label="节点流量排行"] > [data-refresh-key]').length === 3, 'node usage ranking does not show three authorized nodes');
  const bands = () => [...traffic().querySelectorAll('[data-chart-day]')];
  const firstDay = bands()[0], firstDate = firstDay.dataset.chartDay;
  firstDay.scrollIntoView({block:'center',behavior:'instant'});
  firstDay.dispatchEvent(new PointerEvent('pointerover', {bubbles:true,pointerType:'mouse'}));
  await waitFor(() => document.querySelector('.a-tooltip'), 'daily traffic hover has no tooltip');
  const tip = document.querySelector('.a-tooltip');
  assert(tip.textContent.includes(firstDate) && tip.textContent.replace(/\s/g,'').includes('接收1.2GB') && tip.textContent.includes('发送') && tip.textContent.includes('合计'), 'daily tooltip omits date, directions or actual usage');
  fixture.trafficMultiplier=2;await refresh();
  await waitFor(()=>tip.textContent.replace(/\s/g,'').includes('接收2.4GB'),'daily tooltip did not update its current sample');
  assert(document.querySelector('.a-tooltip')===tip && bands()[0]===firstDay,'poll recreated the hovered day or tooltip');
  fixture.trafficMultiplier=1;await refresh();
  firstDay.focus();
  firstDay.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowRight',bubbles:true,cancelable:true}));
  const keyboardDay=bands()[Math.min(1,bands().length-1)];
  await waitFor(()=>document.activeElement===keyboardDay && document.querySelector('.a-tooltip')?.textContent.includes(keyboardDay.dataset.chartDay),'keyboard cannot inspect adjacent daily traffic');
  firstDay.dispatchEvent(new PointerEvent('pointerup',{bubbles:true,pointerType:'touch'}));
  firstDay.dispatchEvent(new PointerEvent('pointerout',{bubbles:true,pointerType:'touch'}));
  assert(document.querySelector('.a-tooltip')?.textContent.includes(firstDate),'touch cannot inspect daily traffic');
  document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));
  assert(!document.querySelector('.a-tooltip'),'Escape did not dismiss daily tooltip');
  const assertLayout=()=>{
    assert(document.documentElement.scrollWidth<=innerWidth+1,`viewport overflow ${innerWidth}`);
    for(const element of document.querySelectorAll(".a-stat,.a-card-body,.a-chart-container,.a-chart-meta,.a-stat-grid"))assert(element.scrollWidth<=element.clientWidth+1,`content overflow ${element.className}`);
    for(const element of document.querySelectorAll('[aria-label="节点运行状态"],[aria-label="最近任务"]'))assert(element.scrollWidth<=element.clientWidth+1,`dashboard summary needs horizontal scrolling ${element.getAttribute('aria-label')}`);
    const summary = traffic().querySelector('[data-refresh-key="月累计流量"]');
    assert(Math.abs(summary.querySelector('.a-stat-heading').getBoundingClientRect().left-summary.querySelector('.a-probe-network').getBoundingClientRect().left)<1,'monthly traffic and direction counters are not aligned');
    assert(Math.abs(summary.querySelector('.a-stat-heading').getBoundingClientRect().left-traffic().querySelector('h2').getBoundingClientRect().left)<1,'traffic summary ignores the card content edge');
    assert(Math.abs(traffic().querySelector('[name=traffic_month]').getBoundingClientRect().bottom-[...traffic().querySelectorAll('button')].find(button=>button.textContent==='流量明细').getBoundingClientRect().bottom)<1,'month and detail controls are not aligned');
  };
  for(const scale of [90,100,110,135]){document.documentElement.style.fontSize=`${scale}%`;await delay(80);assertLayout();}
  document.documentElement.style.fontSize="100%";await delay(80);
  const stablePanel=panel(),stableCPU=cpu(),stableNode=nodes().querySelector("tbody tr"),stableSVG=traffic().querySelector("svg");
  const month=()=>traffic().querySelector('[name="traffic_month"]');
  month().focus();fixture.metrics.cpu_percent=92;await refresh();
  assert(cpu().textContent==="92.0%","manual metrics refresh failed");
  assert(panel()===stablePanel&&cpu()===stableCPU,"refresh replaced metric nodes");
  assert(document.activeElement===month(),"refresh stole filter focus");
  [...traffic().querySelectorAll("button")].find(button=>button.textContent==="流量明细").click();
  const dialog=document.querySelector("dialog[open]");assert(dialog?.matches(":modal"),"traffic detail modal missing");
  const detailTable=dialog.querySelector('[aria-label="每日流量明细"]');assert(detailTable,"traffic daily data missing");
  const scroll=dialog.querySelector(".a-dialog-body");scroll.scrollTop=150;const beforeScroll=scroll.scrollTop;
  fixture.metrics.cpu_percent=41.2;await waitFor(()=>cpu()?.textContent==="41.2%","automatic metrics refresh failed");
  assert(dialog.matches(":modal")&&scroll.scrollTop===beforeScroll,"poll disturbed detail modal");
  assert(nodes().querySelector("tbody tr")===stableNode&&traffic().querySelector("svg")===stableSVG,"poll rebuilt unchanged business content");
  dialog.close();fixture.failed=true;await refresh();
  assert(panel().textContent.includes("刷新失败")&&cpu().textContent==="41.2%","failed refresh discarded metrics");
  fixture.failed=false;fixture.metrics.cpu_percent=0;fixture.metrics.memory_available=false;fixture.metrics.network_available=false;await refresh();
  assert(cpu().textContent==="0.0%","idle CPU became unavailable");assert(metric("内存使用率").textContent==="—","unavailable memory became zero");assert(metric("网络接收速率").textContent==="—","unavailable network became zero");
  fixture.metrics.memory_available=true;
  const memoryTotal=fixture.metrics.memory_total_bytes;
  for(const total of [0,-1,null,'10']) {
    fixture.metrics.memory_total_bytes=total;await refresh();
    assert(metric('内存使用率').textContent==='—','invalid memory capacity was displayed as a valid percentage');
  }
  fixture.metrics.memory_total_bytes=memoryTotal;fixture.metrics.memory_available=false;
  fixture.metrics.network_available=true;fixture.metrics.network_rx_bps=null;await refresh();
  assert(metric("网络接收速率").textContent==="—","invalid network sample became a real rate");
  fixture.metrics.network_rx_bps='<img src=x onerror=alert(1)>';await refresh();
  assert(metric("网络接收速率").textContent==="—"&&!panel().querySelector('img'),"untrusted metric became HTML or a real sample");
  fixture.metrics.network_rx_bps=2048;fixture.metrics.network_tx_bps=512;await refresh();
  assert(metric("网络接收速率").textContent.replace(/\s/g,'')==='2KB/s'&&panel().textContent.includes('容器可见'),"network recovery or container scope lost");
  fixture.metrics.collected_at="invalid";await refresh();
  assert(panel().textContent.includes("尚无有效采样"),"invalid timestamp presented a fresh sample");
  assert(!/NaN|undefined|Infinity/.test(panel().textContent),"invalid metric text leaked");

  fixture.metrics.collected_at=new Date(Date.now()-60000).toISOString();await refresh();assert(panel().textContent.includes("采样已过期"),"stale sample presented live");delete fixture.metrics.collected_at;
  fixture.denied=true;await refresh();assert(cpu().textContent==="—"&&panel().textContent.includes("权限"),"denied host read retained protected data");fixture.denied=false;await refresh();
  const chooseMonth=value=>{month().value=value;month().dispatchEvent(new Event("change",{bubbles:true}));};
  const period=()=>traffic().querySelector(".a-chart-meta").textContent;
  const oldMonth=month().value;
  assert(traffic().textContent.includes("UTC 今日"),"current-month total lost UTC day semantics");
  fixture.trafficFailed=true;const oldReads=reads("/traffic-usage");chooseMonth(`${new Date().getUTCFullYear()-1}-01`);
  await waitFor(()=>reads("/traffic-usage")>oldReads&&traffic().textContent.includes("刷新失败"),"failed month request not shown");
  assert(period().includes(oldMonth),"old chart attributed to failed month");
  fixture.metrics.cpu_percent=37;await waitFor(()=>cpu()?.textContent==="37.0%","failed month stopped host polling");fixture.trafficFailed=false;
  const february=`${new Date().getUTCFullYear()-1}-02`;chooseMonth(february);await waitFor(()=>period().includes(february),"February not loaded");
  const days=new Date(Date.UTC(new Date().getUTCFullYear()-1,2,0)).getUTCDate();assert(traffic().textContent.includes(`· ${days} 天`),"historical average uses incorrect day count");
  assert(!traffic().textContent.includes("UTC 今日"),"historical month claims current day");
  assert(traffic().querySelector('svg [d]')&&traffic().querySelector("svg").textContent.includes(`02-${days}`),"February axis lost final day");
  assertLayout();
  if(mode==="dashboard"){
    Object.defineProperty(document,"hidden",{configurable:true,value:true});document.dispatchEvent(new Event("visibilitychange"));const hidden=reads("/panel-metrics");await delay(5200);assert(reads("/panel-metrics")===hidden,"hidden dashboard polled");delete document.hidden;document.dispatchEvent(new Event("visibilitychange"));await waitFor(()=>reads("/panel-metrics")>hidden,"visible dashboard did not refresh");
  }
  // A pending optional resource cannot delay node/traffic refreshes or page navigation.
  let release;fixture.gate=new Promise(resolve=>{release=resolve;});const beforeGate=reads("/panel-metrics");refreshButton().click();await waitFor(()=>reads("/panel-metrics")>beforeGate,"slow metrics read missing");
  const beforeNodes=reads("/agents");await delay(5300);assert(reads("/agents")>beforeNodes,"slow host read blocked node polling");
  location.hash="#tasks";await waitFor(()=>document.title.startsWith("任务"),"route departure blocked on metrics");const afterLeave=reads("/panel-metrics");fixture.gate=null;release();await delay(2200);assert(!panel()&&reads("/panel-metrics")===afterLeave,"old metrics response/timer survived navigation");
  assert(cspViolations.length===0,`CSP violations: ${cspViolations.join(", ")}`);
}
