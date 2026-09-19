import { bindEvent } from "./refresh.js";

// Config views render user-controlled values into HTML before binding their
// interactions. Keep the renderer self-contained instead of relying on the
// app module's private helper.
const esc = (value) =>
  String(value ?? "").replace(
    /[&<>"']/g,
    (char) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        char
      ],
  );

const generatedPlanFields = Object.freeze([
  "tag",
  "port",
  "username",
  "credential",
  "secondary_credential",
  "transport_path",
  "reality_private_key",
  "reality_public_key",
  "reality_short_id",
  "vless_decryption",
  "vless_encryption",
  "sudoku_client_key",
  "snell_shadow_tls_password",
  "wireguard_server_private_key",
  "wireguard_server_public_key",
  "wireguard_client_private_key",
  "wireguard_client_public_key",
  "wireguard_preshared_key",
]);

const generatedFieldActions = Object.freeze([
  ["tag", "tag", "生成入站标签"],
  ["port", "port", "生成监听端口"],
  ["username", "username", "生成用户名"],
  ["credential", "credential", "生成凭据"],
  ["secondary_credential", "secondary_credential", "生成次凭据"],
  ["transport_path", "transport_path", "生成路径或 ServiceName"],
  [
    "reality_public_key",
    "reality_private_key,reality_public_key",
    "生成 Reality 密钥对",
  ],
  [
    "reality_private_key",
    "reality_private_key,reality_public_key",
    "生成 Reality 密钥对",
  ],
  ["reality_short_id", "reality_short_id", "生成 Short ID"],
  [
    "vless_decryption",
    "vless_decryption,vless_encryption",
    "生成 VLESS-ENC 密钥对",
  ],
  [
    "vless_encryption",
    "vless_decryption,vless_encryption",
    "生成 VLESS-ENC 密钥对",
  ],
  [
    "reality_mldsa65_seed",
    "reality_mldsa65_seed",
    "生成 ML-DSA-65 密钥对",
  ],
  ["sudoku_client_key", "credential,sudoku_client_key", "生成 Sudoku 密钥对"],
  ["snell_shadow_tls_password", "snell_shadow_tls_password", "生成 ShadowTLS 密码"],
  ["wireguard_server_private_key", "wireguard_server_private_key,wireguard_server_public_key", "生成 WireGuard 服务端密钥对"],
  ["wireguard_server_public_key", "wireguard_server_private_key,wireguard_server_public_key", "生成 WireGuard 服务端密钥对"],
  ["wireguard_client_private_key", "wireguard_client_private_key,wireguard_client_public_key", "生成 WireGuard 客户端密钥对"],
  ["wireguard_client_public_key", "wireguard_client_private_key,wireguard_client_public_key", "生成 WireGuard 客户端密钥对"],
  ["wireguard_preshared_key", "wireguard_preshared_key", "生成 WireGuard 预共享密钥"],
]);

const protocolNavigationNames = Object.freeze({
  "vless-xhttp-reality": "VLESS-XHTTP-uTLS-REALITY",
  "vless-enc-tcp-reality-vision": "VLESS-ENC-TCP-Vision-uTLS-REALITY",
  "vless-enc-xhttp-reality-vision": "VLESS-ENC-XHTTP-Vision-uTLS-REALITY",
});

function installGeneratedFieldButtons(form, protocol) {
  generatedFieldActions.forEach(([name, fields, label]) => {
    const control = form.elements.namedItem(name);
    if (!control || control.type === "hidden") return;
    if (control.parentElement.querySelector?.(`[data-regenerate]`)) return;
    const button = document.createElement("button");
    button.className = "field-generate-button";
    button.type = "button";
    const regeneratedFields =
      name === "credential" && protocol?.key === "sudoku"
        ? "credential,sudoku_client_key"
        : fields;
    button.dataset.regenerate = regeneratedFields;
    button.dataset.regenerateSuccess = `${label.replace(/^生成\s*/, "")}已生成`;
    button.setAttribute("aria-label", label);
    button.textContent = regeneratedFields.includes(",") ? "生成密钥对" : "生成";
    if (control.parentElement.classList.contains("secret-value-control")) {
      control.parentElement.append(button);
      return;
    }
    const wrapper = document.createElement("span");
    wrapper.className = "generated-input-control";
    control.replaceWith(wrapper);
    wrapper.append(control, button);
    if (name === "transport_path") {
      const transport = form.elements.namedItem("transport");
      const updateVisibility = () => {
        button.hidden = transport?.value === "raw";
      };
      bindEvent(transport, "change", updateVisibility);
      updateVisibility();
    }
  });
}

