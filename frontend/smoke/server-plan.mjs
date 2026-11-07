import assert from "node:assert/strict";

import { readServerPlanInput } from "../modules/inbound-options-model.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
const previousFormData = globalThis.FormData;
const planControls = Object.fromEntries(
  Object.entries({
    operation: "modify",
    tag: "unsaved-tag",
    listen: "127.0.0.1",
    port: "24443",
    username: "unsaved-user",
    credential: "unsaved-credential",
    secondary_credential: "unsaved-secondary",
    method: "2022-blake3-aes-128-gcm",
    transport: "grpc",
    transport_path: "unsaved-service",
    tls_enabled: "1",
    certificate_path: "/unsaved/certificate.pem",
    private_key_path: "/unsaved/private-key.pem",
    reality_enabled: "0",
    reality_private_key: "",
    reality_public_key: "",
    reality_short_id: "",
    reality_server_name: "unsaved.example.test",
    reality_min_client_ver: "0.0.0",
    reality_mldsa65_seed: "",
    reality_mldsa65_verify: "",
    vless_decryption: "",
    vless_encryption: "",
    listener_routing_mark: "51820",
    listener_rule: "private-egress",
    listener_proxy: "upstream",
    snell_version: "5",
    snell_udp: "1",
    snell_reuse: "1",
    snell_obfs_mode: "shadow-tls",
    snell_obfs_host: "cover.example.test",
    snell_client_fingerprint: "chrome",
    sudoku_client_key: "client-private-key",
    sudoku_padding_min: "2",
    sudoku_padding_max: "18",
    sudoku_table_type: "prefer_entropy",
    sudoku_handshake_timeout: "15",
    sudoku_httpmask_enabled: "1",
    sudoku_httpmask_mode: "stream",
    sudoku_httpmask_tls: "1",
    sudoku_httpmask_host: "cdn.example.test:443",
    sudoku_httpmask_path_root: "qch",
    sudoku_multiplex: "on",
    target_address: "backend.example.test",
    target_port: "9443",
    network: "udp",
  }).map(([name, value]) => [name, { name, value }]),
);
const planForm = { elements: { namedItem: name => planControls[name] || null } };
globalThis.FormData = class {
  constructor(form) {
    this.form = form;
  }

  get(name) {
    return this.form.elements.namedItem(name)?.value ?? null;
  }
};

try {
  const completePresetInput = readServerPlanInput(planForm, {
    key: "sudoku",
    requires_tls: false,
  });
  assert.equal(completePresetInput.listener_routing_mark, 51820);
  assert.equal(completePresetInput.snell_obfs_mode, "shadow-tls");
  assert.equal(completePresetInput.snell_reuse, true);
  assert.equal(completePresetInput.sudoku_client_key, "client-private-key");
  assert.equal(completePresetInput.sudoku_httpmask_mode, "stream");

  const ssRustInput = readServerPlanInput({
    elements: {
      namedItem(name) {
        const values = { ss_rust_dns: "1.1.1.1", ss_rust_outbound_bind_addr: "2001:db8::1", ss_rust_ipv6_first: "1" };
        return name in values ? { value: values[name] } : null;
      },
    },
    dataset: {},
  }, { key: "ss2022" });
  assert.equal(ssRustInput.ss_rust_dns, "1.1.1.1");
  assert.equal(ssRustInput.ss_rust_outbound_bind_addr, "2001:db8::1");
  assert.equal(ssRustInput.ss_rust_ipv6_first, true);

  // Native regeneration controls, draft races, API scope and field-only
  // application are exercised in browser/preset-tls.mjs.
} finally {
  if (previousFormData === undefined) delete globalThis.FormData;
  else globalThis.FormData = previousFormData;
}

}
