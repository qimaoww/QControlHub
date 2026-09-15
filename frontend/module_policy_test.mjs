import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)));
const modulesDir = resolve(root, "modules");
const files = readdirSync(modulesDir).filter((name) => name.endsWith(".js")).sort();
const dependencies = new Map();
const compositionModules = new Set(["agents.js", "configs.js"].map(name => resolve(modulesDir, name)));

assert.ok(files.length > 0, "frontend/modules must contain ES modules");
for (const name of files) {
  const path = resolve(modulesDir, name);
  assert.equal(statSync(path).isFile(), true, `${name} must be a regular file`);
  const source = readFileSync(path, "utf8");
  assert.match(source, /\bexport\s+(?:async\s+)?(?:function|const|let|var|class|\{)/, `${name} must expose a named module API`);
  assert.doesNotMatch(source, /(?:from|import\s*\()\s*["']\.\.\/app\.js["']/, `${name} must not depend on the application entrypoint`);
  const imports = [...source.matchAll(/\b(?:from\s*|import\s*(?:\(\s*)?)["'](\.{1,2}\/[^"']+)["']/g)]
    .map(match => resolve(dirname(path), match[1]));
  for (const dependency of imports) {
    assert.equal(statSync(dependency).isFile(), true, `${name} imports a missing module`);
    assert.notEqual(dependency, resolve(root, "app.js"), `${name} must not import the application entrypoint`);
    assert.equal(compositionModules.has(dependency), false, `${name} must depend on focused collaborators, not Agent/configuration route composition`);
  }
  dependencies.set(path, imports);
}

const visited = new Set(), active = new Set();
function visit(path, chain = []) {
  assert.equal(active.has(path), false, `frontend module dependency cycle: ${[...chain, path].join(" -> ")}`);
  if (visited.has(path)) return;
  active.add(path);
  for (const dependency of dependencies.get(path) || []) visit(dependency, [...chain, path]);
  active.delete(path);
  visited.add(path);
}
for (const path of dependencies.keys()) visit(path);

console.log(`frontend/module_policy_test.mjs: ${files.length} modules satisfy the module boundary contract`);
