import { muted, stack } from "./ui.js";

// Keep the native editor's active controls aligned with the submitted protocol.
export function bindInboundTransport(form, protocol) {
  const input = name => form.elements.namedItem(name);
  const wrap = control => control?.closest(".a-field,.a-check");
  const show = (name, visible, required = false) => {
    const control = input(name);
    if (!control) return;
    wrap(control).hidden = !visible;
    control.disabled = !visible;
    control.required = visible && required;
  };
  const mode = input("tls_mode"), tls = input("tls_enabled"), listen = input("listen");
  const domain = input("tls_server_name"), publicPort = input("tls_public_port");
  if (input("tls_acme_email")) input("tls_acme_email").type = "email";
  if (input("port")) input("port").min = "1";
  if (publicPort) publicPort.min = "1";
  if (tls && protocol.requires_tls) { tls.checked = true; tls.disabled = true; }
  const hint = muted(""), route = muted("");
  if (mode) wrap(mode).closest(".a-form-grid").after(stack(hint, route));
  let previousMode = mode?.value, wasManaged = false;
  function update() {
    const kind = input("transport")?.value || "raw";
    show("transport_path", !["raw", "quic"].includes(kind), true);
    show("transport_host", ["http", "websocket", "httpupgrade", "xhttp"].includes(kind));
    show("transport_http_method", kind === "http");
    const enabled = Boolean(protocol.requires_tls || tls?.checked);
    const managed = enabled && protocol.supports_managed_tls && ["caddy", "nginx", "external"].includes(mode?.value);
    show("tls_client_certificate", enabled && (!mode || mode.value === "native" || mode.value === "nginx" && protocol.key !== "vless-ws-tls"));
    if (previousMode === "nginx" && mode?.value !== "nginx" && input("tls_client_certificate")) input("tls_client_certificate").value = "";
    previousMode = mode?.value;
    if (mode) {
      if (managed) listen.value = "127.0.0.1";
      else if (wasManaged && listen.value === "127.0.0.1") listen.value = "0.0.0.0";
      listen.readOnly = Boolean(managed);
      wasManaged = managed;
      if (domain) domain.required = Boolean(managed);
      if (managed && publicPort && (!publicPort.value || publicPort.value === "0")) publicPort.value = "443";
      show("tls_public_port", managed, true);
      publicPort?.setCustomValidity(managed && publicPort.value === input("port")?.value ? "公网端口不能与内部监听端口相同" : "");
      show("tls_acme_email", enabled && (mode.value === "caddy" || mode.value === "nginx" && protocol.key === "vless-ws-tls"));
      hint.hidden = route.hidden = !managed;
      hint.textContent = mode.value === "external" ? "自行配置公网 TLS 反代，回源到本机内部端口与路径。"
        : mode.value === "nginx" && protocol.key !== "vless-ws-tls" ? "使用自签证书；客户端需使用导出的公共证书验证。"
        : "域名需解析到节点，放行 TCP 80 和公网端口，以自动申请并续期证书。";
      route.textContent = `公网入口：${domain?.value || "待填写域名"}:${publicPort?.value || "443"}${input("transport_path")?.value || ""}；回源：${listen.value}:${input("port")?.value || ""}`;
    }
    for (const name of ["certificate_path", "private_key_path"]) {
      const control = input(name);
      if (!control) continue;
      const selected = Boolean(input("panel_certificate_id")?.value);
      control.disabled = managed || !enabled && !protocol.key.startsWith("vless-xhttp-");
      wrap(control).hidden = control.disabled || selected;
    }
    if (mode?.value === "native") wrap(mode).closest("details").open = true;
  }
  for (const name of ["transport", "tls_enabled", "tls_mode", "panel_certificate_id"]) input(name)?.addEventListener("change", update);
  for (const name of ["port", "tls_public_port", "tls_server_name", "transport_path"]) input(name)?.addEventListener("input", update);
  update();
}
