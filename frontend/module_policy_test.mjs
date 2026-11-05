import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)));
const modulesDir = resolve(root, "modules");
const files = readdirSync(modulesDir).filter((name) => name.endsWith(".js")).sort();
const dependencies = new Map();
const compositionModules = new Set([
  "agents.js", "configs.js", "dashboard.js", "settings.js", "access-control.js",
  "system-bbr.js", "client-access.js", "substore-sync.js", "tasks.js",
  "core-logs.js", "client-connections.js", "traffic.js", "users.js", "ip-quality.js",
].map(name => resolve(modulesDir, name)));

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
    assert.equal(compositionModules.has(dependency), false, `${name} must depend on focused collaborators, not route composition`);
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

const reachable = new Set();
function visitFromApplication(path) {
  if (reachable.has(path)) return;
  reachable.add(path);
  for (const dependency of dependencies.get(path) || []) visitFromApplication(dependency);
}
const appPath = resolve(root, "app.js");
for (const match of readFileSync(resolve(root, "bootstrap.js"), "utf8").matchAll(/from\s*["'](\.\/modules\/[^"']+)["']/g)) {
  visitFromApplication(resolve(root, match[1]));
}
for (const match of readFileSync(appPath, "utf8").matchAll(/\b(?:from\s*|import\s*(?:\(\s*)?)["'](\.{1,2}\/[^"']+)["']/g)) {
  const dependency = resolve(dirname(appPath), match[1]);
  assert.equal(dependencies.has(dependency), true, "the application must import an existing production module");
  visitFromApplication(dependency);
}
for (const path of dependencies.keys()) {
  assert.equal(reachable.has(path), true, `${path} is not reachable from the application`);
}

console.log(`frontend/module_policy_test.mjs: ${files.length} modules satisfy the module boundary contract`);

// Independent UI can share transport and business models, never classic DOM
// renderers or the application shell, including through transitive imports.
const sharedLogic = new Set([
  "account-request.js", "account-storage.js", "agent-service-state.js",
  "agent-address-model.js", "region-model.js",
  "core-install-state.js", "core-install-source.js", "xray-api.js", "enrollment-command.js",
  "config-file-model.js", "config-field-model.js", "config-outbound-model.js", "outbound-preset-model.js",
  "code-format.js",
  "certificate-order.js", "record-display.js", "client-access-model.js", "config-snapshot.js", "dashboard-model.js",
  "errors.js", "ip-quality-model.js", "live-config-state.js", "node-order.js",
  "permissions.js", "poller.js", "preset-route.js", "routes.js",
  "session-watch.js", "substore-model.js", "system-bbr-model.js",
  "theme-registry.js", "theme-lifecycle.js", "traffic-model.js", "user-model.js",
]);
for (const name of ["agent-address-model.js", "region-model.js", "core-install-source.js", "enrollment-command.js", "config-file-model.js", "config-field-model.js", "config-outbound-model.js", "outbound-preset-model.js"]) {
  assert.doesNotMatch(readFileSync(resolve(modulesDir, name), "utf8"), /innerHTML|document\.(?:createElement|querySelector|getElementById)|\.classList\b|\.animate\(/, `${name}: shared logic must not render either theme`);
}
const analyticsRoot = resolve(root, "analytics");
const independent = new Set();
function visitIndependent(path) {
  if (independent.has(path)) return;
  independent.add(path);
  if (dirname(path) !== analyticsRoot) {
    assert.equal(dirname(path), modulesDir, `${path}: unexpected theme dependency`);
    assert.ok(sharedLogic.has(path.slice(modulesDir.length + 1)), `${path}: classic UI cannot enter the independent runtime`);
  }
  const source = readFileSync(path, "utf8");
  for (const match of source.matchAll(/\b(?:from\s*|import\s*(?:\(\s*)?)["'](\.{1,2}\/[^"']+)["']/g)) {
    visitIndependent(resolve(dirname(path), match[1]));
  }
}
visitIndependent(resolve(analyticsRoot, "app.js"));
for (const name of readdirSync(analyticsRoot).filter(name => name.endsWith(".js"))) {
  assert.ok(independent.has(resolve(analyticsRoot, name)), `${name}: unreachable independent theme module`);
}
console.log(`analytics: ${independent.size} independent UI and shared logic modules satisfy the boundary contract`);