export function readServerPlanInput(form, protocol) {
  const values = new FormData(form);
  return {
    protocol: protocol.key,
    tag: values.get("tag"),
    listen: values.get("listen"),
    port: Number(values.get("port")),
    username: values.get("username"),
    credential: values.get("credential"),
    secondary_credential: values.get("secondary_credential"),
    method: values.get("method"),
    flow:
      protocol.key === "vless" ||
      (protocol.uses_vless_encryption && protocol.uses_reality)
        ? "xtls-rprx-vision"
        : "",
    transport: values.get("transport"),
    transport_path: values.get("transport_path"),
    tls_enabled:
      values.get("tls_enabled") === "1" || protocol.requires_tls,
    certificate_path: values.get("certificate_path") || "",
    private_key_path: values.get("private_key_path") || "",
    reality_enabled: values.get("reality_enabled") === "1",
    reality_private_key: values.get("reality_private_key") || "",
    reality_public_key: values.get("reality_public_key") || "",
    reality_short_id: values.get("reality_short_id") || "",
    reality_server_name: values.get("reality_server_name") || "",
    reality_min_client_ver:
      values.get("reality_min_client_ver") || "0.0.0",
    reality_mldsa65_seed: values.get("reality_mldsa65_seed") || "",
    reality_mldsa65_verify: values.get("reality_mldsa65_verify") || "",
    vless_decryption: values.get("vless_decryption") || "",
    vless_encryption: values.get("vless_encryption") || "",
    listener_routing_mark: Number(values.get("listener_routing_mark") || 0),
    listener_rule: values.get("listener_rule") || "",
    ss_rust_dns: values.get("ss_rust_dns") || "",
    ss_rust_outbound_bind_addr: values.get("ss_rust_outbound_bind_addr") || "",
    ss_rust_ipv6_first: values.get("ss_rust_ipv6_first") === "1",
    listener_proxy: values.get("listener_proxy") || "",
    mieru_transport: values.get("mieru_transport") || "TCP",
    snell_version: Number(values.get("snell_version") || 0),
    snell_udp: values.get("snell_udp") === "1",
    snell_reuse: values.get("snell_reuse") === "1",
    snell_obfs_mode: values.get("snell_obfs_mode") || "none",
    snell_obfs_host: values.get("snell_obfs_host") || "",
    snell_client_fingerprint: values.get("snell_client_fingerprint") || "chrome",
    snell_shadow_tls_version: Number(values.get("snell_shadow_tls_version") || 0),
    snell_shadow_tls_password: values.get("snell_shadow_tls_password") || "",
    snell_shadow_tls_user: values.get("snell_shadow_tls_user") || "",
    snell_shadow_tls_handshake: values.get("snell_shadow_tls_handshake") || "",
    snell_shadow_tls_proxy: values.get("snell_shadow_tls_proxy") || "",
    snell_shadow_tls_alpn: values.get("snell_shadow_tls_alpn") || "",
    sudoku_client_key: values.get("sudoku_client_key") || "",
    sudoku_padding_min: Number(values.get("sudoku_padding_min") || 0),
    sudoku_padding_max: Number(values.get("sudoku_padding_max") || 0),
    sudoku_table_type: values.get("sudoku_table_type") || "",
    sudoku_handshake_timeout: Number(values.get("sudoku_handshake_timeout") || 0),
    sudoku_enable_pure_downlink: values.get("sudoku_enable_pure_downlink") === "1",
    sudoku_httpmask_enabled: values.get("sudoku_httpmask_enabled") === "1",
    sudoku_httpmask_mode: values.get("sudoku_httpmask_mode") || "ws",
    sudoku_httpmask_tls: values.get("sudoku_httpmask_tls") === "1",
    sudoku_httpmask_host: values.get("sudoku_httpmask_host") || "",
    sudoku_httpmask_path_root: values.get("sudoku_httpmask_path_root") || "",
    sudoku_multiplex: values.get("sudoku_multiplex") || "off",
    sudoku_fallback: values.get("sudoku_fallback") || "",
    target_address: values.get("target_address") || "",
    target_port: Number(values.get("target_port") || 0),
    network: values.get("network") || "",
    block_mainland_destination:
      form.dataset?.blockMainlandDestination === "1",
    block_mainland_source: form.dataset?.blockMainlandSource === "1",
    wireguard_server_private_key: values.get("wireguard_server_private_key") || "",
    wireguard_server_public_key: values.get("wireguard_server_public_key") || "",
    wireguard_client_private_key: values.get("wireguard_client_private_key") || "",
    wireguard_client_public_key: values.get("wireguard_client_public_key") || "",
    wireguard_preshared_key: values.get("wireguard_preshared_key") || "",
    wireguard_client_address: values.get("wireguard_client_address") || "",
    wireguard_server_address: values.get("wireguard_server_address") || "",
    wireguard_allowed_ips: values.get("wireguard_allowed_ips") || "",
    wireguard_mtu: Number(values.get("wireguard_mtu") || 0),
    wireguard_keepalive: Number(values.get("wireguard_keepalive") || 0),
  };
}

