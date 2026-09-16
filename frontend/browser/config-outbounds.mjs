import { assert, pause, waitFor, createConfigFixture as fixture } from "./config-fixture.mjs";
import { testOutboundPresetsRuntime } from "./config-outbound-presets.mjs";

export async function testConfigOutboundsRuntime() {
  const action = kind => document.querySelector(`[data-outbound-action="${kind}"]`);
  const input = () => document.querySelector('textarea[aria-label="出站配置 JSON"]');
  const save = () => document.querySelector('dialog [data-intent="validate"]').click();
  const open = async kind => {
    action(kind).click();
    await waitFor(input, `${kind} outbound editor missing`);
  };
  const changeInput = value => {
    const mode = document.querySelector("[data-outbound-mode]");
    if (mode && mode.value !== "json") { mode.value = "json"; mode.dispatchEvent(new Event("change")); }
    input().value = value;
    input().dispatchEvent(new Event("input"));
  };
  const close = async () => {
    document.querySelector("[data-outbound-close]").click();
    await pause();
  };
  for (const {engine, protocol} of [
    {engine:"xray"}, {engine:"sing-box"},
    {engine:"xray", protocol:"wireguard"}, {engine:"sing-box", protocol:"wireguard"},
  ]) {
    const test = await fixture(engine, {drift:true, protocol});
    const section = engine === "sing-box" && protocol === "wireguard" ? "endpoints" : "inbounds";
    const originalListeners = JSON.parse(test.saved().content)[section];
    const menu = action("add").closest("details");
    assert(menu.previousElementSibling.querySelector("[data-inbound-action]"), "outbound menu is not next to inbound operations");
    assert(["add", "bind", "modify", "delete"].every(kind=>action(kind).disabled), "outbound operation has no inbound scope");
    test.select("first");
    assert(!action("add").disabled && !action("bind").disabled && action("modify").disabled && action("delete").disabled,
      "unbound inbound must allow creation/binding, not editing another exit");
    const inboundMenu = menu.previousElementSibling;
    inboundMenu.querySelector("summary").click();
    menu.querySelector("summary").click();
    assert(!inboundMenu.open && menu.open && menu.querySelector("summary").getAttribute("aria-expanded") === "true",
      "opening outbound menu did not close inbound menu");
    const menuBounds = menu.querySelector('[role="menu"]').getBoundingClientRect();
    assert(menuBounds.left >= 0 && menuBounds.right <= innerWidth, "outbound menu exceeds viewport");
    menu.querySelector("summary").focus();
    menu.dispatchEvent(new KeyboardEvent("keydown", {key:"ArrowDown", bubbles:true}));
    assert(document.activeElement === action("add"), "outbound keyboard entry differs from inbound menu");
    menu.dispatchEvent(new KeyboardEvent("keydown", {key:"End", bubbles:true}));
    assert(document.activeElement === action("bind"), "keyboard focused disabled outbound action");
    menu.dispatchEvent(new KeyboardEvent("keydown", {key:"Escape", bubbles:true}));
    assert(!menu.open && document.activeElement === menu.querySelector("summary"), "Escape did not restore outbound menu focus");

    let release;
    test.workspaceGate = new Promise(resolve=>{release=resolve;});
    action("add").click();
    assert(document.querySelector('dialog[open] [role="status"]')?.textContent.includes("正在读取"), "outbound click gives no immediate loading feedback");
    await close();
    release(); test.workspaceGate = null; await pause();
    assert(!document.querySelector("dialog"), "late read reopened a canceled outbound dialog");
    await open("add");
    assert(document.querySelector("[data-bound-inbound]").textContent.includes("first") &&
      document.querySelector("[data-outbound-mark]").textContent.includes("0x51435209"), "binding identity or required mark missing");
    if (innerWidth <= 600) {
      const dialog = document.querySelector("dialog"), rect = dialog.getBoundingClientRect();
      assert(dialog.scrollWidth <= dialog.clientWidth + 1 && rect.left >= 0 && rect.right <= innerWidth,
        "mobile outbound dialog overflows");
      assert(document.querySelector('[data-intent="deploy"]').getBoundingClientRect().height >= 40, "outbound touch actions too small");
    }
    changeInput(input().value.replace("exit-21001", "exit-a"));
    assert(test.pages.configHasUnsavedChanges(), "outbound draft not protected during navigation");
    await close();
    assert(input()?.value.includes("exit-a"), "canceling close lost outbound draft");
    test.gate = new Promise(resolve=>{release=resolve;});
    save(); save(); await pause();
    assert(document.querySelector("dialog form").getAttribute("aria-busy") === "true" &&
      document.querySelector("[data-outbound-close]").disabled && input().disabled &&
      document.querySelector("[data-outbound-status]").textContent.includes("正在保存"), "pending outbound save has no consistent lock/status");
    document.querySelector("dialog").dispatchEvent(new Event("cancel", {cancelable:true}));
    assert(document.querySelector("dialog[open]"), "Escape closed an uncertain outbound write");
    release();
    await waitFor(()=>test.writes.length === 1 && !document.querySelector("dialog"), "outbound creation failed");
    test.gate = null;
    const routeKey = engine === "xray" ? "routing" : "route", targetKey = engine === "xray" ? "outboundTag" : "outbound";
    const binding = () => JSON.parse(test.saved().content)[routeKey].rules.find(rule=>rule[targetKey] === "exit-a");
    assert(test.writes[0].path.endsWith("/source") && binding() && !action("modify").disabled,
      "atomic outbound save lost binding or selected inbound");
    await open("modify");
    changeInput(input().value.replace("exit-a", "exit-b"));
    save();
    await waitFor(()=>test.writes.length === 2 && !document.querySelector("dialog"), "bound rename failed");
    assert(!binding() && JSON.parse(test.saved().content)[routeKey].rules.some(rule=>rule[targetKey] === "exit-b"),
      "renaming did not update inbound route");
    await open("bind");
    assert(input().readOnly, "binding an existing template must show a read-only preview");
    const select = document.querySelector('dialog select');
    select.value = "0"; select.dispatchEvent(new Event("change"));
    assert(input().value.includes("direct") && test.pages.configHasUnsavedChanges(), "existing outbound selection did not update/protect its preview");
    save(); await pause();
    assert(test.writes.length === 2 && document.querySelector("dialog"), "canceled rebinding wrote data");
    test.confirm = true;
    save();
    await waitFor(()=>test.writes.length === 3 && !document.querySelector("dialog"), "existing binding failed");
    await open("modify");
    save();
    await waitFor(()=>document.querySelector("[data-outbound-error]")?.textContent.includes("共享出口"), "default/shared exit was not protected");
    assert(test.writes.length === 3, "inbound edit altered global default");
    await close();
    await open("bind");
    const rebound = document.querySelector('dialog select');
    rebound.value = [...rebound.options].find(option=>option.textContent === "exit-b").value;
    rebound.dispatchEvent(new Event("change"));
    save();
    await waitFor(()=>test.writes.length === 4 && !document.querySelector("dialog"), "rebinding previous template failed");
    await open("delete");
    assert(input().readOnly && input().value.includes("exit-b") &&
      document.querySelector('[data-intent="deploy"]').classList.contains("danger"), "deletion lost readonly preview or danger style");
    test.confirm = false;
    save(); await pause();
    assert(test.writes.length === 4 && document.querySelector("dialog"), "canceled deletion wrote data");
    test.confirm = true;
    save();
    await waitFor(()=>test.writes.length === 5 && !document.querySelector("dialog"), "bound deletion failed");
    const final = JSON.parse(test.saved().content);
    assert(!final.outbounds.some(entry=>entry.tag === "exit-b") && !final[routeKey].rules.length &&
      JSON.stringify(final[section]) === JSON.stringify(originalListeners) && action("modify").disabled,
    "deletion left a binding or changed another inbound/endpoint");
    test.dispose();
  }
  for (const status of [400, 409, 503, undefined]) {
    const test = await fixture("xray");
    test.select("first");
    await open("add");
    changeInput(input().value.replace("exit-21001", "retained-draft"));
    test.sourceFailure = {status};
    save();
    await waitFor(()=>!document.querySelector("[data-outbound-error]").hidden, "save error not visible");
    assert(input().value.includes("retained-draft") && !input().disabled && !test.writes.length, "save failure lost draft or left inputs locked");
    assert(document.querySelector('[data-intent="validate"]').disabled === (status !== 400), "uncertain/conflicting writes allow duplicate submission");
    test.confirm = true;
    await close();
    test.dispose();
  }
  const late = await fixture("xray");
  late.select("first");
  let release;
  late.workspaceGate = new Promise(resolve=>{release=resolve;});
  action("add").click();
  late.dispose();
  release(); await pause();
  assert(!document.querySelector("dialog") && !late.writes.length, "outbound read escaped its route");
  for (const options of [{readonly:true}, {noTasks:true}, {import:true}]) {
    const test = await fixture("xray", options);
    test.select("first");
    assert(["add", "bind", "modify", "delete"].every(kind=>action(kind).disabled), "outbound actions bypass permission/source gates");
    test.dispose();
  }
  await testOutboundPresetsRuntime();
}
