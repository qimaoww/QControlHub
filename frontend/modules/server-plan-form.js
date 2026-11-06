import { readServerPlanInput } from "./inbound-options-model.js";
export { readServerPlanInput } from "./inbound-options-model.js";
import { generatedPlanFields, generatedFieldActions } from "./inbound-options-model.js";
import { readMieruPattern, validateMieruNonce } from "./inbound-options-model.js";
import { readXHTTPOptions } from "./server-plan-xhttp.js";
import { setVisible } from "./presence-motion.js";
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
    button.className = form.closest?.(".config-inbound-dialog") ? "button field-generate-button" : "field-generate-button";
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
        setVisible(button, !(transport?.value === "raw"));
      };
      updateVisibility();
    }
  });
}

function wireguardProtocolOptions(plan, endpoint = false) {
  return `<div class="preset-protocol-options" data-wireguard-options ${endpoint ? "data-wireguard-endpoint" : ""}>
    <details class="preset-option-panel" open><summary><b>WireGuard 密钥与客户端</b><small>导入私钥时须填写匹配公钥</small></summary>
    ${endpoint ? '<div class="plan-fields one"><p class="tls-help">节点内核需支持 WireGuard 与 gVisor。</p></div>' : ""}
    <div class="plan-fields two">
      <label class="secret-input">服务端私钥<span class="secret-value-control"><input type="password" name="wireguard_server_private_key" required maxlength="44" value="${esc(plan.wireguard_server_private_key || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span></label>
      <label>服务端公钥<input name="wireguard_server_public_key" required maxlength="44" value="${esc(plan.wireguard_server_public_key || "")}"></label>
      <label class="secret-input">客户端私钥<span class="secret-value-control"><input type="password" name="wireguard_client_private_key" required maxlength="44" value="${esc(plan.wireguard_client_private_key || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span></label>
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

export function listenerAdvancedOptions(plan) {
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
    <details class="preset-option-panel" open><summary><b>Sudoku 密钥</b><small>Ed25519 分割密钥</small></summary><div class="plan-fields one">${optionSecret("sudoku_client_key", "Available Private Key（客户端）", plan.sudoku_client_key, "64 字节十六进制私钥。")}</div></details>
    <details class="preset-option-panel" open><summary><b>填充与字节表</b></summary><div class="plan-fields three"><label>Padding 最小值<input type="number" name="sudoku_padding_min" min="0" max="100" value="${Number(plan.sudoku_padding_min ?? 5)}"></label><label>Padding 最大值<input type="number" name="sudoku_padding_max" min="0" max="100" value="${Number(plan.sudoku_padding_max ?? 15)}"></label><label>Table Type<select name="sudoku_table_type">${["prefer_entropy", "prefer_ascii", "up_ascii_down_entropy", "up_entropy_down_ascii"].map((value) => `<option value="${value}" ${value === (plan.sudoku_table_type || "prefer_entropy") ? "selected" : ""}>${value}</option>`).join("")}</select></label></div></details>
    <details class="preset-option-panel" open><summary><b>HTTPMask 与下行</b><small>服务端固定 auto</small></summary><div class="plan-fields three"><label class="plan-check"><input type="checkbox" name="sudoku_httpmask_enabled" value="1" ${plan.sudoku_httpmask_enabled ? "checked" : ""}><span><b>启用 HTTPMask</b><small>关闭后使用原始 TCP。</small></span></label><label>客户端 HTTPMask 模式<select name="sudoku_httpmask_mode">${["ws", "stream", "poll", "auto"].map((value) => `<option value="${value}" ${value === (plan.sudoku_httpmask_mode || "ws") ? "selected" : ""}>${value}</option>`).join("")}</select></label><label class="plan-check" data-sudoku-httpmask-modern><input type="checkbox" name="sudoku_httpmask_tls" value="1" ${plan.sudoku_httpmask_tls ? "checked" : ""}><span><b>客户端强制 HTTPS</b><small>仅现代隧道模式生效。</small></span></label></div><div class="plan-fields three"><label data-sudoku-httpmask-modern data-sudoku-httpmask-fields>Host / SNI<input name="sudoku_httpmask_host" value="${esc(plan.sudoku_httpmask_host || "")}" placeholder="可选 example.com:443"></label><label data-sudoku-httpmask-fields>Path Root<input name="sudoku_httpmask_path_root" maxlength="128" value="${esc(plan.sudoku_httpmask_path_root || "")}" placeholder="例如 aabbcc"></label><label>Fallback<input name="sudoku_fallback" value="${esc(plan.sudoku_fallback || "")}" placeholder="可选 127.0.0.1:80"></label></div><div class="plan-fields two"><label class="plan-check"><input type="checkbox" name="sudoku_enable_pure_downlink" value="1" ${plan.sudoku_enable_pure_downlink ? "checked" : ""}><span><b>纯 Sudoku 下行</b></span></label><label>握手超时（秒）<input type="number" name="sudoku_handshake_timeout" min="1" max="300" value="${Number(plan.sudoku_handshake_timeout || 5)}"></label></div></details>
    <details class="preset-option-panel"><summary><b>原生复用</b></summary><div class="plan-fields one"><label>Sudoku Multiplex<select name="sudoku_multiplex"><option value="off" ${(plan.sudoku_multiplex || "off") === "off" ? "selected" : ""}>off</option><option value="auto" ${plan.sudoku_multiplex === "auto" ? "selected" : ""}>auto</option><option value="on" ${plan.sudoku_multiplex === "on" ? "selected" : ""}>on</option></select><small>auto 仅复用 HTTPMask；on 启用单会话多目标。不叠加 SMux / TCP Brutal。</small></label></div></details>${listenerAdvancedOptions(plan)}</div>`;
}

