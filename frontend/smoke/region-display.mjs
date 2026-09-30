import assert from "node:assert/strict";
import { createRegionDisplay } from "../modules/regions.js";

function regionCard() {
  const avatar = {
    dataset: {},
    isConnected: true,
    children: [],
    classList: { toggle() {}, remove() {} },
    hasAttribute() { return false; },
    setAttribute() {},
    replaceChildren() { this.children = []; },
    append(child) { child.parentNode = this; this.children.push(child); },
    get firstChild() { return this.children[0]; },
    set textContent(value) { this.children = [{ textContent: value }]; },
  };
  return {
    avatar,
    querySelector(selector) {
      return selector === "[data-region-avatar]" ? avatar : null;
    },
  };
}

// Run sequentially with the other DOM suites; importing installs no globals.
export async function run() {
  const previousDocument = globalThis.document;
  globalThis.document = {
    createElement(tag) {
      assert.equal(tag, "img");
      return { addEventListener() {} };
    },
  };
  try {
    const requests = [];
    const state = { data: {} };
    const display = createRegionDisplay({
      state,
      can: () => true,
      api: async (path) => {
        requests.push(path);
        return { country_code: "US" };
      },
    });

    // A bounded list can return unresolved countries while lookups still run.
    // Rendering the fleet must not open another provider request per card.
    const agents = Array.from({ length: 40 }, (_, index) => ({
      id: `node-${index}`,
      region_code: "",
      metrics: { public_ipv4: `8.8.8.${index + 1}` },
    }));
    const cards = agents.map(() => regionCard());
    agents.forEach((agent, index) => display(agent, cards[index]));
    assert.equal(requests.length, 0, "empty list country fields never trigger per-node fallbacks");
    assert.equal(state.data.agentRegions[agents[0].id].ready, true, "an empty list country is a completed display result");
    display(agents[0], cards[0]);
    const rebuilt = regionCard();
    display(agents[0], rebuilt);
    assert.equal(requests.length, 0, "reconciled and rebuilt unresolved cards reuse the list result");

    // A subsequent list uses the warmed provider cache and updates the avatar.
    display({ ...agents[0], region_code: "US" }, cards[0]);
    assert.equal(cards[0].avatar.dataset.regionCode, "US");
    assert.equal(cards[0].avatar.firstChild.src, "/api/v1/region-flags/us", "a later inline result replaces the unknown avatar");
    assert.equal(requests.length, 0);

    // Manual preferences keep priority over a conflicting automatic result.
    display({ ...agents[0], region_code: "US", labels: { region_code: "JP" } }, cards[0]);
    assert.equal(cards[0].avatar.dataset.regionCode, "JP");
    assert.equal(cards[0].avatar.firstChild.src, "/api/v1/region-flags/jp");
    assert.equal(requests.length, 0);

    // Older control planes omit the field entirely and still need the endpoint.
    const legacy = { id: "legacy", metrics: { public_ipv4: "9.9.9.9" } };
    const legacyCard = regionCard();
    display(legacy, legacyCard);
    await Promise.resolve();
    assert.deepEqual(requests, ["/agents/legacy/region"]);
    assert.equal(legacyCard.avatar.dataset.regionCode, "US");
    display(legacy, legacyCard);
    assert.equal(requests.length, 1, "a legacy result is reused on later renders");

    // Missing and present-but-empty fields must not share a display identity.
    display({ ...legacy, region_code: "" }, legacyCard);
    assert.equal(legacyCard.avatar.dataset.regionCode, "", "a current empty list result replaces the legacy endpoint result");
    assert.equal(requests.length, 1);
    display(legacy, legacyCard);
    await Promise.resolve();
    assert.equal(requests.length, 2, "a missing field retains the legacy fallback after an empty list result");
    assert.equal(legacyCard.avatar.dataset.regionCode, "US");
  } finally {
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
}