function wireguardProtocolOptions(plan, endpoint = false) {
  return `<div class="preset-protocol-options" data-wireguard-options ${endpoint ? "data-wireguard-endpoint" : ""}>
    <details class="preset-option-panel" open><summary><b>WireGuard 密钥与客户端</b><small>导入私钥时须填写匹配公钥</small></summary>
    ${endpoint ? '<p class="preset-compat-note">sing-box 监听所有接口；需 with_wireguard 与 with_gvisor。</p>' : ""}
    <div class="plan-fields two">
      <label class="secret-input">服务端私钥<span class="secret-value-control"><input type="password" name="wireguard_server_private_key" required maxlength="44" value="${esc(plan.wireguard_server_private_key || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span></label>
      <label>服务端公钥<input name="wireguard_server_public_key" required maxlength="44" value="${esc(plan.wireguard_server_public_key || "")}"></label>
      <label class="secret-input">客户端私钥<span class="secret-value-control"><input type="password" name="wireguard_client_private_key" required maxlength="44" value="${esc(plan.wireguard_client_private_key || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span><small>仅控制面加密保存，不写入节点。</small></label>
      <label>客户端公钥<input name="wireguard_client_public_key" required maxlength="44" value="${esc(plan.wireguard_client_public_key || "")}"></label>
      <label class="secret-input">预共享密钥（可选）<span class="secret-value-control"><input type="password" name="wireguard_preshared_key" maxlength="44" value="${esc(plan.wireguard_preshared_key || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span></label>
      <label>客户端地址<input name="wireguard_client_address" required value="${esc(plan.wireguard_client_address || "10.66.66.2/32")}"></label>
      ${endpoint ? `<label>服务端隧道地址<input name="wireguard_server_address" required value="${esc(plan.wireguard_server_address || "10.66.66.1/24")}"></label>` : ""}
      <label>允许的客户端路由<input name="wireguard_allowed_ips" value="${esc(plan.wireguard_allowed_ips || "")}" placeholder="留空时按客户端地址族生成默认路由"></label>
      <label>MTU<input type="number" name="wireguard_mtu" min="576" max="65535" value="${Number(plan.wireguard_mtu ?? 1420)}"></label>
      <label>客户端 Keepalive（秒）<input type="number" name="wireguard_keepalive" min="0" max="65535" value="${Number(plan.wireguard_keepalive ?? 25)}"><small>0 表示关闭。</small></label>
    </div></details></div>`;
}

