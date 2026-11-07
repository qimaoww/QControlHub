import assert from "node:assert/strict";
import { bytes, actionName } from "./analytics/operations.js";

const normalize = value => value.replaceAll("\u00a0", " ");
for (const [value, expected] of [[0,"0 B"],[1024,"1 KB"],[1024 ** 2,"1 MB"],[-1,"0 B"],[null,"0 B"]])
  assert.equal(normalize(bytes(value)), expected);
assert.equal(actionName("read-managed-config"), "读取托管配置");
assert.equal(actionName("configure-tcp"), "配置 TCP");
assert.equal(actionName("new_action"), "new_action");
// Native appearance/date/account-reset cases live in shell_modules_smoke.
// DOM text escaping and service badges are exercised on actual native nodes;
// chart history/legend tests use the real SVG in node-task-lifecycle modes.
console.log("Native display scalar smoke passed");
