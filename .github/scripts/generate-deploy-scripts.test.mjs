import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  checkDeployScripts,
  deployManifest,
  renderDeployScript,
  repositoryRoot,
  validateDeployManifest,
} from "../../scripts/generate-deploy-scripts.mjs";

const generatorPath = fileURLToPath(
  new URL("../../scripts/generate-deploy-scripts.mjs", import.meta.url),
);

test("every deploy distribution script exactly matches its ordered source modules", () => {
  for (const entry of deployManifest) {
    assert.deepEqual(
      renderDeployScript(entry),
      readFileSync(resolve(repositoryRoot, entry.output)),
      `${entry.output} must be generated from its manifest modules`,
    );
  }
  assert.doesNotThrow(() => checkDeployScripts());
});

test("the generator check command accepts the checked-in distribution scripts", () => {
  const result = spawnSync(process.execPath, [generatorPath, "--check"], {
    cwd: repositoryRoot,
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout, "");
});

test("the manifest rejects a source slice that was not given an output order", () => {
  const listedModules = deployManifest.flatMap((entry) => entry.modules);
  assert.throws(
    () =>
      validateDeployManifest(deployManifest, [
        ...listedModules,
        "deploy/modules/quick-start/99-unlisted.sh",
      ]),
    /unlisted=deploy\/modules\/quick-start\/99-unlisted\.sh/,
  );
});
