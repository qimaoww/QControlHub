import assert from "node:assert/strict";
import {presetRoute, readPresetRoute} from "./modules/preset-route.js";

const route = presetRoute({agentId:"node&special=1",engine:"sing-box",credential:"never-in-url"});
assert.deepEqual(readPresetRoute(route),{agentId:"node&special=1",engine:"sing-box"});
assert.equal(route.includes("never-in-url"),false);
assert.equal(readPresetRoute("#agent-config"),null);
assert.equal(readPresetRoute("#tasks?agent=x"),null);

// Native protocol/common-field draft scopes, baseline restoration, secret
// visibility, CAS, cancellation and account isolation run on the actual
// editors in browser/config-common.mjs and browser/presets.mjs.
console.log("Preset route scope smoke passed");
