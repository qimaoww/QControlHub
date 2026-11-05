import assert from "node:assert/strict";
import { createThemePreference, effectiveTheme, themeByID, loginTheme } from "./modules/theme-registry.js";

const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};
let server = { theme_id: "classic", revision: 0 };
let next = null;
let requests = [];
let current = true;
const model = createThemePreference({
  isCurrent: () => current,
  api: async (path, options) => {
    requests.push({path, options});
    if (next) { const value=next; next=null; return value.promise; }
    if (options?.method === "PUT") {
      const input = JSON.parse(options.body);
      if (input.revision !== server.revision) throw Object.assign(new Error("conflict"),{status:409});
      server = { theme_id: input.theme_id, revision: server.revision + 1 };
    }
    return { ...server };
  },
});
assert.equal(effectiveTheme("bad"),"classic");
assert.equal(effectiveTheme("analytics"),"classic", "incomplete theme is never advertised as available");
assert.equal(themeByID("bad"),null);
assert.equal(loginTheme(),"classic", "anonymous entry never reads an account preference");
assert.equal(loginTheme("unknown"),"classic");
assert.equal(loginTheme("analytics"),"classic", "unavailable runtime remains guarded at anonymous entry");
assert.equal(loginTheme("?theme=analytics"),"classic", "URLs cannot select an account theme");
await assert.rejects(model.save("classic"));
assert.deepEqual(await model.read(),server);
await assert.rejects(model.save("unknown"));
await assert.rejects(model.save("analytics"));
const saving = deferred(); next = saving;
const first = model.save("classic");
await assert.rejects(model.save("classic"),/正在保存/);
await Promise.resolve();
assert.equal(model.current.revision,0,"must not mark saved before confirmation");
saving.resolve({theme_id:"classic",revision:1});
assert.equal((await first).revision,1);
server.revision=2;
await assert.rejects(model.save("classic"),error=>error.status===409);
assert.equal(model.current.revision,1,"failure preserves last confirmed value");
await model.read();
assert.equal((await model.save("classic")).revision,3);
const old = deferred(); next=old;
const oldRead=model.read();
await model.read();
old.resolve({theme_id:"classic",revision:0});
await assert.rejects(oldRead,{name:"AbortError"});
assert.equal(model.current.revision,3);
const stale = deferred(); next=stale;
const staleRead=model.read();
model.reset(); current=false;
stale.resolve({theme_id:"analytics",revision:50});
await assert.rejects(staleRead,{name:"AbortError"});
assert.equal(model.current,null);
current=true; await model.read();
const staleSave=deferred(); next=staleSave;
const pending=model.save("classic");
await Promise.resolve(); model.reset();
staleSave.resolve({theme_id:"classic",revision:4});
await assert.rejects(pending,{name:"AbortError"});
assert.equal(model.current,null);
assert.ok(requests.every(value=>value.path==="/preferences/theme"));
console.log("Theme preference confirmation, duplicate writes, failures, conflicts and stale-session isolation passed");