function bindProtocolOptionVisibility(form) {
  const transport = form.elements.namedItem("transport");
  const tls = form.elements.namedItem("tls_enabled");
  const tlsMode = form.elements.namedItem("tls_mode");
  const updateNative = () => {
    const kind = transport?.value || "raw";
    form.querySelectorAll('[data-regenerate="transport_path"]').forEach(button => setVisible(button, kind !== "raw"));
    const path = form.elements.namedItem("transport_path");
    const host = form.elements.namedItem("transport_host");
    const usesPath = !["raw", "quic"].includes(kind);
    if (path && form.querySelector("[data-transport-path]")) {
      setVisible(form.querySelector("[data-transport-path]"), usesPath);
      path.disabled = !usesPath; path.required = usesPath;
    }
    if (host) { const active = ["http", "websocket", "httpupgrade", "xhttp"].includes(kind); setVisible(form.querySelector("[data-transport-host]"), active); host.disabled = !active; }
    const flow = form.elements.namedItem("flow");
    if (flow?.tagName === "SELECT") { flow.disabled = kind !== "raw"; if (flow.disabled) flow.value = ""; }
    const method = form.elements.namedItem("transport_http_method");
    if (method) { setVisible(form.querySelector("[data-http-method]"), kind === "http"); method.disabled = kind !== "http"; }
    const tlsFields = form.querySelector("[data-native-tls]");
    if (tlsFields) { setVisible(tlsFields, Boolean(tls?.checked)); tlsFields.querySelectorAll("input, textarea").forEach(control => control.disabled = !tls?.checked); }
  };
  let currentTLSMode = tlsMode?.value || "";
  const updateTLSMode = () => {
    const nextTLSMode = tlsMode?.value || "";
    if (tlsMode?.dataset.nginxAcme === "1" && currentTLSMode === "nginx" && nextTLSMode !== "nginx") {
      const trustCertificate = form.elements.namedItem("tls_client_certificate");
      if (trustCertificate) trustCertificate.value = "";
    }
    currentTLSMode = nextTLSMode;
    const managed = tls?.checked && ["caddy", "nginx", "external"].includes(tlsMode?.value);
    form.querySelectorAll("[data-managed-tls]").forEach(el => { setVisible(el, managed); el.querySelectorAll("input").forEach(control => { control.disabled = !managed; control.required = managed; }); });
    const automaticNginx = tlsMode?.value === "nginx" && tlsMode.dataset.nginxAcme === "1";
    const acme = form.querySelector("[data-caddy-acme]");
    if (acme) { const active = Boolean(tls?.checked && (tlsMode?.value === "caddy" || automaticNginx)); setVisible(acme, active); acme.querySelectorAll("input").forEach(control => control.disabled = !active); }
    const hint = form.querySelector("[data-managed-tls-hint]");
    if (hint) hint.textContent = tlsMode?.value === "nginx"
      ? automaticNginx ? "域名解析到节点，放行 TCP 80 和所选公网端口；自动申请并续期公开可信证书。" : "面板生成自签证书，客户端通过导出的公共证书验证。请放行所选公网端口。"
      : tlsMode?.value === "external"
        ? "自行配置可信 TLS 证书和公网反代，回源到本机内部端口与路径；HTTP/H2 使用 h2c，WS/HTTPUpgrade 使用 HTTP/1.1 Upgrade。节点检查不验证外部入口。"
      : tlsMode?.value === "caddy"
        ? "域名解析到节点，放行 TCP 80 和所选公网端口；自动申请并续期证书。"
        : "使用节点上已有的证书和私钥，需填写下方文件路径。";
    form.querySelectorAll("[data-native-certificate]").forEach(el => { setVisible(el, !managed); el.querySelectorAll("input,textarea").forEach(control => control.disabled = managed || !tls?.checked); });
    const listen = form.elements.namedItem("listen");
    if (listen && tlsMode) {
      listen.readOnly = managed;
      if (managed) listen.value = "127.0.0.1";
      else if (form.dataset.managedTLS === "1" && listen.value === "127.0.0.1") listen.value = "0.0.0.0";
      form.dataset.managedTLS = managed ? "1" : "0";
    }
    const domain = form.elements.namedItem("tls_server_name");
    if (domain && tlsMode) domain.required = managed;
    const portLabel = form.querySelector("[data-listen-port-label]");
    if (portLabel) portLabel.textContent = managed ? "内部监听端口" : "监听端口";
    const port = form.elements.namedItem("tls_public_port");
    port?.setCustomValidity(managed && port.value === form.elements.namedItem("port")?.value ? "公网端口不能与内部监听端口相同" : "");
    const domainLabel = form.querySelector("[data-tls-domain-label]");
    if (domainLabel) domainLabel.textContent = managed ? "域名" : "证书域名 / TLS SNI";
    const route = form.querySelector("[data-managed-tls-route]");
    if (route) {
      setVisible(route, Boolean(managed));
      route.textContent = `${tlsMode?.value === "external" ? "外部反代" : tlsMode?.value === "nginx" ? "Nginx" : "Caddy"}：${domain?.value || "你的域名"}:${port?.value || "443"}${form.elements.namedItem("transport_path")?.value || ""} → ${listen?.value}:${form.elements.namedItem("port")?.value}`;
    }
    if (tlsMode?.value === "native") {
      const advanced = form.querySelector("[data-tls-advanced]");
      if (advanced) advanced.open = true;
    }
  };
  bindEvent(tlsMode, "change", updateTLSMode);
  bindEvent(form.elements.namedItem("tls_public_port"), "input", updateTLSMode);
  bindEvent(form.elements.namedItem("port"), "input", updateTLSMode);
  bindEvent(form.elements.namedItem("tls_server_name"), "input", updateTLSMode);
  bindEvent(form.elements.namedItem("transport_path"), "input", updateTLSMode);
  const updateSecurity = () => { updateNative(); updateTLSMode(); };
  bindEvent(transport, "change", updateSecurity);
  bindEvent(tls, "change", updateSecurity);
  updateNative();
  updateTLSMode();
  if (form.querySelector("[data-wireguard-endpoint]")) {
    const listen = form.elements.namedItem("listen");
    if (listen) listen.readOnly = true;
  }
  const show = (selector, visible) => {
    form.querySelectorAll(selector).forEach((element) => {
      setVisible(element, visible);
    });
  };
  const mieruMode = form.elements.namedItem("mieru_traffic_pattern");
  const mieruCustom = form.querySelector("[data-mieru-custom-fields]");
  const mieruFragment = form.elements.namedItem("mieru_custom_tcp_fragment");
  const mieruNonce = form.elements.namedItem("mieru_custom_nonce_type");
  const mieruNonceMin = form.elements.namedItem("mieru_custom_nonce_min_len");
  const mieruNonceMax = form.elements.namedItem("mieru_custom_nonce_max_len");
  const mieruFixedHex = form.elements.namedItem("mieru_custom_nonce_fixed_hex");
  const mieruEntropy = form.elements.namedItem("mieru_custom_low_entropy_mode");
  const validateMieru = () => validateMieruNonce({mode:mieruMode?.value, nonce:mieruNonce?.value,
    min:mieruNonceMin, max:mieruNonceMax, fixed:mieruFixedHex});
  const updateMieru = () => {
    if (!mieruMode || !mieruCustom) return;
    const custom = mieruMode.value === "custom";
    setVisible(mieruCustom, custom);
    show("[data-mieru-fragment-sleep]", Boolean(mieruFragment?.checked));
    show("[data-mieru-nonce-range]", ["PRINTABLE", "PRINTABLE_SUBSET"].includes(mieruNonce?.value));
    show("[data-mieru-nonce-fixed]", mieruNonce?.value === "FIXED");
    show("[data-mieru-mask-rotation]", mieruEntropy?.value !== "0");
    mieruCustom.querySelectorAll("input, select").forEach((input) => {
      const visible = custom && !input.closest("[data-mieru-custom-fields] [hidden]");
      input.disabled = !visible;
      if (input.type === "number") input.required = visible;
    });
    if (mieruFixedHex) mieruFixedHex.required = custom && mieruNonce?.value === "FIXED";
    validateMieru();
  };
  for (const control of [mieruMode, mieruFragment, mieruNonce, mieruNonceMin, mieruNonceMax, mieruFixedHex, mieruEntropy]) {
    bindEvent(control, "input", updateMieru);
    bindEvent(control, "change", updateMieru);
  }
  updateMieru();
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
