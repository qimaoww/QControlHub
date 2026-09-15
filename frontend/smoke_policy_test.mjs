import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = dirname(fileURLToPath(import.meta.url));

function reachable(entrypoint) {
  const seen = new Set();
  function visit(path) {
    if (seen.has(path)) return;
    seen.add(path);
    const source = readFileSync(path, "utf8");
    for (const match of source.matchAll(/\b(?:from\s*|import\s*(?:\(\s*)?)["'](\.{1,2}\/[^"']+\.mjs)["']/g)) {
      const target = resolve(dirname(path), match[1]);
      assert.equal(statSync(target).isFile(), true, `missing test module: ${match[1]}`);
      visit(target);
    }
  }
  visit(resolve(root, entrypoint));
  return seen;
}

for (const [directory, entrypoint] of [
  ["smoke", "module_smoke.mjs"],
  ["browser", "agents_browser_runtime.mjs"],
]) {
  test(`${directory} modules must all be reachable from the regression runner`, () => {
    const visited = reachable(entrypoint);
    for (const name of readdirSync(resolve(root, directory))) {
      assert.match(name, /\.mjs$/, `${directory} must contain explicit test modules`);
      const path = resolve(root, directory, name);
      assert.equal(visited.has(path), true, `${directory}/${name} is not run by ${entrypoint}`);
      assert.match(readFileSync(path, "utf8"), /\bexport\s+(?:async\s+)?(?:function|const|class|\{)/, `${name} must expose an explicit test/fixture API`);
    }
  });
}
