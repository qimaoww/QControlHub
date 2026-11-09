// Shared protocol option serialization; owns no rendering or browser state.
export const isXHTTPGroup = key => ["vless-xhttp-five", "vless-xhttp-tls-reality", "vless-xhttp-reality-tls"].includes(key);
const object = value => { if (!value?.trim()) return {}; const parsed = JSON.parse(value); if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new Error("XHTTP extra 必须是 JSON 对象"); return parsed; };
export function readXHTTPOptions(form, protocol, values) {
  if (!protocol?.transports?.includes("xhttp")) return {};
  const extra = object(values.get("xhttp_extra") || "{}");
  const group = isXHTTPGroup(protocol.key);
  if (!group && values.get("xhttp_split") === "1") {
    const tls = protocol.key === "vless-xhttp-tls";
    const d = extra.downloadSettings ||= {};
    Object.assign(d, {address:values.get("xhttp_download_address") || "", port:Number(values.get("xhttp_download_port")), network:"xhttp", security:tls ? "tls" : "reality"});
    const security = d[tls ? "tlsSettings" : "realitySettings"] ||= {};
    Object.assign(security, {serverName:values.get("xhttp_download_sni") || "", fingerprint:security.fingerprint || "chrome"});
    if (tls) Object.assign(security, {allowInsecure:false, alpn:(values.get("xhttp_download_alpn") || "h2").split(",").map(x => x.trim()).filter(Boolean), fingerprint:values.get("xhttp_download_alpn")?.trim() === "http/1.1" ? "unsafe" : security.fingerprint === "unsafe" ? "chrome" : security.fingerprint});
    else {
      Object.assign(security, {publicKey:values.get("reality_public_key") || "", shortId:values.get("reality_short_id") || ""});
      if (values.get("reality_mldsa65_verify")) security.mldsa65Verify = values.get("reality_mldsa65_verify");
      else delete security.mldsa65Verify;
    }
    const x = d.xhttpSettings ||= {};
    Object.assign(x, {path:values.get("transport_path"), host:values.get("xhttp_download_host") || "", mode:x.mode || "auto"});
  } else if (!group && form.dataset.xhttpSplitChanged === "true") delete extra.downloadSettings;
  object(values.get("xhttp_server_extra") || "{}");
  return {
    xhttp_mode:values.get("xhttp_mode") || "auto", xhttp_extra:JSON.stringify(extra), xhttp_server_extra:values.get("xhttp_server_extra") || "{}",
    client_address:values.get("client_address") || "", client_port:Number(values.get("client_port") || 0), client_host:values.get("client_host") || "",
    tls_client_alpn:values.get("tls_client_alpn") || "", xhttp_cdn:values.get("xhttp_cdn") || "none", reality_target:values.get("reality_target") || "", reality_server_names:values.get("reality_server_names") || "",
    xhttp_tls_address:values.get("xhttp_tls_address") || "", xhttp_tls_client_port:Number(values.get("xhttp_tls_client_port") || 0), xhttp_tls_host:values.get("xhttp_tls_host") || "", xhttp_backend_port:Number(values.get("xhttp_backend_port") || 0), xhttp_tls_port:Number(values.get("xhttp_tls_port") || 0),
    ...(group ? {certificate_path:values.get("certificate_path") || "", private_key_path:values.get("private_key_path") || ""} : {}),
  };
}


export function readMieruPattern(form, protocol) {
  const customControl = (name) => form.elements.namedItem(`mieru_custom_${name}`);
  const customValue = (name) => customControl(name)?.value ?? "";
  const nonceType = customValue("nonce_type");
  const lowEntropyMode = Number(customValue("low_entropy_mode"));
  return protocol.key === "mieru" && form.elements.namedItem("mieru_traffic_pattern")?.value === "custom" ? {
    tcp_fragment: Boolean(customControl("tcp_fragment")?.checked),
    max_sleep_ms: customControl("tcp_fragment")?.checked ? Number(customValue("max_sleep_ms")) : 0,
    nonce_type: nonceType,
    nonce_apply_to_all_udp: Boolean(customControl("nonce_apply_to_all_udp")?.checked),
    nonce_min_len: ["PRINTABLE", "PRINTABLE_SUBSET"].includes(nonceType) ? Number(customValue("nonce_min_len")) : 0,
    nonce_max_len: ["PRINTABLE", "PRINTABLE_SUBSET"].includes(nonceType) ? Number(customValue("nonce_max_len")) : 0,
    nonce_fixed_hex: nonceType === "FIXED" ? customValue("nonce_fixed_hex") : "",
    padding_middle: Number(customValue("padding_middle")),
    padding_end: Number(customValue("padding_end")),
    low_entropy_mode: lowEntropyMode,
    mask_rotation: lowEntropyMode === 0 ? "none" : customValue("mask_rotation"),
  } : undefined;
}

