import { assert, delay, waitFor } from "./assertions.mjs";
import { accountStorage } from "../modules/account-storage.js";

export async function testPortNamesAndRuntimeRefresh({ testAPI }) {
location.hash = "#client-access";
  const row = (port) => document.querySelector(`[data-client-profile-port="${port}"]`)?.closest(".client-profile-row");
  await waitFor(() => row(20002), "两个端口没有渲染");
  const open = (port) => {row(port).querySelector("[data-client-display-open]").click(); return row(port).querySelector("dialog.client-display-dialog form");};
  let form = open(20001);
  assert.equal(form.querySelectorAll(".client-display-scope").length, 1, "端口作用范围只说明一次");
  const nameBox = form.elements.name.getBoundingClientRect();
  const addressBox = form.elements.address.getBoundingClientRect();
  const stacked = getComputedStyle(form.querySelector(".client-display-form-grid")).gridTemplateColumns.split(" ").length === 1;
  assert.ok(stacked ? addressBox.top > nameBox.bottom : Math.abs(nameBox.top - addressBox.top) <= 1,
    "客户端参数的帮助文本不得挤压相邻输入框");
  assert.equal(form.dataset.clientProfileTag,"ss-rust-1");
  form.elements.name.value = "香港 & ATT <edge>";
  testAPI.profileSaveFailure = true;
  form.requestSubmit();
  await waitFor(() => !form.querySelector('[type="submit"]').disabled,"失败保存按钮未恢复");
  assert.equal(form.closest("dialog").open,true,"保存失败关闭了弹窗");
  assert.equal(form.elements.name.value,"香港 & ATT <edge>","失败丢失草稿");
  testAPI.profileSaveFailure = false;
  let release;
  testAPI.profileSaveGate = new Promise((resolve) => {release=resolve;});
  const calls = testAPI.profileSaves.length;
  form.requestSubmit();form.requestSubmit();
  await waitFor(() => testAPI.profileSaves.length===calls+1,"未提交保存");
  assert.equal(testAPI.profileSaves.length,calls+1,"重复提交产生并行请求");
  release();testAPI.profileSaveGate=null;
  await waitFor(() => row(20001).querySelector("header b").textContent === "香港 & ATT <edge>","首端口名称未更新");
  assert.equal(row(20001).querySelector("header edge"),null,"名称未转义");
  assert.equal(row(20002).querySelector("header b").textContent,"ss-rust-2","改名影响第二端口");
  assert.equal("address" in testAPI.profileSaves.at(-1),false,"仅改名称意外固定了自动连接地址");
  assert.equal("address_mode" in testAPI.profileSaves.at(-1),false,"仅改名称意外覆盖协议栈");
  form=open(20002);form.elements.name.value="Tokyo 第二端口";form.requestSubmit();
  await waitFor(() => row(20002).querySelector("header b").textContent === "Tokyo 第二端口","第二端口名称未更新");
  document.querySelector("[data-refresh-client-access]").click();
  await waitFor(() => !document.querySelector("[data-refresh-client-access]").disabled,"刷新未完成");
  assert.equal(row(20001).querySelector("header b").textContent,"香港 & ATT <edge>","刷新串名");
  assert.equal(new URL(row(20002).querySelector(".client-share-control input").value).hash,"#Tokyo%20%E7%AC%AC%E4%BA%8C%E7%AB%AF%E5%8F%A3","分享链接未使用独立名称");
  form=open(20001);form.elements.name.value="";form.requestSubmit();
  await waitFor(() => row(20001).querySelector("header b").textContent === "ss-rust-1","清空未恢复入站标签");
  assert.equal(row(20002).querySelector("header b").textContent,"Tokyo 第二端口","清空影响其他端口");

  // 连接地址与地址协议栈都按内核、按监听端口独立保存
  const share = (port) => row(port).querySelector(".client-share-control input").value;
  form = open(20002);
  assert.equal(form.elements.namedItem("address_mode").value, "auto", "第二端口协议栈默认非自动");
  form.elements.namedItem("address_mode").value = "ipv6";
  form.requestSubmit();
  await waitFor(() => testAPI.profileModes[20002] === "ipv6", "协议栈未按端口保存");
  await waitFor(() => share(20002).includes("2001:db8::1"), "第二端口分享链接未切换到 IPv6");
  assert.equal(testAPI.profileModes[20001] || "auto", "auto", "协议栈修改影响了第一端口");
  assert.equal(share(20001).includes("edge.example.com"), true, "协议栈修改改写了第一端口分享链接");
  form = open(20001);
  assert.equal(form.elements.namedItem("address_mode").value, "auto", "第一端口继承了第二端口的协议栈");
  assert.equal(form.elements.namedItem("address").value, "", "第一端口不应预填自动地址");
  form = open(20002);
  assert.equal(form.elements.namedItem("address_mode").value, "ipv6", "重渲染丢失协议栈");
  assert.equal(form.elements.namedItem("address").value, "", "未覆盖端口不应预填自动地址");
  assert.equal(form.textContent.includes("[2001:db8::1]"), true, "自动识别地址未在说明中显示");
  form.elements.namedItem("address").value = "two.example.com";
  form.requestSubmit();
  await waitFor(() => testAPI.profileAddresses[20002] === "two.example.com", "连接地址未按端口保存");
  assert.equal(testAPI.profileAddresses[20001] || "", "", "连接地址修改影响了第一端口");
  await waitFor(() => share(20002).includes("two.example.com"), "第二端口分享链接未切换到手动地址");
  form = open(20002);
  assert.equal(form.querySelector("[data-clear-client-address]") === null, false, "手动地址缺少恢复自动识别入口");
  assert.equal(form.elements.namedItem("address").value, "two.example.com", "手动地址未回填到输入框");
  assert.equal(form.elements.namedItem("address_mode").disabled, true, "手动地址未锁定协议栈选择");
  form.querySelector("[data-clear-client-address]").click();
  await waitFor(() => (testAPI.profileAddresses[20002] || "") === "", "恢复自动识别未清除端口地址");
  await waitFor(() => share(20002).includes("2001:db8::1"), "恢复自动识别未回到协议栈地址");
  assert.equal("address" in testAPI.profileSaves.at(-1), true, "恢复自动识别未按端口提交地址");
  form = row(20002).querySelector("dialog.client-display-dialog form");
  assert.equal(form.elements.namedItem("address_mode").disabled, false, "恢复自动识别未解锁协议栈选择");
  assert.equal(form.elements.namedItem("address").value, "", "恢复自动识别后仍预填手动地址");

  location.hash="#settings-node-alpha";
  await waitFor(() => document.querySelector(".node-operations-workspace"),"节点详情未渲染");
  const runtime = (installed,version,service_status="running") => {
    testAPI.agents = testAPI.agents.map((agent) => agent.id!=="alpha"?agent:{...agent,runtime:{...agent.runtime,"sing-box":{installed,version,service_status}}});
  };
  document.querySelector('[data-node-tab="agent"]').click();
  const draft = document.querySelector('[data-agent-name-form="alpha"] input');draft.value="尚未保存的节点名称";
  runtime(true,"1.13.0");
  await waitFor(() => document.querySelector('.service-sing-box[data-core-installed="1"]'),"安装完成后详情没有自动刷新");
  assert.match(document.querySelector('[data-core-version="sing-box"]').textContent,/1\.13\.0/);
  assert.equal(document.querySelector('[data-agent-name-form="alpha"] input').value,"尚未保存的节点名称","刷新覆盖节点名称草稿");
  assert.equal(document.querySelector('[data-task-engine="sing-box"][data-task-action="restart"]').disabled,false,"安装后操作按钮未启用");
  runtime(false,"");
  await waitFor(() => document.querySelector('.service-sing-box[data-core-installed="0"]'),"卸载状态未自动刷新");

  // Core installation and version changes now live only in node settings.
  document.querySelector('[data-node-tab="cores"]').click();
  const card = () => document.querySelector(".service-sing-box");
  card().querySelector("[data-open-version-form]").click();
  card().querySelector('[name="release_channel"][value="custom"]').click();
  card().querySelector('[name="custom_version"]').value="1.14.0-draft";
  runtime(true,"1.13.1");
  await waitFor(() => card().dataset.coreInstalled==="1","节点设置安装状态未自动刷新");
  assert.match(card().querySelector('[data-core-version]').textContent,/1\.13\.1/);
  assert.equal(card().querySelector('[name="custom_version"]').value,"1.14.0-draft","安装刷新覆盖版本草稿");
  assert.equal(card().querySelector(".version-drawer").open,true,"刷新关闭版本抽屉");
  runtime(true,"1.13.2","inactive");
  await waitFor(() => card().querySelector('[data-core-version]').textContent.includes("1.13.2"),"版本切换未自动刷新");
  assert.equal(card().querySelector('[data-core-service]').textContent,"已停止");
  testAPI.agentsFailure=true;
  await delay(2200);
  assert.match(card().querySelector('[data-core-version]').textContent,/1\.13\.2/,"获取失败丢失最后状态");
  testAPI.agentsFailure=false;
  runtime(false,"");
  await waitFor(() => card().dataset.coreInstalled==="0","失败后没有恢复轮询");
  location.hash="#client-access";
  await waitFor(() => row(20001),"离开节点设置失败");
  await delay(2200);
  const before = testAPI.calls.filter((call) => call.path==="/agents").length;
  await delay(2200);
  assert.equal(testAPI.calls.filter((call) => call.path==="/agents").length,before,"离开节点设置仍后台轮询");
}

