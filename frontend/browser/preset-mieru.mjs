import { assert, waitFor, createConfigFixture } from "./config-fixture.mjs";

// Preserve the complete custom-pattern workflow on the actual analytics page.
export async function testMieruRoundTripRuntime() {
  for (const shared of [false, true]) await checkMieru(shared);
}

async function checkMieru(shared) {
  const fixture = await createConfigFixture("mihomo", {protocol:"mieru", shared});
  fixture.confirm = true;
  fixture.taskStatus = "succeeded";
  fixture.select("second");
  const form = () => document.querySelector("dialog[open] form:has([name=protocol])");
  const control = name => form().elements.namedItem(name);
  const submissions = () => fixture.writes.filter(write=>write.path.endsWith("/server-inbounds")).map(body=>({body}));
  const edit = (name, value) => {
    control(name).value = value;
    control(name).dispatchEvent(new Event("input", {bubbles:true}));
    control(name).dispatchEvent(new Event("change", {bubbles:true}));
  };
  const open = async () => {
    fixture.click("modify");
    await waitFor(()=>form()?.elements.namedItem("mieru_traffic_pattern"),"Mieru native form did not open");
  };
  const save = async () => {
    const count=fixture.writes.length+1;
    fixture.submit("deploy");
    await fixture.settle(count);
    await open();
  };
  try {
    await open();
    await save();
    assert(control("mieru_transport").value === "TCP", "Mieru default transport was lost");
    assert(control("mieru_traffic_pattern").value === "off" && submissions()[0].body.input.mieru_traffic_pattern === "off", "Mieru default traffic pattern was lost");
    assert([...control("mieru_traffic_pattern").options].map(option => option.value).join(",") === "off,light,balanced,aggressive,custom", "Mieru traffic modes changed");
    const customFields = () => control("mieru_custom_nonce_type").closest("details");
    assert(customFields().hidden, "Mieru custom controls appear when mode is off");
    assert(control("credential").type === "password", "Mieru password is not masked");
    edit("mieru_transport", "UDP");
    edit("username", "mieru-user");
    edit("listener_routing_mark", "42");
    edit("listener_rule", "test-rule");
    edit("listener_proxy", "test-proxy");
    for (const preset of ["light", "balanced", "aggressive", "off"]) {
      edit("mieru_traffic_pattern", preset);
      await save();
      const saved = submissions().at(-1).body.input;
      assert(saved.mieru_traffic_pattern === preset && control("mieru_traffic_pattern").value === preset, `Mieru ${preset} preset was lost during save/reopen`);
      assert(saved.mieru_transport === "UDP" && saved.username === "mieru-user", "Mieru form lost transport or username");
      assert(control("mieru_transport").value === "UDP", "Mieru saved UDP transport did not reopen");
      for (const [name, value] of [["listener_routing_mark", 42], ["listener_rule", "test-rule"], ["listener_proxy", "test-proxy"]]) {
        assert(saved[name] === value && control(name).value === String(value), `Mieru lost ${name} during save/reopen`);
      }
    }
    edit("mieru_traffic_pattern", "custom");
    assert(!customFields().hidden && control("mieru_custom_max_sleep_ms").value === "8" && control("mieru_custom_low_entropy_mode").value === "56", "Mieru custom defaults did not open from balanced values");
    control("mieru_custom_tcp_fragment").checked = false;
    control("mieru_custom_tcp_fragment").dispatchEvent(new Event("input", { bubbles: true }));
    assert(control("mieru_custom_max_sleep_ms").closest(".a-field").hidden, "Mieru disabled fragment still shows sleep setting");
    edit("mieru_custom_max_sleep_ms", "13");
    control("mieru_custom_tcp_fragment").checked = true;
    control("mieru_custom_tcp_fragment").dispatchEvent(new Event("input", { bubbles: true }));
    assert(!control("mieru_custom_max_sleep_ms").closest(".a-field").hidden && control("mieru_custom_max_sleep_ms").value === "13", "Mieru fragment setting was lost when toggled");
    edit("mieru_custom_nonce_min_len", "3");
    edit("mieru_custom_nonce_max_len", "7");
    edit("mieru_custom_nonce_type", "FIXED");
    assert(control("mieru_custom_nonce_min_len").closest(".a-field").hidden && !control("mieru_custom_nonce_fixed_hex").closest(".a-field").hidden, "Mieru fixed nonce fields are wrong");
    edit("mieru_custom_nonce_fixed_hex", "00010203, a1b2c3d4");
    control("mieru_custom_nonce_apply_to_all_udp").checked = false;
    edit("mieru_custom_padding_middle", "99");
    edit("mieru_custom_padding_end", "222");
    edit("mieru_custom_low_entropy_mode", "40");
    edit("mieru_custom_mask_rotation", "left_3");
    edit("mieru_traffic_pattern", "light");
    assert(customFields().hidden, "Mieru custom controls remain visible for light mode");
    edit("mieru_traffic_pattern", "custom");
    assert(!customFields().hidden && control("mieru_custom_nonce_fixed_hex").value === "00010203, a1b2c3d4" && control("mieru_custom_mask_rotation").value === "left_3", "Mieru custom fields were lost when switching modes");
    assert(form().checkValidity(), "Mieru custom form is invalid");
    await save();
    const custom = submissions().at(-1).body.input.mieru_custom_pattern;
    assert(submissions().at(-1).body.input.mieru_traffic_pattern === "custom" && JSON.stringify(custom) === JSON.stringify({
      tcp_fragment: true, max_sleep_ms: 13, nonce_type: "FIXED", nonce_apply_to_all_udp: false,
      nonce_min_len: 0, nonce_max_len: 0, nonce_fixed_hex: "00010203, a1b2c3d4",
      padding_middle: 99, padding_end: 222, low_entropy_mode: 40, mask_rotation: "left_3",
    }), "Mieru custom parameters were not submitted");
    assert(control("mieru_traffic_pattern").value === "custom" && control("mieru_custom_mask_rotation").value === "left_3" && !customFields().hidden, "Mieru custom settings did not reopen after save");
    edit("mieru_custom_nonce_type", "PRINTABLE_SUBSET");
    edit("mieru_custom_nonce_min_len", "3");
    edit("mieru_custom_nonce_max_len", "7");
    edit("mieru_custom_nonce_fixed_hex", "invalid");
    assert(form().checkValidity(), "Mieru hidden fixed nonce blocks printable mode");
    await save();
    const printable = submissions().at(-1).body.input.mieru_custom_pattern;
    assert(printable.nonce_type === "PRINTABLE_SUBSET" && printable.nonce_min_len === 3 && printable.nonce_max_len === 7 && printable.nonce_fixed_hex === "", "Mieru printable nonce settings were not submitted");
    assert(control("mieru_custom_nonce_min_len").value === "3" && control("mieru_custom_nonce_max_len").value === "7", "Mieru printable nonce range did not reopen");
    assert(!control("mieru_custom_nonce_min_len").disabled && !control("mieru_custom_nonce_max_len").disabled, "Mieru custom fields stayed disabled after reopening on another tab");
    const beforeInvalid = submissions().length;
    edit("mieru_custom_nonce_min_len", "8");
    assert(!form().checkValidity() && control("mieru_custom_nonce_max_len").validationMessage.includes("最长长度"), "Mieru reversed nonce range was not marked on the field");
    customFields().open = false;
    fixture.submit("deploy");
    assert(submissions().length === beforeInvalid && control("mieru_custom_nonce_max_len").closest("details").open &&
      document.activeElement === control("mieru_custom_nonce_max_len"), "Mieru reversed nonce range submitted or stayed hidden");
    edit("mieru_custom_nonce_min_len", "3");
    assert(form().checkValidity(), "Mieru corrected nonce range stayed invalid");
    edit("mieru_custom_nonce_type", "FIXED");
    for (const badHex of ["", "a", "aabb,", "aabb, zz", "aa".repeat(13)]) {
      edit("mieru_custom_nonce_fixed_hex", badHex);
      assert(!form().checkValidity() && control("mieru_custom_nonce_fixed_hex").validationMessage, `Mieru invalid fixed nonce ${badHex} was accepted`);
    }
    fixture.submit("deploy");
    assert(submissions().length === beforeInvalid && document.activeElement === control("mieru_custom_nonce_fixed_hex"), "Mieru invalid fixed nonce submitted or was not focused");
    edit("mieru_custom_nonce_fixed_hex", "aa, " + "bb".repeat(12));
    assert(form().checkValidity(), "Mieru valid fixed nonce did not clear the error");
    edit("mieru_custom_nonce_type", "PRINTABLE_SUBSET");
    assert(form().checkValidity() && !control("mieru_custom_nonce_fixed_hex").validationMessage, "Mieru hidden fixed nonce error was not cleared");
    edit("mieru_custom_nonce_min_len", "99");
    assert(!form().checkValidity(), "Mieru invalid visible nonce length was accepted");
    edit("mieru_traffic_pattern", "off");
    assert(form().checkValidity(), "Mieru hidden custom value blocks off mode");
  } finally { fixture.dispose(); }
}