function optionSecret(name, label, value, help = "") {
  return `<label class="secret-input">${label}<span class="secret-value-control"><input type="password" name="${name}" value="${esc(value || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span>${help ? `<small>${help}</small>` : ""}</label>`;
}

function listenerAdvancedOptions(plan) {
  return `<details class="preset-option-panel"><summary><b>监听高级选项</b><small>路由标记、子规则与前置代理</small></summary><div class="plan-fields three"><label>Routing Mark<input type="number" name="listener_routing_mark" min="0" value="${Number(plan.listener_routing_mark || 0)}"><small>仅 Linux；0 表示不设置。</small></label><label>Rule<input name="listener_rule" maxlength="64" value="${esc(plan.listener_rule || "")}" placeholder="可选子规则名称"></label><label>Proxy<input name="listener_proxy" maxlength="64" value="${esc(plan.listener_proxy || "")}" placeholder="可选前置代理名称"></label></div></details>`;
}

function snellProtocolOptions(plan, shadowTLS = false) {
  const shadowOptions = shadowTLS
    ? `<details class="preset-option-panel" open><summary><b>ShadowTLS v3</b><small>客户端严格校验证书</small></summary><input type="hidden" name="snell_obfs_mode" value="shadow-tls"><input type="hidden" name="snell_shadow_tls_version" value="3">
      <div class="plan-fields two"><label>客户端 Host / SNI<input name="snell_obfs_host" required value="${esc(plan.snell_obfs_host || "")}" placeholder="www.example.com"></label><label>用户名<input name="snell_shadow_tls_user" required value="${esc(plan.snell_shadow_tls_user || "")}"></label></div>
      <div class="plan-fields one">${optionSecret("snell_shadow_tls_password", "ShadowTLS 密码", plan.snell_shadow_tls_password, "独立于 Snell PSK，至少 8 个字符。")}</div>
      <div class="plan-fields three"><label>握手目标<input name="snell_shadow_tls_handshake" required value="${esc(plan.snell_shadow_tls_handshake || "")}" placeholder="www.example.com:443"><small>须与客户端 Host 对应，提供可信公网证书。</small></label><label>握手代理<input name="snell_shadow_tls_proxy" value="${esc(plan.snell_shadow_tls_proxy || "")}" placeholder="可选代理名称"></label><label>客户端 ALPN<input name="snell_shadow_tls_alpn" value="${esc(plan.snell_shadow_tls_alpn || "h2,http/1.1")}" placeholder="h2,http/1.1"></label></div>
      <div class="plan-fields one"><label>客户端指纹<input name="snell_client_fingerprint" value="${esc(plan.snell_client_fingerprint || "chrome")}" placeholder="chrome"></label></div></details>`
    : '<input type="hidden" name="snell_obfs_mode" value="none">';
  return `<div class="preset-protocol-options" data-snell-options><details class="preset-option-panel" open><summary><b>Snell v5</b></summary><input type="hidden" name="snell_version" value="5"><div class="plan-fields two"><label class="plan-check"><input type="checkbox" name="snell_udp" value="1" ${plan.snell_udp ? "checked" : ""}><span><b>UDP over TCP</b></span></label><label class="plan-check"><input type="checkbox" name="snell_reuse" value="1" ${plan.snell_reuse ? "checked" : ""}><span><b>客户端连接复用</b></span></label></div></details>${shadowOptions}${listenerAdvancedOptions(plan)}</div>`;
}