export async function testClientNodeOrderRuntime({ testAPI }) {
const cards = () => [...document.querySelectorAll(".client-access-node-card")];
  const cardNodes = () => cards().map(card => card.querySelector("[data-region-avatar]").dataset.regionAvatar).join(",");
  const sidebarNodes = () => [...document.querySelectorAll(".context-list [data-access-agent]")]
    .map(link => link.dataset.accessAgent)
    .filter(id => testAPI.clientAccessEntries.some(entry => entry.agent_id === id)).join(",");
  await waitFor(() => cards().length === 3, "client cards did not load");
  assert.equal(cardNodes(), "alpha,bravo,charlie", "default client cards must follow the API node list");
  assert.equal(cardNodes(), sidebarNodes(), "client cards and their sidebar must agree");
  assert.equal(accountStorage.getItem("qcontrolhub:node-card-order"), null, "opening clients must not freeze a custom node order");
  const alpha = cards().find(card => card.dataset.refreshKey === "client-access-node-alpha");
  assert.equal([...alpha.querySelectorAll("[data-config-engine]")].map(link => link.dataset.configEngine).join(","),
    "xray,mihomo", "sorting nodes must preserve engine order within each node");
  assert.equal([...alpha.querySelectorAll(".client-profile-row>header>b")].map(title => title.textContent).join(","),
    "alpha-20002,alpha-20001,alpha-20002,alpha-20001", "sorting nodes must preserve profile order");

  const saved = JSON.stringify(["charlie", "delta", "bravo", "alpha"]);
  accountStorage.setItem("qcontrolhub:node-card-order", saved);
  location.hash = "#node-settings";
  await waitFor(() => document.querySelectorAll(".node-card-grid>[data-agent-node]").length === 4, "node cards did not load");
  assert.equal([...document.querySelectorAll(".node-card-grid>[data-agent-node]")].map(card => card.dataset.agentNode).join(","),
    "charlie,delta,bravo,alpha", "node settings must use the saved order");
  location.hash = "#client-access";
  await waitFor(() => cardNodes() === "charlie,bravo,alpha", "client navigation did not apply the saved node order");
  assert.equal(cardNodes(), sidebarNodes(), "nodes without exports must not disrupt client order");

  document.querySelector('[data-filter-engine="xray"]').click();
  assert.equal(cardNodes(), "charlie,alpha", "engine filtering must preserve node order");
  document.querySelector('[data-access-agent="alpha"]').click();
  assert.equal(cardNodes(), "alpha", "node filtering must select the requested card");
  document.querySelector('[data-access-agent=""]').click();
  assert.equal(cardNodes(), "charlie,alpha", "clearing the node filter must restore node order");
  document.querySelector(".client-access-search-menu").open = true;
  document.querySelector('#client-search [name="q"]').value = "20001";
  document.querySelector("#client-search").requestSubmit();
  assert.equal(cardNodes(), "charlie,alpha", "profile search must preserve node order");
  assert.equal(document.querySelectorAll(".client-profile-row").length, 2, "profile search must narrow the results");
  document.querySelector("[data-clear-search]").click();
  document.querySelector('[data-filter-engine=""]').click();
  assert.equal(cardNodes(), "charlie,bravo,alpha", "clearing all filters must restore node order");
  assert.equal(accountStorage.getItem("qcontrolhub:node-card-order"), saved, "filters must not rewrite node order");

  accountStorage.setItem("qcontrolhub:node-card-order", JSON.stringify(["bravo", "alpha"]));
  testAPI.clientAccessEntries.reverse();
  document.querySelector("[data-refresh-client-access]").click();
  await waitFor(() => cardNodes() === "bravo,alpha,charlie", "refresh did not apply updated node order");
  assert.equal(cardNodes(), sidebarNodes(), "refresh must keep the cards and sidebar aligned");
}
