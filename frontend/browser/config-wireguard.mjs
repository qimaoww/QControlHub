import { assert, waitFor, createConfigFixture } from "./config-fixture.mjs";

export async function testWireGuardConfigRuntime() {
  for (const engine of ["xray", "sing-box"]) {
    const fixture = await createConfigFixture(engine, {protocol:"wireguard"});
    const controller = document.querySelector("[data-code-editor]").configFileController;
    const expected = engine === "sing-box" ? "endpoints/second.json" : "inbounds/second.json";
    assert(controller.paths().includes(expected), "WireGuard has no selectable source file");
    fixture.select("second");
    assert(controller.selectedInbound()?.tag === "second", "endpoint selection lost its native identity");
    fixture.click("modify");
    await waitFor(() => document.querySelector("#server-plan-form"), "WireGuard editor did not open");
    let form = document.querySelector("#server-plan-form");
    assert(form.elements.wireguard_keepalive.value === "0", "disabled keepalive became 25 on edit");
    assert(form.elements.wireguard_client_private_key.type === "password" &&
      form.elements.wireguard_server_private_key.type === "password", "private keys are visible by default");
    assert(!form.elements.wireguard_server_public_key.readOnly &&
      !form.elements.wireguard_client_public_key.readOnly, "custom private key has no matching public-key input");
    assert(Boolean(form.elements.wireguard_server_address) === (engine === "sing-box"), "Xray exposes an ignored server address");
    assert(form.elements.listen.readOnly === (engine === "sing-box"), "endpoint pretends to support a custom listen address");
    assert(!form.elements.wireguard_preshared_key.required, "optional PSK became mandatory");
    const clientKey = form.elements.wireguard_client_private_key.value;
    const port = form.elements.port.value;
    const serverKey = form.elements.wireguard_server_private_key.value;
    form.elements.wireguard_server_private_key.value = "edited";
    form.elements.wireguard_server_public_key.value = "edited";
    form.querySelector('[data-regenerate="wireguard_server_private_key,wireguard_server_public_key"]').click();
    await waitFor(() => form.elements.wireguard_server_private_key.value === serverKey, "server key pair regeneration failed");
    assert(form.elements.wireguard_server_public_key.value !== "edited" &&
      form.elements.wireguard_client_private_key.value === clientKey && form.elements.port.value === port,
    "server key generation changed client identity or listener port");
    form.elements.tag.value = "wg-renamed";
    form.querySelector("[data-plan-intent=validate]").click();
    await waitFor(() => fixture.writes.length === 1 && !document.querySelector("dialog"), "WireGuard save failed");
    assert(fixture.writes[0].original_tag === "second" && fixture.writes[0].input.wireguard_keepalive === 0 &&
      fixture.writes[0].input.wireguard_client_private_key === clientKey, "WireGuard request lost identity or metadata");
    fixture.select("wg-renamed");
    fixture.click("delete");
    await waitFor(() => document.querySelector("[data-delete-intent=validate]"), "endpoint delete confirmation missing");
    document.querySelector("[data-delete-intent=validate]").click();
    await waitFor(() => fixture.writes.length === 2 && !document.querySelector("dialog"), "endpoint deletion failed");
    assert(fixture.writes[1].original_tag === "wg-renamed", "endpoint deletion targeted another file");
    fixture.click("add");
    await waitFor(() => document.querySelector("[data-preset-protocol]"), "protocol chooser missing");
    const selector = document.querySelector("[data-preset-protocol]");
    assert(![...selector.options].some(option => ["tailscale", "openvpn-server"].includes(option.value)),
      "incomplete endpoint lifecycle is offered as a preset");
    if (selector.value !== "wireguard") {
      const previous = document.querySelector("#server-plan-form");
      selector.value = "wireguard";
      selector.dispatchEvent(new Event("change", {bubbles:true}));
      await waitFor(() => document.querySelector("#server-plan-form") !== previous, "protocol form did not refresh");
    }
    await waitFor(() => document.querySelector("[data-wireguard-options]"), "WireGuard add form missing");
    form = document.querySelector("#server-plan-form");
    form.querySelector("[data-plan-intent=validate]").click();
    await waitFor(() => fixture.writes.length === 3 && !document.querySelector("dialog"), "new WireGuard preset did not save");
    assert(fixture.writes[2].operation === "add" && fixture.writes[2].input.protocol === "wireguard", "new endpoint used the wrong protocol");
    fixture.dispose();
  }
}
