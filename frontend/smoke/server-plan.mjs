import assert from "node:assert/strict";

import { bindServerPlanRegeneration, readServerPlanInput } from "../modules/configs.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
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
const planFormListeners = new Map();
const planForm = {
  isConnected: true,
  elements: {
    namedItem(name) {
      return planControls[name] || null;
    },
  },
  addEventListener(type, listener) {
    const listeners = planFormListeners.get(type) || [];
    listeners.push(listener);
    planFormListeners.set(type, listeners);
  },
  async dispatch(type) {
    await Promise.all(
      (planFormListeners.get(type) || []).map((listener) =>
        listener({ currentTarget: this }),
      ),
    );
  },
};
const fakePlanButton = (textContent, dataset) => {
  const listeners = [];
  const attributes = new Map();
  return {
    attributes,
    dataset,
    disabled: false,
    textContent,
    addEventListener(type, listener) {
      if (type === "click") listeners.push(listener);
    },
    setAttribute(name, value) {
      attributes.set(name, value);
    },
    removeAttribute(name) {
      attributes.delete(name);
    },
    dispatchClick() {
      return Promise.all(
        listeners.map((listener) =>
          listener({ currentTarget: this, preventDefault: noop }),
        ),
      );
    },
  };
};
const planButton = fakePlanButton("重新生成参数", { regenerate: "all" });
const credentialPlanButton = fakePlanButton("生成", {
  regenerate: "credential",
  regenerateSuccess: "凭据已生成",
});
const realityKeyPlanButton = fakePlanButton("生成密钥对", {
  regenerate: "reality_private_key,reality_public_key",
  regenerateSuccess: "Reality 密钥对已生成",
});
const mldsaPlanButton = fakePlanButton("生成密钥对", {
  regenerate: "reality_mldsa65_seed",
  regenerateSuccess: "ML-DSA-65 密钥对已生成",
});
const deferredPlan = () => {
  let resolve;
  let reject;
  const promise = new Promise((accept, fail) => {
    resolve = accept;
    reject = fail;
  });
  return { promise, resolve, reject };
};
const planRequests = [];
const planNotifications = [];
const appliedPlans = [];
let canRegenerate = true;
const regenerationBusy = [];
const pageState = {
  route: "#agent-config",
  node: "node-current",
  engine: "ss-rust",
  tab: "transport",
  expanded: true,
  scrollY: 720,
};
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

  bindServerPlanRegeneration({
    form: planForm,
    buttons: [planButton, credentialPlanButton, realityKeyPlanButton, mldsaPlanButton],
    api: async (path, options) => {
      const pending = deferredPlan();
      planRequests.push({ path, options, pending });
      return pending.promise;
    },
    base: "/agents/node-current/configs/ss-rust",
    protocol: {
      key: "ss2022",
      requires_tls: false,
      uses_reality: false,
    },
    report: (...message) => planNotifications.push(message),
    onApplied: (plan) => appliedPlans.push(plan),
    canApply: () => canRegenerate,
    onBusy: busy => regenerationBusy.push(busy),
  });

  const firstClick = planButton.dispatchClick();
  assert.equal(planButton.disabled, true, "regeneration disables its button");
  assert.equal(credentialPlanButton.disabled, true);
  assert.equal(planButton.textContent, "生成中…");
  assert.equal(planButton.attributes.get("aria-busy"), "true");
  assert.equal(planRequests.length, 1);
  assert.equal(
    planRequests[0].path,
    "/agents/node-current/configs/ss-rust/plans",
    "regeneration stays scoped to the selected node and engine",
  );
  const firstPayload = JSON.parse(planRequests[0].options.body);
  assert.equal(
    firstPayload.input.method,
    "2022-blake3-aes-128-gcm",
    "regeneration uses the current unsaved method",
  );
  assert.equal(firstPayload.input.transport, "grpc");
  assert.equal(firstPayload.input.listen, "127.0.0.1");
  assert.equal(firstPayload.input.certificate_path, "/unsaved/certificate.pem");
  assert.equal(
    firstPayload.input.reality_min_client_ver,
    "0.0.0",
    "the editable minClientVer preserves the legacy preset default",
  );
  assert.equal(firstPayload.input.target_address, "backend.example.test");
  assert.equal(firstPayload.input.target_port, 9443);
  assert.equal(firstPayload.input.network, "udp");
  planRequests[0].pending.resolve({
    tag: "regenerated-tag",
    port: 35555,
    username: "regenerated-user",
    credential: "regenerated-credential",
    secondary_credential: "regenerated-secondary",
    transport_path: "regenerated-service",
    method: "server-default-method",
    transport: "websocket",
    listen: "0.0.0.0",
    certificate_path: "/server/default.pem",
    target_address: "127.0.0.1",
    target_port: 80,
    network: "tcp",
  });
  await firstClick;
  assert.equal(planControls.tag.value, "regenerated-tag");
  assert.equal(planControls.port.value, "35555");
  assert.equal(planControls.credential.value, "regenerated-credential");
  assert.equal(
    planControls.method.value,
    "2022-blake3-aes-128-gcm",
    "local updates preserve current selections",
  );
  assert.equal(planControls.transport.value, "grpc");
  assert.equal(planControls.listen.value, "127.0.0.1");
  assert.equal(planControls.operation.value, "modify");
  assert.equal(
    planControls.target_address.value,
    "backend.example.test",
    "regeneration preserves the selected forwarding target",
  );
  assert.equal(planControls.target_port.value, "9443");
  assert.equal(planControls.network.value, "udp");
  assert.deepEqual(pageState, {
    route: "#agent-config",
    node: "node-current",
    engine: "ss-rust",
    tab: "transport",
    expanded: true,
    scrollY: 720,
  });
  assert.equal(appliedPlans.length, 1);
  assert.equal(planButton.disabled, false);
  assert.equal(planButton.textContent, "重新生成参数");
  assert.equal(planButton.attributes.has("aria-busy"), false);

  const fullTag = planControls.tag.value;
  const fullPort = planControls.port.value;
  const credentialClick = credentialPlanButton.dispatchClick();
  assert.equal(planButton.disabled, true);
  assert.equal(credentialPlanButton.textContent, "生成中…");
  planRequests[1].pending.resolve({
    tag: "must-not-apply",
    port: 38888,
    credential: "credential-only-result",
  });
  await credentialClick;
  assert.equal(
    planControls.credential.value,
    "credential-only-result",
    "a field action updates its requested result",
  );
  assert.equal(planControls.tag.value, fullTag);
  assert.equal(planControls.port.value, fullPort);
  assert.deepEqual(planNotifications.at(-1), ["凭据已生成"]);
  assert.equal(credentialPlanButton.textContent, "生成");

  const previousShortID = planControls.reality_short_id.value;
  const realityKeyClick = realityKeyPlanButton.dispatchClick();
  planRequests[2].pending.resolve({
    reality_private_key: "regenerated-private-key",
    reality_public_key: "regenerated-public-key",
    reality_short_id: "must-not-apply",
  });
  await realityKeyClick;
  assert.equal(
    planControls.reality_private_key.value,
    "regenerated-private-key",
  );
  assert.equal(planControls.reality_public_key.value, "regenerated-public-key");
  assert.equal(
    planControls.reality_short_id.value,
    previousShortID,
    "Reality key generation updates the pair without changing Short ID",
  );

  const failedCredential = planControls.credential.value;
  const failedClick = planButton.dispatchClick();
  planRequests[3].pending.reject(new Error("generation unavailable"));
  await failedClick;
  assert.equal(
    planControls.credential.value,
    failedCredential,
    "a failed request preserves the current form",
  );
  assert.deepEqual(planNotifications.at(-1), [
    "生成参数失败：generation unavailable",
    "error",
  ]);
  assert.equal(planButton.disabled, false);

  const olderClick = planButton.dispatchClick();
  planControls.method.value = "2022-blake3-aes-256-gcm";
  await planForm.dispatch("change");
  const newerClick = planButton.dispatchClick();
  assert.equal(planRequests.length, 6, "rapid clicks can be ordered safely");
  assert.equal(
    JSON.parse(planRequests[5].options.body).input.method,
    "2022-blake3-aes-256-gcm",
    "the newer request captures the newer unsaved selection",
  );
  planRequests[5].pending.resolve({
    tag: "newer-tag",
    port: 36666,
    credential: "newer-credential",
  });
  await newerClick;
  assert.equal(planControls.credential.value, "newer-credential");
  assert.equal(
    planButton.disabled,
    true,
    "the button stays disabled until every pending request settles",
  );
  planRequests[4].pending.resolve({
    tag: "older-tag",
    port: 37777,
    credential: "older-credential",
  });
  await olderClick;
  assert.equal(
    planControls.credential.value,
    "newer-credential",
    "an out-of-order response cannot overwrite the newer result",
  );
  assert.equal(planControls.method.value, "2022-blake3-aes-256-gcm");
  assert.equal(planButton.disabled, false);

  const mldsaClick = mldsaPlanButton.dispatchClick();
  planRequests[6].pending.resolve({
    reality_mldsa65_seed: "generated-mldsa-seed",
    reality_mldsa65_verify: "server-derived-verify",
  });
  await mldsaClick;
  assert.equal(
    planControls.reality_mldsa65_seed.value,
    "generated-mldsa-seed",
    "ML-DSA generation applies only the persisted server seed",
  );
  assert.equal(
    planControls.reality_mldsa65_verify.value,
    "",
    "the UI does not retain a stale client verify value",
  );
  assert.deepEqual(planNotifications.at(-1), ["ML-DSA-65 密钥对已生成"]);
  canRegenerate = false;
  await planButton.dispatchClick();
  assert.equal(planRequests.length, 7, "saving/navigation guard must suppress generation requests");
  canRegenerate = true;
  const guardedClick = planButton.dispatchClick();
  assert.equal(regenerationBusy.at(-1), true, "generation did not lock saving");
  canRegenerate = false;
  const guardedCredential = planControls.credential.value;
  planRequests[7].pending.resolve({credential:"must-not-apply-after-save-or-navigation"});
  await guardedClick;
  assert.equal(planControls.credential.value, guardedCredential, "late result crossed save/navigation boundary");
  assert.equal(regenerationBusy.at(-1), false, "generation did not release saving lock");
} finally {
  if (previousFormData === undefined) delete globalThis.FormData;
  else globalThis.FormData = previousFormData;
}

}
