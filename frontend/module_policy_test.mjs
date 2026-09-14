import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)));
const modulesDir = resolve(root, "modules");
const files = readdirSync(modulesDir).filter((name) => name.endsWith(".js")).sort();

assert.ok(files.length > 0, "frontend/modules must contain ES modules");
for (const name of files) {
  const path = resolve(modulesDir, name);
  assert.equal(statSync(path).isFile(), true, `${name} must be a regular file`);
  const source = readFileSync(path, "utf8");
  assert.match(source, /\bexport\s+(?:async\s+)?(?:function|const|let|var|class|\{)/, `${name} must expose a named module API`);
  assert.doesNotMatch(source, /(?:from|import\s*\()\s*["']\.\.\/app\.js["']/, `${name} must not depend on the application entrypoint`);
}

console.log(`frontend/module_policy_test.mjs: ${files.length} modules satisfy the module boundary contract`);
