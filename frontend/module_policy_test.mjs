import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
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

assert.equal(existsSync(resolve(root, "app.js")), false, "the retired classic entrypoint must not return");
for (const name of ["app.css", "styles"])
  assert.equal(existsSync(resolve(root, name)), false, `retired classic styles must not return: ${name}`);
assert.equal(existsSync(resolve(modulesDir, "classic-motion.js")), false, "the retired classic lifecycle must not return");

console.log(`frontend/module_policy_test.mjs: ${files.length} modules satisfy the module boundary contract`);

// Independent UI can share transport and business models, never classic DOM
// renderers or the application shell, including through transitive imports.
const sharedLogic = new Set([
  "card-move-scroll.js",
  "core-log-preferences.js", "core-log-scope.js", "core-log-read.js", "core-log-cache.js", "core-log-model.js", "core-log-projection.js", "core-log-source-status.js", "client-connection-model.js", "client-connection-cache.js",
  "inbound-options-model.js", "archive-draft-model.js", "config-defaults.js", "config-mutations.js",
  "account-request.js", "account-storage.js", "agent-service-state.js",
  "agent-address-model.js", "agent-time-zone-model.js", "agent-batch.js", "region-model.js",
  "core-install-state.js", "core-install-source.js", "xray-api.js", "enrollment-command.js",
  "config-file-model.js", "config-field-model.js", "config-outbound-model.js", "outbound-preset-model.js", "outbound-preset-controller.js",
  "code-format.js", "core-log-selection.js",
  "certificate-order.js", "record-display.js", "client-access-model.js", "config-snapshot.js", "dashboard-model.js",
  "errors.js", "ip-quality-model.js", "ip-quality-controller.js", "live-config-state.js", "node-order.js",
  "permissions.js", "poller.js", "preset-route.js", "routes.js", "route-loader.js", "route-warmup.js",
  "session-watch.js", "substore-model.js", "system-bbr-model.js", "system-bbr-presets.js",
  "task-model.js",
  "theme-registry.js", "theme-lifecycle.js", "traffic-model.js", "shared-instance-model.js", "traffic-order.js", "user-model.js",
]);
for (const name of ["agent-address-model.js", "agent-time-zone-model.js", "agent-batch.js", "region-model.js", "core-install-source.js", "enrollment-command.js", "config-file-model.js", "config-field-model.js", "config-outbound-model.js", "outbound-preset-model.js", "config-mutations.js", "shared-instance-model.js", "traffic-order.js"]) {
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

// The production image must ship exactly the active runtime's shared graph.
// Legacy modules retained while their tests migrate cannot enter the image.
for (const match of readFileSync(resolve(root, "bootstrap.js"), "utf8").matchAll(/from\s*["'](\.\/modules\/[^"']+)["']/g)) {
  visitIndependent(resolve(root, match[1]));
}
const dockerfile = readFileSync(resolve(root, "../Dockerfile"), "utf8").replace(/\\\r?\n/g, " ");
const moduleCopies = [...dockerfile.matchAll(/^COPY ([^\n]+) \/usr\/share\/nginx\/html\/assets\/modules\/\s*$/gm)];
assert.equal(moduleCopies.length, 1, "the web image must explicitly copy its shared runtime modules");
const shipped = moduleCopies[0][1].trim().split(/\s+/).map(source => {
  assert.match(source, /^frontend\/modules\/[a-z0-9-]+\.js$/, "do not package the entire legacy module directory");
  return resolve(root, "..", source);
});
assert.equal(new Set(shipped).size, shipped.length, "duplicate shared runtime asset");
assert.deepEqual(shipped.sort(), [...independent].filter(path => dirname(path) === modulesDir).sort(),
  "the image must include every active dependency and exclude retired UI modules");

// Retained classic code is test-only while its business contracts migrate.
// Require an actual test consumer instead of pretending a dead entrypoint is
// production reachability. The native graph and image boundary stay separate.
const tested = new Set();
function visitTest(path) {
  if (tested.has(path)) return;
  tested.add(path);
  const source = readFileSync(path, "utf8");
  for (const match of source.matchAll(/\b(?:from\s*|import\s*(?:\(\s*)?)["'](\.{1,2}\/[^"']+)["']/g)) {
    visitTest(resolve(dirname(path), match[1]));
  }
}
function visitTestDirectory(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) visitTestDirectory(path);
    else if (entry.name.endsWith(".mjs")) visitTest(path);
    else if (entry.name.endsWith("_test.go")) {
      for (const match of readFileSync(path, "utf8").matchAll(/"(modules\/[a-z0-9-]+\.js)"/g)) visitTest(resolve(root, match[1]));
    }
  }
}
visitTestDirectory(root);
for (const path of dependencies.keys()) {
  assert.ok(independent.has(path) || tested.has(path), `${path} has neither an active runtime nor a retained test consumer`);
}
console.log(`legacy: ${files.filter(name => !independent.has(resolve(modulesDir, name))).length} modules retained only for migration tests`);
