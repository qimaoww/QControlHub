import assert from "node:assert/strict";
import { coreSourceForInstall, developmentSourceVisible } from "../modules/core-install-source.js";
import { coreSourceLabel, coreSourceName } from "../modules/task-model.js";

// Runtime transitions and discovery run on the native page in agent-detail-actions.mjs.
export async function run() {
assert.equal(developmentSourceVisible("mihomo", "development"), true, "mihomo development shows source choice");
assert.equal(developmentSourceVisible("mihomo", "stable"), false, "stable hides source choice");
assert.equal(developmentSourceVisible("xray", "development"), false, "non-mihomo hides source choice");
assert.equal(coreSourceForInstall("mihomo", "development", "mirror"), "mirror", "mirror carries through");
assert.equal(coreSourceForInstall("mihomo", "development", ""), "official", "omitted source defaults to official");
assert.equal(coreSourceForInstall("mihomo", "stable", "mirror"), undefined, "stable omits source");
assert.equal(coreSourceForInstall("xray", "development", "mirror"), undefined, "non-mihomo omits source");
assert.equal(coreSourceName("mirror"), "vernesong/mihomo 镜像（第三方）", "mirror audited label");
assert.equal(coreSourceName("official"), "MetaCubeX/mihomo 官方", "official audited label");
assert.equal(coreSourceName(""), "", "no source has no label");
assert.equal(coreSourceLabel("mihomo", "development", ""), "MetaCubeX/mihomo 官方", "omitted mihomo development audits as official");
assert.equal(coreSourceLabel("mihomo", "development", "official"), "MetaCubeX/mihomo 官方", "explicit official audits as official");
assert.equal(coreSourceLabel("mihomo", "development", "mirror"), "vernesong/mihomo 镜像（第三方）", "mirror audits as third party");
assert.equal(coreSourceLabel("mihomo", "stable", ""), "", "stable has no source label");
assert.equal(coreSourceLabel("xray", "development", ""), "", "non-mihomo has no source label");

}