function sudokuProtocolOptions(plan) {
  return `<div class="preset-protocol-options" data-sudoku-options>
    <details class="preset-option-panel" open><summary><b>Sudoku 密钥</b><small>Ed25519 分割密钥</small></summary><div class="plan-fields one">${optionSecret("sudoku_client_key", "Available Private Key（客户端）", plan.sudoku_client_key, "64 字节十六进制私钥；仅控制面加密保存，不写入节点。")}</div></details>
    <details class="preset-option-panel" open><summary><b>填充与字节表</b></summary><div class="plan-fields three"><label>Padding 最小值<input type="number" name="sudoku_padding_min" min="0" max="100" value="${Number(plan.sudoku_padding_min ?? 5)}"></label><label>Padding 最大值<input type="number" name="sudoku_padding_max" min="0" max="100" value="${Number(plan.sudoku_padding_max ?? 15)}"></label><label>Table Type<select name="sudoku_table_type">${["prefer_entropy", "prefer_ascii", "up_ascii_down_entropy", "up_entropy_down_ascii"].map((value) => `<option value="${value}" ${value === (plan.sudoku_table_type || "prefer_entropy") ? "selected" : ""}>${value}</option>`).join("")}</select></label></div></details>
    <details class="preset-option-panel" open><summary><b>HTTPMask 与下行</b><small>服务端固定 auto</small></summary><div class="plan-fields three"><label class="plan-check"><input type="checkbox" name="sudoku_httpmask_enabled" value="1" ${plan.sudoku_httpmask_enabled ? "checked" : ""}><span><b>启用 HTTPMask</b><small>关闭后使用原始 TCP。</small></span></label><label>客户端 HTTPMask 模式<select name="sudoku_httpmask_mode">${["ws", "stream", "poll", "auto"].map((value) => `<option value="${value}" ${value === (plan.sudoku_httpmask_mode || "ws") ? "selected" : ""}>${value}</option>`).join("")}</select></label><label class="plan-check" data-sudoku-httpmask-modern><input type="checkbox" name="sudoku_httpmask_tls" value="1" ${plan.sudoku_httpmask_tls ? "checked" : ""}><span><b>客户端强制 HTTPS</b><small>仅现代隧道模式生效。</small></span></label></div><div class="plan-fields three"><label data-sudoku-httpmask-modern data-sudoku-httpmask-fields>Host / SNI<input name="sudoku_httpmask_host" value="${esc(plan.sudoku_httpmask_host || "")}" placeholder="可选 example.com:443"></label><label data-sudoku-httpmask-fields>Path Root<input name="sudoku_httpmask_path_root" maxlength="128" value="${esc(plan.sudoku_httpmask_path_root || "")}" placeholder="例如 aabbcc"></label><label>Fallback<input name="sudoku_fallback" value="${esc(plan.sudoku_fallback || "")}" placeholder="可选 127.0.0.1:80"></label></div><div class="plan-fields two"><label class="plan-check"><input type="checkbox" name="sudoku_enable_pure_downlink" value="1" ${plan.sudoku_enable_pure_downlink ? "checked" : ""}><span><b>纯 Sudoku 下行</b></span></label><label>握手超时（秒）<input type="number" name="sudoku_handshake_timeout" min="1" max="300" value="${Number(plan.sudoku_handshake_timeout || 5)}"></label></div></details>
    <details class="preset-option-panel"><summary><b>原生复用</b></summary><div class="plan-fields one"><label>Sudoku Multiplex<select name="sudoku_multiplex"><option value="off" ${(plan.sudoku_multiplex || "off") === "off" ? "selected" : ""}>off</option><option value="auto" ${plan.sudoku_multiplex === "auto" ? "selected" : ""}>auto</option><option value="on" ${plan.sudoku_multiplex === "on" ? "selected" : ""}>on</option></select><small>auto 仅复用 HTTPMask；on 启用单会话多目标。不叠加 SMux / TCP Brutal。</small></label></div></details>${listenerAdvancedOptions(plan)}</div>`;
}

