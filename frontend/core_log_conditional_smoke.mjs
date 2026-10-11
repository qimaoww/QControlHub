import assert from "node:assert/strict";
import { readCoreLogWindow } from "./modules/core-log-read.js";
import { requestJSON } from "./modules/errors.js";

const params = new URLSearchParams("agent_id=alpha&limit=2000");
const options = { signal: new AbortController().signal, preview: false, engines: ["xray"] };
let payload = [{ id: 1, agent_id: "alpha", engine: "xray", message: "initial", level: "info" }];
let tag = 'W/"first"', parses = 0, current = true, forcedStatus = 0;
const requests = [];
const api = (path, init) => requestJSON(path, init, {
  isCurrent: () => current,
  fetchImpl: async (_path, request) => {
    requests.push(request);
    assert.equal("conditional" in request, false, "transport-only options never reach fetch");
    const status = forcedStatus || (request.headers?.["If-None-Match"] === tag ? 304 : 200);
    return {
      status, ok: status === 200, headers: new Headers({ ETag: tag }),
      json: async () => {
        parses++;
        assert.notEqual(status, 304, "a conditional hit must never parse an empty body");
        return structuredClone(payload);
      },
    };
  },
});
const first = await readCoreLogWindow(api, params, options);
assert.equal(requests.at(-1).headers?.["If-None-Match"], undefined);
const same = await readCoreLogWindow(api, params, { ...options, previous: first });
assert.equal(same, first, "unchanged polls preserve the complete searchable window by identity");
assert.equal(parses, 1, "unchanged polls skip JSON parsing");
assert.equal(requests.at(-1).headers["If-None-Match"], tag);

payload[0].message = "corrected under the same ID";
tag = 'W/"second"';
const changed = await readCoreLogWindow(api, params, { ...options, previous: same });
assert.notEqual(changed, first);
assert.equal(changed[0].message, payload[0].message);
assert.equal(await readCoreLogWindow(api, params, { ...options, previous: changed }), changed);
assert.equal(parses, 2, "the validator follows the reused snapshot after a content change");

await readCoreLogWindow(api, new URLSearchParams("agent_id=alpha&limit=1000"), { ...options, previous: changed });
assert.equal(requests.at(-1).headers?.["If-None-Match"], undefined, "related limits cannot validate one another");
await readCoreLogWindow(api, new URLSearchParams("agent_id=beta&limit=2000"), { ...options, previous: changed });
assert.equal(requests.at(-1).headers?.["If-None-Match"], undefined, "validators cannot cross node scopes");
await readCoreLogWindow(api, params, options);
assert.equal(requests.at(-1).headers?.["If-None-Match"], undefined, "evicted or new-account windows need a body");

for (const status of [403, 503]) {
  forcedStatus = status;
  await assert.rejects(readCoreLogWindow(api, params, { ...options, previous: changed }), error => error.status === status);
}
forcedStatus = 0;
assert.equal(await readCoreLogWindow(api, params, { ...options, previous: changed }), changed,
  "a transient failure retains a validator for recovery");
current = false;
await assert.rejects(readCoreLogWindow(api, params, { ...options, previous: changed }), error => error.name === "AbortError",
  "a bodyless response cannot restore a previous account's snapshot");
current = true;
forcedStatus = 304;
await assert.rejects(readCoreLogWindow(api, params, options), error => error.status === 304,
  "an unsolicited 304 cannot turn a missing window into valid data");
console.log("Core log conditional transport smoke passed");
