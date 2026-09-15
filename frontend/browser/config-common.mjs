import { assert, pause, waitFor, createConfigFixture as fixture } from "./config-fixture.mjs";

export async function testCommonConfigRuntime() {
  const form = () => document.querySelector("#field-form");
  const selectField = async key => {
    const select = document.querySelector("[data-common-field]");
    assert([...select.options].some(option=>option.value === key), `common field ${key} is unavailable`);
    select.value = key;
    select.dispatchEvent(new Event("change", {bubbles:true}));
    await waitFor(()=>form() && document.querySelector("#common-options .field-editor-heading code")?.textContent === key,
      `common field ${key} did not load`);
  };
  const protectedStructure = test => {
    const saved = JSON.parse(test.saved().content);
    return JSON.stringify(Object.fromEntries(["inbounds", "outbounds", "listeners", "servers"].filter(key=>key in saved).map(key=>[key, saved[key]])));
  };
  for (const engine of ["xray", "sing-box", "mihomo", "ss-rust"]) {
    const test = await fixture(engine);
    const structure = protectedStructure(test);
    test.selectCommon();
    assert(!document.querySelector("dialog"), "selecting public config must not automatically open an editor");
    assert(document.querySelector("[data-config-operation-label]").textContent === "通用配置操作", "public selection did not change the operation menu");
    assert(!document.querySelector("[data-common-actions]").hidden &&
      document.querySelector('[data-inbound-action="modify"]').hidden, "public menu mixes common and inbound mutations");
    const add = document.querySelector('[data-inbound-action="add"]');
    assert(add && !add.hidden && !add.disabled && add.closest(".config-file-actions") &&
      !add.closest(".config-inbound-menu") && document.querySelectorAll('[data-inbound-action="add"]').length === 1,
      "add inbound must always be a single standalone source-toolbar action");
    const preview = document.querySelector("[data-config-preview]");
    if (preview) {
      assert(add.nextElementSibling === preview, "add inbound must be immediately before merged preview");
      const bounds = add.getBoundingClientRect(), previewBounds = preview.getBoundingClientRect();
      assert(bounds.right <= previewBounds.left && Math.abs(bounds.top - previewBounds.top) <= 1,
        "add inbound and merged preview must stay together on one row");
    }
    test.common("add");
    await waitFor(()=>form(), "common add form did not load");
    assert(document.querySelector("#config-inbound-title").textContent === "增加通用配置项", "common dialog title is incorrect");
    assert(form().elements.mutation.type === "hidden" && form().elements.mutation.value === "add", "common add mutation is not locked");
    let choices = [...document.querySelector("[data-common-field]").options].map(option=>option.value);
    assert(choices.join(",") === `dns,${test.extraField}`, "add choices contain existing or inbound-scoped fields");
    assert(!document.querySelector("dialog #server-plan-form, dialog #inbound-options, dialog .field-rail, dialog .config-source-studio"),
      "common editor contains unrelated inbound forms or a second source editor");
    assert(!test.calls.some(call=>call.path.endsWith("/plans")), "common editor generated unused inbound credentials");
    if (innerWidth <= 600) {
      const dialog = document.querySelector("dialog"), bounds = dialog.getBoundingClientRect();
      const field = form().elements.fragment.getBoundingClientRect();
      assert(dialog.scrollWidth <= dialog.clientWidth + 1 && field.left >= bounds.left && field.right <= bounds.right,
        "mobile common editor overflows its dialog");
      assert(form().querySelector("[data-field-intent=validate]").getBoundingClientRect().height >= 40,
        "mobile common actions are too small");
    }
    form().elements.fragment.value = '{"servers":["1.1.1.1"]}';
    await selectField(test.extraField);
    form().elements.fragment.value = '{"draft":true}';
    await selectField("dns");
    assert(form().elements.fragment.value === '{"servers":["1.1.1.1"]}', "switching common fields discarded the local draft");
    document.querySelector("[data-inbound-close]").click(); await pause();
    assert(document.querySelector("dialog[open]") && form().elements.fragment.value.includes("1.1.1.1"),
      "canceling common editor close discarded its draft");
    test.confirm = true;
    let release;
    test.gate = new Promise(resolve=>{release=resolve;});
    const submit = form().querySelector("[data-field-intent=validate]");
    submit.click(); submit.click(); await pause();
    assert(submit.disabled && document.querySelector("[data-common-field]").disabled &&
      form().getAttribute("aria-busy") === "true", "common save did not lock form and field switching");
    document.querySelector("[data-inbound-close]").click(); await pause();
    assert(document.querySelector("dialog[open]"), "common dialog closed during a pending write");
    release();
    await waitFor(()=>!document.querySelector("dialog"), "common add did not return to the source editor");
    test.gate = null;
    assert(test.writes.length === 1 && test.writes[0].path.endsWith("/fields/dns") &&
      test.writes[0].mutation === "add" && test.writes[0].expected_version === 1 &&
      !Object.hasOwn(test.writes[0], "install_if_missing"), "common add used wrong field, revision, or installation flow");
    assert(protectedStructure(test) === structure && JSON.parse(test.saved().content).dns.servers[0] === "1.1.1.1",
      "adding a common field altered inbounds or their paired exits");
    assert(document.querySelector("[data-config-operation-label]").textContent === "通用配置操作",
      "common save changed the current file selection");
    assert(test.confirmations.some(item=>item.title === "存在其他未保存修改"), "saving one common field silently discarded other field drafts");

    test.select("first");
    assert(document.querySelector("[data-common-actions]").hidden && !document.querySelector('[data-inbound-action="modify"]').hidden,
      "selecting an inbound did not restore inbound actions");
    assert(!document.querySelector('[data-inbound-action="add"]').hidden && !document.querySelector('[data-inbound-action="add"]').disabled,
      "selecting an existing inbound hid the standalone add action");
    test.common("delete"); await pause();
    assert(!document.querySelector("dialog"), "hidden common action can mutate the current inbound selection");
    const menu = document.querySelector(".config-inbound-menu");
    menu.querySelector("summary").focus();
    menu.dispatchEvent(new KeyboardEvent("keydown", {key:"ArrowDown", bubbles:true}));
    assert(document.activeElement.dataset.inboundAction === "modify", "keyboard menu navigation focused a hidden common action");
    menu.querySelector("summary").focus();
    menu.dispatchEvent(new KeyboardEvent("keydown", {key:"ArrowUp", bubbles:true}));
    assert(document.activeElement.dataset.inboundAction === "delete", "menu ArrowUp did not enter at the last visible item");
    menu.open = false;
    if (["xray", "sing-box"].includes(engine)) {
      document.querySelector("[data-config-preview]").click();
      assert(document.querySelector("[data-common-actions]").hidden &&
        document.querySelector('[data-common-action="modify"]').disabled, "merged preview is treated as common configuration");
      assert(!document.querySelector('[data-inbound-action="add"]').hidden, "merged preview hid the standalone add action");
      document.querySelector("[data-config-preview]").click();
    }
    test.selectCommon();
    assert(!document.querySelector("dialog"), "returning to common source opened a modal");
    const source = document.querySelector("[data-code-input]"), baseline = source.value;
    source.value += "\n "; source.dispatchEvent(new Event("input"));
    test.common("modify"); await pause();
    assert(!document.querySelector("dialog") && test.notices.some(message=>message.includes("未保存修改")),
      "common mutation ignored an unsaved source draft");
    source.value = baseline; source.dispatchEvent(new Event("input"));
    test.common("modify");
    await waitFor(()=>form(), "common modify form did not load");
    await selectField("dns");
    choices = [...document.querySelector("[data-common-field]").options].map(option=>option.value);
    assert(choices.includes(test.primaryField) && choices.includes(test.secondaryField) &&
      choices.includes("dns") && !choices.includes(test.extraField), "modify choices do not reflect saved field presence");
    assert(form().elements.fragment.value.includes("1.1.1.1") && form().elements.mutation.value === "modify",
      "modify did not load the saved field value and locked operation");
    form().elements.fragment.value = '{"servers":["9.9.9.9"]}';
    form().elements.mutation.value = "delete";
    form().querySelector("[data-field-intent=validate]").click(); await pause();
    assert(test.writes.length === 1 && test.notices.some(message=>message.includes("操作已变化")),
      "tampering with a hidden mutation escaped the common operation");
    form().elements.mutation.value = "modify";
    form().querySelector("[data-field-intent=validate]").click();
    await waitFor(()=>!document.querySelector("dialog"), "common modify did not finish");
    assert(test.writes.length === 2 && test.writes[1].mutation === "modify" &&
      test.writes[1].expected_version === 2 && protectedStructure(test) === structure, "common modify changed another target");
    const beforeDelete = JSON.parse(test.saved().content);
    test.common("delete");
    await waitFor(()=>form(), "common delete form did not load");
    await selectField("dns");
    assert(form().elements.fragment.readOnly && form().elements.fragment.value.includes("9.9.9.9") &&
      form().elements.mutation.value === "delete", "common deletion must show the selected saved value read-only");
    test.confirm = false;
    form().querySelector("[data-field-intent=deploy]").click(); await pause();
    assert(test.writes.length === 2 && document.querySelector("dialog[open]"), "canceling common deletion wrote data");
    assert(test.confirmations.at(-1).message.includes("dns") &&
      test.confirmations.at(-1).message.includes("重启内核"), "common delete confirmation omitted field identity or deploy impact");
    test.confirm = true;
    form().querySelector("[data-field-intent=validate]").click();
    await waitFor(()=>!document.querySelector("dialog"), "common delete did not finish");
    const afterDelete = JSON.parse(test.saved().content);
    delete beforeDelete.dns;
    assert(test.writes.length === 3 && test.writes[2].mutation === "delete" &&
      test.writes[2].expected_version === 3 && test.writes[2].fragment === "" &&
      JSON.stringify(afterDelete) === JSON.stringify(beforeDelete), "common deletion removed more than the selected field");
    test.common("add");
    await waitFor(()=>form(), "deleted common field cannot be added again");
    assert([...document.querySelector("[data-common-field]").options].some(option=>option.value === "dns"),
      "deleted common field remained in the wrong operation list");
    test.dispose();
  }
  for (const options of [{missing:true}, {emptyInbounds:true}]) {
    const test = await fixture("xray", options);
    const add = document.querySelector('[data-inbound-action="add"]');
    assert(add && !add.hidden && !add.disabled && add.closest(".config-file-actions") &&
      !add.closest(".config-inbound-menu"), "empty workspace lost the standalone add-inbound action");
    test.click("add");
    await waitFor(()=>document.querySelector("#server-plan-form"), "first inbound editor did not open");
    document.querySelector("[data-plan-intent=validate]").click();
    await waitFor(()=>test.writes.length === 1 && !document.querySelector("dialog"), "first inbound did not save");
    test.selectCommon();
    assert(!document.querySelector('[data-inbound-action="add"]').hidden &&
      !document.querySelector('.config-inbound-menu [data-inbound-action="add"]'),
      "creating the first inbound hid the persistent action or moved it into the common menu");
    test.dispose();
  }
  const nativeCommon = await fixture("mihomo", {native:true});
  assert(!document.querySelector('[data-inbound-action="add"]').hidden &&
    !document.querySelector('.config-inbound-menu [data-inbound-action="add"]'),
    "native non-preset listeners changed the standalone add action");
  nativeCommon.dispose();
  for (const options of [{readonly:true}, {noTasks:true}, {import:true}]) {
    const test = await fixture("xray", options);
    assert(!document.querySelector('[data-inbound-action="add"]').hidden, "permission or source state should disable, not hide, the add action");
    for (const operation of ["add", "modify", "delete"]) {
      test.common(operation); await pause();
      assert(!document.querySelector("dialog") && !test.writes.length,
        "read-only, denied, imported or drifted source permits common mutation");
    }
    test.dispose();
  }
  for (const options of [{missing:true}, {missing:true, savedMissing:true}, {offline:true}]) {
    const test = await fixture("ss-rust", options);
    test.common("add");
    if (options.missing && !options.savedMissing) {
      assert(!document.querySelector("dialog") && document.querySelector('[data-common-action="add"]').disabled,
        "common fields can be submitted before a configuration exists");
    } else {
      await waitFor(()=>form(), "unavailable core should still allow common field drafts");
      assert(form().querySelector("[data-field-intent=validate]").disabled &&
        form().querySelector("[data-field-intent=deploy]").disabled, "common field save bypassed runtime availability");
    }
    assert(!test.writes.length, "common configuration unexpectedly installed a missing core");
    test.dispose();
  }
  for (const options of [{noCommon:true}, {allCommon:true}]) {
    const test = await fixture("xray", options);
    test.common(options.noCommon ? "delete" : "add");
    await waitFor(()=>document.querySelector("#common-options"), "empty common choices did not finish loading");
    assert(!form() && document.querySelector("#common-options").textContent.includes(options.noCommon ? "尚无可操作" : "没有可增加"),
      "empty common choices exposed an accidental save target");
    test.dispose();
  }
  for (const kind of ["workspace", "field"]) {
    const test = await fixture("xray");
    if (kind === "workspace") test.stale = true;
    else test.staleField = true;
    test.common("modify");
    await waitFor(()=>document.querySelector("dialog")?.textContent.includes("配置版本已变化"), "common version conflict was not surfaced");
    assert(!form() && !test.writes.length, "common editor allows writes from a stale workspace/field read");
    test.dispose();
  }
  const isolated = await fixture("xray");
  isolated.common("modify");
  await waitFor(()=>form(), "common draft isolation form missing");
  form().elements.fragment.value = '{"level":"draft-only"}';
  await isolated.reenter();
  isolated.common("delete");
  await waitFor(()=>form(), "common deletion did not reopen after navigation");
  assert(form().elements.mutation.value === "delete" && form().elements.fragment.value === '{"level":"info"}',
    "a modify draft leaked into the delete operation");
  isolated.dispose();
  const conflict = await fixture("xray");
  conflict.common("modify");
  await waitFor(()=>form(), "common conflict form missing");
  form().elements.fragment.value = '{"level":"keep-this-draft"}';
  conflict.fail = true;
  form().querySelector("[data-field-intent=validate]").click();
  await waitFor(()=>document.querySelector("dialog [data-preset-status]")?.textContent.includes("草稿已保留"),
    "common save conflict did not preserve the draft");
  assert(form().elements.fragment.value.includes("keep-this-draft") && form().querySelector("[data-field-intent=validate]").disabled,
    "common conflict allowed stale resubmission or discarded inputs");
  conflict.dispose();
  const late = await fixture("xray");
  let release;
  late.fieldGate = new Promise(resolve=>{release=resolve;});
  late.common("modify");
  await waitFor(()=>document.querySelector("[data-common-field]"), "deferred common read did not mount its selector");
  late.dispose();
  release(); await pause();
  assert(!document.querySelector("dialog") && !late.writes.length, "late common field response escaped its disposed session");
}