function bindProtocolOptionVisibility(form) {
  if (form.querySelector("[data-wireguard-endpoint]")) {
    const listen = form.elements.namedItem("listen");
    if (listen) listen.readOnly = true;
  }
  const show = (selector, visible) => {
    form.querySelectorAll(selector).forEach((element) => {
      element.hidden = !visible;
    });
  };
  const snellVersion = form.elements.namedItem("snell_version");
  const snellMode = form.elements.namedItem("snell_obfs_mode");
  const updateSnell = () => {
    if (!snellVersion || !snellMode) return;
    const version = Number(snellVersion.value);
    const udp = form.elements.namedItem("snell_udp");
    const reuse = form.elements.namedItem("snell_reuse");
    if (udp) {
      if (version < 3) udp.checked = false;
      udp.disabled = version < 3;
    }
    if (reuse) {
      if (version < 4) reuse.checked = false;
      reuse.disabled = version < 4;
    }
  };
  bindEvent(snellVersion, "change", updateSnell);
  bindEvent(snellMode, "change", updateSnell);
  updateSnell();

  const httpEnabled = form.elements.namedItem("sudoku_httpmask_enabled");
  const httpMode = form.elements.namedItem("sudoku_httpmask_mode");
  const updateSudoku = () => {
    if (!form.querySelector("[data-sudoku-options]")) return;
    const enabled = Boolean(httpEnabled?.checked);
    const modern = enabled && httpMode?.value !== "legacy";
    show("[data-sudoku-httpmask-fields]", enabled);
    show("[data-sudoku-httpmask-modern]", modern);
  };
  for (const control of [httpEnabled, httpMode]) bindEvent(control, "change", updateSudoku);
  updateSudoku();
}

export function bindServerPlanRegeneration({
  form,
  buttons,
  api,
  base,
  protocol,
  report,
  onApplied,
  canApply = () => true,
  onBusy = () => {},
}) {
  let latestRequest = 0;
  let formRevision = 0;
  let pendingRequests = 0;
  const buttonState = new Map(
    buttons.map((button) => [
      button,
      { label: button.textContent, disabled: button.disabled },
    ]),
  );
  const markChanged = () => {
    formRevision += 1;
  };
  bindEvent(form, "input", markChanged);
  bindEvent(form, "change", markChanged);
  buttons.forEach((button) => {
    bindEvent(button, "click", async () => {
      if (!canApply()) return;
      const request = ++latestRequest;
      const requestedRevision = formRevision;
      const input = readServerPlanInput(form, protocol);
      const requestedFields =
        !button.dataset.regenerate || button.dataset.regenerate === "all"
          ? generatedPlanFields
          : button.dataset.regenerate.split(",");
      pendingRequests += 1;
      onBusy(true);
      buttons.forEach((item) => {
        item.disabled = true;
      });
      button.textContent = "生成中…";
      button.setAttribute("aria-busy", "true");
      try {
        const plan = await api(`${base}/plans`, {
          method: "POST",
          body: JSON.stringify({ protocol: protocol.key, input }),
        });
        if (request !== latestRequest || form.isConnected === false || !canApply()) return;
        if (requestedRevision !== formRevision) {
          report("当前参数已变更，未应用过期的生成结果", "error");
          return;
        }
        requestedFields.forEach((name) => {
          const control = form.elements.namedItem(name);
          if (control && Object.hasOwn(plan, name)) {
            control.value = String(plan[name] ?? "");
          }
        });
        onApplied?.(readServerPlanInput(form, protocol));
        report(button.dataset.regenerateSuccess || "参数已重新生成");
      } catch (error) {
        if (request === latestRequest && form.isConnected !== false) {
          report(`生成参数失败：${error.message}`, "error");
        }
      } finally {
        pendingRequests -= 1;
        if (pendingRequests === 0) {
          buttonState.forEach((initial, item) => {
            item.disabled = initial.disabled;
            item.textContent = initial.label;
            item.removeAttribute("aria-busy");
          });
          onBusy(false);
        }
      }
    });
  });
}

export { installGeneratedFieldButtons, protocolNavigationNames, snellProtocolOptions, sudokuProtocolOptions, wireguardProtocolOptions, bindProtocolOptionVisibility };
