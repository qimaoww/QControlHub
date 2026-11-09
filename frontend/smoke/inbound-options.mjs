import assert from 'node:assert/strict';
import { readXHTTPOptions, readMieruPattern } from '../modules/inbound-options-model.js';

export function run() {
const form = { dataset: {} };
const values = new Map(Object.entries({ xhttp_split: '1', xhttp_download_address: '2001:db8::2', xhttp_download_port: '443',
  xhttp_download_sni: 'download.example', xhttp_download_host: 'download.example', xhttp_download_alpn: 'http/1.1',
  xhttp_extra: '{"xmux":{"maxConcurrency":0,"maxConnections":2}}', transport_path: '/split', client_address: '2001:db8::1', client_port: '8443' }));
const tls = { key: 'vless-xhttp-tls', transports: ['xhttp'] };
const split = readXHTTPOptions(form, tls, values), extra = JSON.parse(split.xhttp_extra);
assert.equal(extra.downloadSettings.address, '2001:db8::2');
assert.equal(extra.downloadSettings.port, 443);
assert.equal(extra.downloadSettings.tlsSettings.fingerprint, 'unsafe');
assert.equal(extra.downloadSettings.tlsSettings.allowInsecure, false);
assert.equal(extra.downloadSettings.xhttpSettings.path, '/split');
assert.equal(extra.xmux.maxConnections, 2);
assert.equal(split.client_address, '2001:db8::1');
values.set('xhttp_extra', split.xhttp_extra); values.delete('xhttp_split');
assert.ok(JSON.parse(readXHTTPOptions(form, tls, values).xhttp_extra).downloadSettings, 'untouched raw JSON retains download settings');
form.dataset.xhttpSplitChanged = 'true';
assert.equal(JSON.parse(readXHTTPOptions(form, tls, values).xhttp_extra).downloadSettings, undefined);
values.set('xhttp_extra', '[]');
assert.throws(() => readXHTTPOptions(form, tls, values), /JSON 对象/);

const reality = {key:'vless-xhttp-reality',transports:['xhttp']};
values.set('xhttp_extra', '{"downloadSettings":{"realitySettings":{"mldsa65Verify":"old-key"}}}');
values.set('xhttp_split','1');
values.set('reality_mldsa65_verify','new-key');
assert.equal(JSON.parse(readXHTTPOptions(form,reality,values).xhttp_extra).downloadSettings.realitySettings.mldsa65Verify,'new-key','download retains a stale ML-DSA key');
values.set('reality_mldsa65_verify','');
assert.equal(JSON.parse(readXHTTPOptions(form,reality,values).xhttp_extra).downloadSettings.realitySettings.mldsa65Verify,undefined,'clearing ML-DSA retains download verification');

const controls = new Map(Object.entries({ mieru_traffic_pattern: { value: 'custom' },
  mieru_custom_tcp_fragment: { checked: false }, mieru_custom_max_sleep_ms: { value: '8' },
  mieru_custom_nonce_type: { value: 'FIXED' }, mieru_custom_nonce_min_len: { value: '6' }, mieru_custom_nonce_max_len: { value: '8' },
  mieru_custom_nonce_fixed_hex: { value: 'aabb' }, mieru_custom_nonce_apply_to_all_udp: { checked: true },
  mieru_custom_padding_middle: { value: '64' }, mieru_custom_padding_end: { value: '128' },
  mieru_custom_low_entropy_mode: { value: '0' }, mieru_custom_mask_rotation: { value: 'right_7' } }));
const mieru = readMieruPattern({ elements: { namedItem: name => controls.get(name) } }, { key: 'mieru' });
assert.equal(mieru.max_sleep_ms, 0); assert.equal(mieru.nonce_min_len, 0); assert.equal(mieru.nonce_max_len, 0);
assert.equal(mieru.nonce_fixed_hex, 'aabb'); assert.equal(mieru.mask_rotation, 'none');
controls.get('mieru_traffic_pattern').value = 'off';
assert.equal(readMieruPattern({ elements: { namedItem: name => controls.get(name) } }, { key: 'mieru' }), undefined);
console.log('XHTTP split/raw settings and Mieru structured option serialization passed');

}
