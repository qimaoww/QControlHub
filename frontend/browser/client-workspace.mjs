import { assert, delay, waitFor } from "./assertions.mjs";
import { copyClientValue } from "../modules/client-clipboard.js";

export async function testClientWorkspace({ testAPI }) {
  const query = selector => document.querySelector(selector);
  const refresh = async () => {
    query("[data-refresh-client-access]").click();
    await waitFor(() => !query("[data-refresh-client-access]").disabled, "refresh did not settle");
  };
  const format = value => {
    const select = query("[data-client-display-format]");
    select.value = value;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  };
  await refresh();
  const first = testAPI.clientAccessEntries[0].profiles[0];
  const initialURI = first.profile.uri;
  let copied = "";
  const clipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: async value => { copied = value; } } });
  try {
    const preview = query(".client-export-preview");
    assert.equal(preview.open, false, "exports should start collapsed");
    query(".client-copy").click();
    await waitFor(() => copied === first.profile.uri, "collapsed URL copy lost data");
    assert.equal(preview.open, false, "copy should not expand configuration");
    assert.equal(query(".client-export-control input").type, "password", "copy revealed the secret");
    format("mihomo");
    query(".client-copy").click();
    await waitFor(() => copied === first.mihomo_yaml, "format change copied stale URL");
    await delay(1700);
    assert.equal(query(".client-copy").textContent, "复制 Mihomo YAML", "feedback reset lost the selected format");
    // Long multiline YAML stays bounded and copies completely, including on the legacy path.
    const saved = first.mihomo_yaml;
    first.mihomo_yaml = `name: ${"long-name-".repeat(80)}\n${"password: example-only\n".repeat(70)}`;
    await refresh();
    query(".client-copy").click();
    await waitFor(() => copied === first.mihomo_yaml, "multiline copy was truncated");
    const details = query(".client-export-preview");
    details.open = true;
    const textarea = query(".client-export-control textarea");
    query(".client-export-control [data-secret-visibility]").click();
    assert.equal(textarea.classList.contains("is-masked"), false, "YAML could not be revealed");
    assert.ok(textarea.getBoundingClientRect().height <= 240, "YAML expands beyond its reading area");
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "long YAML overflowed the viewport");
    details.open = false;
    await waitFor(() => textarea.classList.contains("is-masked"), "closing preview must mask it");
    let legacyValue = "";
    const originalCommand = document.execCommand;
    document.execCommand = () => { legacyValue = document.activeElement.value; return true; };
    const copyButton = query(".client-copy");
    copyButton.focus();
    try {
      await copyClientValue(textarea, { navigatorObject: {} });
      assert.equal(legacyValue, first.mihomo_yaml, "legacy copy cannot select a collapsed export");
      assert.equal(document.activeElement, copyButton, "legacy copy lost keyboard focus");
    } finally { document.execCommand = originalCommand; }
    first.mihomo_yaml = saved;
    // A delayed clipboard response must not re-enable an unavailable format.
    let releaseCopy;
    navigator.clipboard.writeText = () => new Promise(resolve => { releaseCopy = resolve; });
    await refresh();
    query(".client-copy").click();
    first.profile = { ...first.profile, uri: "", format: "URL" };
    await refresh();
    format("url");
    releaseCopy();
    await delay();
    assert.equal(query(".client-copy").disabled, true, "late copy re-enabled unavailable export");
    first.profile = { ...first.profile, uri: initialURI };
    format("mihomo");
    navigator.clipboard.writeText = async () => { throw new Error("clipboard denied"); };
    await refresh();
    query(".client-copy").click();
    await waitFor(() => document.body.textContent.includes("复制失败"), "clipboard rejection had no feedback");
  } finally {
    if (clipboard) Object.defineProperty(navigator, "clipboard", clipboard);
    else delete navigator.clipboard;
  }
  query("[data-client-parameter-open]").click();
  const dialog = query(".client-parameter-dialog[open]");
  const secret = dialog.querySelector(".secret-value-control input");
  assert.equal(secret.type, "password", "parameter secret should start masked");
  dialog.querySelector("[data-secret-visibility]").click();
  assert.equal(secret.type, "text", "parameter reveal failed");
  dialog.querySelector("[data-client-parameter-close]").click();
  await waitFor(() => !dialog.open, "parameter dialog did not close");
  query("[data-client-parameter-open]").click();
  assert.equal(secret.type, "password", "reopening retained a revealed secret");
  dialog.querySelector("[data-client-parameter-close]").click();
  await waitFor(() => !dialog.open, "parameter dialog did not close");

  // Search a port that is not part of the inbound name, then a per-profile IPv6 address.
  for (const term of ["21026", "2001:db8::21"]) {
    query('#client-search [name="q"]').value = term;
    query("#client-search").requestSubmit();
    assert.equal(document.querySelectorAll(".client-profile-row").length, 1, `search did not match ${term}`);
    query("[data-clear-search]").click();
  }
  query('[data-access-agent="sg-04"]').click();
  query('[data-filter-engine="sing-box"]').click();
  query('[data-access-agent="lax-02"]').click();
  assert.ok(query('[data-filter-engine=""]').hasAttribute("aria-current"), "node change retained an unavailable engine");
  assert.match(query(".client-access-scope h1").textContent, /LAX-02/, "selected scope not shown");
  query('[data-access-agent=""]').click();

  const search = query('#client-search [name="q"]');
  search.focus();
  search.value = "unsaved query";
  search.setSelectionRange(3, 7);
  testAPI.clientAccessFailure = true;
  await refresh();
  assert.equal(document.querySelectorAll(".client-access-node-card").length, 8, "refresh failure discarded usable exports");
  assert.match(query("[data-client-load-status]").textContent, /刷新失败/, "refresh error must remain visible");
  assert.equal(document.activeElement, search, "refresh failure lost focus");
  assert.equal(search.value, "unsaved query", "refresh discarded search draft");
  assert.equal(search.selectionStart, 3, "refresh moved caret");
  testAPI.clientAccessFailure = false;
  await refresh();
  assert.equal(query("[data-client-load-status]").hidden, true, "successful retry retained error");
  assert.equal(query("[data-client-display-format]").value, "mihomo", "refresh changed format");
  search.value = search.defaultValue;
  search.blur();
  const scroller = query(".workspace-main");
  scroller.scrollTop = 200;
  await delay(200);
  const scroll = scroller.scrollTop;
  await refresh();
  assert.ok(Math.abs(scroller.scrollTop - scroll) <= 2, `refresh lost reading position: ${scroll} -> ${scroller.scrollTop}`);

  // Dense nodes exercise stable profile identities and keyboard order after refresh.
  const entry = testAPI.clientAccessEntries[0];
  const savedProfiles = entry.profiles;
  entry.profiles = Array.from({ length: 60 }, (_, index) => ({ ...savedProfiles[0], tag: `dense-${index}`, port: 30000 + index, client_name: index === 0 ? "long-inbound-".repeat(50) : `Dense ${index}` }));
  await refresh();
  query(`[data-access-agent="${entry.agent_id}"]`).click();
  assert.equal(document.querySelectorAll(".client-profile-row").length, 60, "dense node lost inbounds");
  assert.equal(new Set([...document.querySelectorAll(".client-export-control input")].map(input => input.id)).size, 60, "dense inbounds reused IDs");
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "long names overflow the dense node");
  entry.profiles = savedProfiles;
  await refresh();
  query('[data-access-agent=""]').click();

  const savedEntries = testAPI.clientAccessEntries;
  testAPI.clientAccessEntries = [];
  await refresh();
  assert.match(query(".client-access-empty-state").textContent, /尚未生成客户端配置/, "empty fleet has no next step");
  testAPI.clientAccessEntries = savedEntries;
  await refresh();
  query('[data-access-agent="jp-08"]').click();
  assert.match(query(".client-access-entry-empty").textContent, /公网地址.*刷新/, "missing address has no recovery instruction");
  query('[data-access-agent=""]').click();
  format("url");
  for (const theme of ["dark", "light"]) {
    document.documentElement.dataset.theme = theme;
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, `${theme} viewport overflows`);
    for (const card of document.querySelectorAll(".client-access-node-card")) {
      const flag = card.querySelector("[data-region-avatar]").getBoundingClientRect();
      const title = card.querySelector(".node-card-title").getBoundingClientRect();
      assert.ok(title.left - flag.right >= 10, "flag is too close to node text");
    }
  }
}
