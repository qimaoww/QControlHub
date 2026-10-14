import assert from "node:assert/strict";
import test from "node:test";
import { requireSuccess } from "./require-ci-success.mjs";
const jobs = ["debian", "alpine", "upgrade-sandbox", "core-permissions", "build-images"];
const successful = () => Object.fromEntries(jobs.map(name => [name, { result: "success" }]));
test("required image checks accept only a complete successful graph", () => {
  requireSuccess(successful());
  for (const name of jobs) for (const result of ["failure", "cancelled", "skipped", undefined]) {
    const needs = successful();
    if (result) needs[name].result = result;
    else delete needs[name];
    assert.throws(() => requireSuccess(needs), new RegExp(name));
  }
});
