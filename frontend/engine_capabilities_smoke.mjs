import assert from "node:assert/strict";
import { engineCapabilityToggles, selectedDefaultEngines } from "./modules/engine-capabilities.js";

const defaults = engineCapabilityToggles([]);
assert.equal((defaults.match(/type="checkbox"/g) || []).length, 4);
assert.equal(defaults.includes("checked"), false);
assert.equal(defaults.includes("disabled"), false, "globally disabled engines remain selectable");
const node = engineCapabilityToggles(["mihomo"], { supported: ["mihomo", "xray"], node: true });
assert.match(node, /data-engine-capability="xray"[^>]*>/);
assert.doesNotMatch(node, /data-engine-capability="xray"[^>]*disabled/);
assert.match(node, /data-engine-capability="sing-box"[^>]*disabled/);
assert.equal((engineCapabilityToggles([], { writable: false }).match(/disabled/g) || []).length, 4);
const form = new FormData();
assert.deepEqual(selectedDefaultEngines(form), []);
form.append("default_agent_engines", "xray");
form.append("default_agent_engines", "ss-rust");
assert.deepEqual(selectedDefaultEngines(form), ["xray", "ss-rust"]);
console.log("Engine capability module smoke passed");
