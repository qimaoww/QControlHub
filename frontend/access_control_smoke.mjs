import assert from 'node:assert/strict';
import { inboundAccessState } from './analytics/access-control.js';

const agent = { status: 'online' };
const state = (engine, entry = {}, options = {}) => inboundAccessState({ agent, engine, entry, siblings: [entry], can: () => true, ...options });
for (const engine of ['xray', 'sing-box']) {
  const wireguard = { kind: 'wireguard', tag: 'wg', port: 51820 };
  assert.equal(state(engine, wireguard).sourceEditable, false, 'WireGuard must not offer ineffective public source blocking');
  assert.equal(state(engine, wireguard).destinationEditable, true, 'WireGuard destination blocking remains available');
  assert.equal(state(engine, { ...wireguard, block_mainland_source: true }).sourceEditable, true, 'operators can clear a legacy source rule');
  assert.equal(state(engine, { ...wireguard, block_mainland_source: true }, { can: () => false }).sourceEditable, false, 'readonly users cannot clear a legacy source rule');
}
assert.equal(state('xray', { kind: 'vless' }).sourceEditable, true, 'ordinary source policy remains editable');
for (const permission of ['agent-config.write', 'tasks.execute']) {
  assert.equal(state('xray', {}, { can: value => value !== permission }).editable, false, `policy requires ${permission}`);
}
assert.equal(state('xray', {}, { agent: { status: 'offline' } }).editable, false, 'offline policies cannot deploy');
assert.equal(state('ss-rust', {}, { agent: { status: 'online', shared_allocation: {} } }).editable, false, 'shared Rust policy cannot change host firewall');
assert.equal(state('ss-rust', {}, { siblings: [{}, {}] }).destinationEditable, false, 'Rust multi-port target policy must remain disabled');
assert.equal(state('ss-rust', {}, { siblings: [{}, {}] }).sourceEditable, true, 'Rust multi-port source firewall remains independently editable');
console.log('Native inbound policy permissions, tunnel boundaries and legacy rule removal passed');