export const generatedPlanFields = Object.freeze([
  "tag",
  "port",
  "username",
  "credential",
  "secondary_credential",
  "transport_path",
  "reality_private_key",
  "reality_public_key",
  "reality_short_id",
  "reality_mldsa65_seed",
  "reality_mldsa65_verify",
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

export const generatedFieldActions = Object.freeze([
  ["tag", "tag", "生成入站标签"],
  ["port", "port", "生成监听端口"],
  ["username", "username", "生成用户名"],
  ["credential", "credential", "生成凭据"],
  ["secondary_credential", "secondary_credential", "生成次凭据"],
  ["transport_path", "transport_path", "生成路径或 ServiceName"],
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
    "reality_mldsa65_seed",
    "reality_mldsa65_seed,reality_mldsa65_verify",
    "生成 ML-DSA-65 密钥对",
  ],
  ["sudoku_client_key", "credential,sudoku_client_key", "生成 Sudoku 密钥对"],
  ["snell_shadow_tls_password", "snell_shadow_tls_password", "生成 ShadowTLS 密码"],
  ["wireguard_server_private_key", "wireguard_server_private_key,wireguard_server_public_key", "生成 WireGuard 服务端密钥对"],
  ["wireguard_client_private_key", "wireguard_client_private_key,wireguard_client_public_key", "生成 WireGuard 客户端密钥对"],
  ["wireguard_preshared_key", "wireguard_preshared_key", "生成 WireGuard 预共享密钥"],
]);


export function readServerPlanInput(form, protocol, values = new FormData(form)) {
  const tlsEnabled = values.get("tls_enabled") === "1" || Boolean(protocol.requires_tls);
  const customPattern = readMieruPattern(form, protocol);
  return {
    ...readXHTTPOptions(form, protocol, values),
    protocol: protocol.key,
    tag: values.get("tag"),
    listen: values.get("listen"),
    port: Number(values.get("port")),
    username: values.get("username"),
    credential: values.get("credential"),
    secondary_credential: values.get("secondary_credential"),
    method: values.get("method"),
    flow: values.get("flow") || (
      (protocol.key === "vless" && values.get("reality_enabled") === "1") ||
      (protocol.uses_vless_encryption && protocol.uses_reality)
        ? "xtls-rprx-vision"
        : ""),
    transport: values.get("transport"),
    transport_path: ["raw", "quic"].includes(values.get("transport")) ? "" : values.get("transport_path") || "",
    transport_host: ["http", "httpupgrade", "websocket", "xhttp"].includes(values.get("transport")) ? values.get("transport_host") || "" : "",
    transport_http_method: values.get("transport") === "http" ? values.get("transport_http_method") || "" : "",
    tls_mode: tlsEnabled && protocol.supports_managed_tls ? values.get("tls_mode") || "native" : "",
    tls_acme_email: !values.get("panel_certificate_id") && tlsEnabled && protocol.supports_managed_tls && (values.get("tls_mode") === "caddy" || ["vless-ws-tls", "vless-xhttp-tls"].includes(protocol.key) && values.get("tls_mode") === "nginx") ? (values.get("tls_acme_email") || "").trim() : "",
    tls_public_port: tlsEnabled && ["caddy", "nginx", "external"].includes(values.get("tls_mode")) ? Number(values.get("tls_public_port") || 443) : 0,
    tls_static_html: protocol.key === "vless-xhttp-tls" && ["caddy", "nginx"].includes(values.get("tls_mode")) ? (values.get("tls_static_page") === "custom" || !form.elements.namedItem("tls_static_page") ? values.get("tls_static_html") || "" : (protocol.static_pages || []).find(page=>page.key === values.get("tls_static_page"))?.html || "") : "",
    tls_server_name: values.get("tls_server_name") || "",
    tls_alpn: values.get("tls_alpn") || "",
    tls_client_alpn: values.get("tls_client_alpn") || "",
    client_address: values.get("client_address") || "",
    client_port: Number(values.get("client_port") || 0),
    client_host: values.get("client_host") || "",
    tls_client_certificate: values.get("tls_client_certificate") || "",
    tls_enabled: tlsEnabled,
    panel_certificate_id: values.get("tls_mode") === "external" ? "" : values.get("panel_certificate_id") || "",
    certificate_path: tlsEnabled && !["caddy", "nginx", "external"].includes(values.get("tls_mode")) || protocol.key.startsWith("vless-xhttp-") && protocol.uses_reality && protocol.key !== "vless-xhttp-reality" ? values.get("certificate_path") || "" : "",
    private_key_path: tlsEnabled && !["caddy", "nginx", "external"].includes(values.get("tls_mode")) || protocol.key.startsWith("vless-xhttp-") && protocol.uses_reality && protocol.key !== "vless-xhttp-reality" ? values.get("private_key_path") || "" : "",
    reality_enabled: values.get("reality_enabled") === "1",
    reality_private_key: values.get("reality_private_key") || "",
    reality_public_key: values.get("reality_public_key") || "",
    reality_short_id: values.get("reality_short_id") || "",
    reality_server_name: values.get("reality_server_name") || "",
    reality_target: values.get("reality_target") || "",
    reality_server_names: values.get("reality_server_names") || "",
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
    mieru_traffic_pattern: values.get("mieru_traffic_pattern") || "off",
    mieru_custom_pattern: customPattern,
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


// Custom-pattern constraints are shared by protocol editors.
export function validateMieruNonce({mode, nonce, min, max, fixed}) {
  max?.setCustomValidity("");
  fixed?.setCustomValidity("");
  if (mode !== "custom") return;
  if (["PRINTABLE", "PRINTABLE_SUBSET"].includes(nonce) &&
      min?.value !== "" && max?.value !== "" &&
      min?.validity.valid && max?.validity.valid &&
      Number(min.value) > Number(max.value)) {
    max.setCustomValidity("最长长度不能小于最短长度。");
  }
  if (nonce === "FIXED" && fixed) {
    const prefixes = fixed.value.split(",").map(value => value.trim());
    if (prefixes.length === 1 && prefixes[0] === "") {
      fixed.setCustomValidity("请至少填写一个固定前缀。");
    } else if (prefixes.some(value => !/^(?:[0-9a-f]{2}){1,12}$/i.test(value))) {
      fixed.setCustomValidity("每个前缀须为 1–12 字节的十六进制字符，并用英文逗号分隔。");
    }
  }
}
