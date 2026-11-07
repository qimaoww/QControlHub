import assert from "node:assert/strict";

// Page factories from the retired shell have no runtime consumers. Verify the
// shipped native route entries instead; the route import smoke additionally
// rejects eager DOM, storage and request work before a page is called.
export async function run() {
  for (const [path, names] of [
    ['nodes', ['nodesPage']], ['client-access', ['clientAccessPage']],
    ['configs', ['archivePage']], ['live-config', ['liveConfigPage']],
    ['operations', ['dashboardPage', 'tasksPage']], ['settings', ['settingsPage']],
    ['traffic', ['trafficPage']],
  ]) {
    const module = await import(`../analytics/${path}.js`);
    for (const name of names) assert.equal(typeof module[name], 'function', `${path}.${name} native page entry missing`);
  }
}
