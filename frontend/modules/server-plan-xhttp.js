import { bindEvent } from "./refresh.js";
import { setVisible } from "./presence-motion.js";

const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({"&":"&amp;", "<":"&lt;", ">":"&gt;", '"':"&quot;", "'":"&#39;"})[c]);
export const isXHTTPGroup = key => ["vless-xhttp-five", "vless-xhttp-tls-reality", "vless-xhttp-reality-tls"].includes(key);
const input = (name, label, value, help = "", type = "text") => `<label>${label}<input name="${name}" type="${type}" value="${esc(value)}"${type === "number" ? ' min="1" max="65535"' : ""}>${help ? `<small>${help}</small>` : ""}</label>`;
const mode = (name, value) => `<label>客户端上传模式<select name="${name}">${["auto", "stream-one", "stream-up", "packet-up"].map(x => `<option value="${x}"${x === (value || "auto") ? " selected" : ""}>${x}</option>`).join("")}</select></label>`;
const object = value => { if (!value?.trim()) return {}; const parsed = JSON.parse(value); if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new Error("XHTTP extra 必须是 JSON 对象"); return parsed; };

export function xhttpProtocolOptions(plan, protocol) {
  if (!protocol?.transports?.includes("xhttp")) return "";
  const group = isXHTTPGroup(protocol.key);
  const tls = group || protocol.key === "vless-xhttp-tls";
  let extra = {};
  try { extra = object(plan.xhttp_extra); } catch { /* Preserve malformed source for editing. */ }
  const download = extra.downloadSettings;
  const settings = download?.xhttpSettings || {};
  const security = download?.tlsSettings || download?.realitySettings || {};
  const basic = `<details class="preset-option-panel xhttp-options" open><summary><b>${group ? "组合公共参数与客户端入口" : "XHTTP · 独立部署"}</b></summary><div class="plan-fields two">${input("transport_host", "服务端 XHTTP Host", plan.transport_host, "留空接受任意 Host；填写后两端必须完全匹配。")}${mode("xhttp_mode", plan.xhttp_mode)}${input("client_address", group ? "Reality 连接地址" : "上传连接地址", plan.client_address, "留空使用节点自动识别地址；IPv6 不附端口。")}${input("client_port", group ? "Reality 连接端口" : "客户端连接端口", plan.client_port || plan.port, "CDN 客户端端口与源站监听端口可以不同。", "number")}${input("client_host", group ? "Reality XHTTP Host（可留空）" : "上传 XHTTP Host", plan.client_host, "与地址、SNI 分开维护；CDN 需填写有效站点 Host。")}</div><p class="validation-note">${group ? "公共端口由 Xray Reality 监听；HTTP 回落与本地 Nginx TLS 目标汇入同一个 XHTTP 后端。单独混合方案只创建所需公共入口与后端；五合一额外启用 Vision 客户端。" : "Xray 直接监听并处理 XHTTP，无需安装 Nginx / Caddy。服务端 mode 固定 auto，允许客户端选择上传模式。"}</p></details>`;
  const tlsPanel = tls ? `<details class="preset-option-panel xhttp-options" open><summary><b>${group ? "TLS/CDN 入口与本地反代" : "原生 TLS · 域名与信任"}</b></summary><div class="plan-fields two">${input("tls_server_name", "TLS SNI / 证书域名（必填）", plan.tls_server_name)}${group ? input("xhttp_tls_address", "TLS/CDN 连接地址", plan.xhttp_tls_address, "留空使用 TLS SNI；可填写 CDN 域名或指定 IP。") : ""}${group ? input("xhttp_tls_client_port", "TLS/CDN 连接端口", plan.xhttp_tls_client_port || plan.client_port || plan.port, "CDN 边缘连接端口；可与源站端口不同。", "number") + input("xhttp_tls_host", "TLS/CDN XHTTP Host", plan.xhttp_tls_host || plan.tls_server_name, "与连接地址、TLS SNI 独立维护。") : ""}${input("tls_alpn", "服务端 ALPN", plan.tls_alpn || "h2,http/1.1", group ? "组合反代固定 h2。" : "逗号分隔；建议 h2,http/1.1。")}${input("tls_client_alpn", "客户端 ALPN", plan.tls_client_alpn || "h2", "建议 h2；h3 需要 CDN 支持及 UDP 可达，组合源站使用 TCP/h2。")}<label>CDN<select name="xhttp_cdn">${[["none", "直连"], ["cloudflare", "Cloudflare"], ["other", "其他 CDN"]].map(([key, label]) => `<option value="${key}"${key === (plan.xhttp_cdn || "none") ? " selected" : ""}>${label}</option>`).join("")}</select></label>${group ? input("certificate_path", "节点证书路径（必填）", plan.certificate_path) + input("private_key_path", "节点私钥路径（必填）", plan.private_key_path) : ""}</div><p class="validation-note">客户端证书验证始终开启。直连须使用客户端信任且覆盖 SNI 的证书。CDN 域名须启用代理，路径禁用缓存、允许长请求与流式传输，回源使用 HTTPS 严格验证。CDN 源站证书仅供 CDN 信任，不能代替客户端对边缘证书的验证。${group ? "组合证书须同时覆盖 Reality SNI 与 TLS SNI；证书目录 root:qcontrolhub-core 0750，私钥 0640。Nginx 使用独立受管配置，保留节点现有站点。" : "证书由节点维护，更新文件后重新部署。"}</p></details>` : "";
  const split = group ? "" : `<details class="preset-option-panel xhttp-options"${download ? " open" : ""}><summary><b>上下行分离 · 独立下载端点</b></summary><label class="tls-switch"><input type="checkbox" name="xhttp_split" value="1"${download ? " checked" : ""}><strong>启用独立下载参数</strong></label><div data-xhttp-download${download ? "" : " hidden"}><div class="plan-fields two">${input("xhttp_download_address", "下载连接地址", download?.address)}${input("xhttp_download_port", "下载连接端口", download?.port || plan.client_port || plan.port, "", "number")}${input("xhttp_download_sni", "下载 SNI", security.serverName || (tls ? plan.tls_server_name : plan.reality_server_name))}${input("xhttp_download_host", "下载 XHTTP Host", settings.host)}${tls ? input("xhttp_download_alpn", "下载 TLS ALPN", security.alpn?.join(",") || "h2") : ""}</div><button class="button small" type="button" data-xhttp-swap>交换上传与下载地址</button><p class="validation-note">两个端点须到达同一个 XHTTP 入站与相同路径。Reality 双栈需监听 ::、节点具备 IPv4/IPv6 入口且客户端两端均可达；单栈也可使用指向同一后端的两个可达入口。交换仅交换连接地址与端口，SNI / Host 仍分别维护。</p></div></details>`;
  const advanced = `<details class="preset-option-panel xhttp-options"><summary><b>XHTTP 高级参数</b></summary><div class="plan-fields two">${protocol.uses_reality && !group ? input("reality_target", "公网 Reality target", plan.reality_target, "公网域名:端口；留空使用所选 SNI:443，保留公网 DNS 与 TLS 校验。") : ""}${protocol.uses_reality ? input("reality_server_names", "允许的 Reality SNI", plan.reality_server_names || plan.reality_server_name, "逗号分隔；客户端所选 SNI 必须在此列表中。") : ""}${group ? input("xhttp_backend_port", "内部 XHTTP 端口", plan.xhttp_backend_port, "仅监听 127.0.0.1，须与所有实例端口不同。", "number") + input("xhttp_tls_port", "本地 TLS 目标端口", plan.xhttp_tls_port, "仅监听 127.0.0.1，由受管 Nginx 提供。", "number") : ""}</div><div class="plan-fields one"><label>客户端 extra（完整 JSON）<textarea name="xhttp_extra" rows="6" spellcheck="false">${esc(plan.xhttp_extra || "{}")}</textarea><small>完整保留 downloadSettings、xmux 与 sockopt。分离表单只更新对应基础字段；其余参数保留。stream-one 不支持分离；maxConcurrency 与 maxConnections 至少一项为 0。hMaxReusableSecs 请使用已验证的 26.3.27；旧 cMaxLifetimeMs 需手动换算为秒。</small></label><label>服务端 extra（JSON）<textarea name="xhttp_server_extra" rows="5" spellcheck="false">${esc(plan.xhttp_server_extra || "{}")}</textarea><small>例如 scMaxEachPostBytes、scMaxBufferedPosts、noSSEHeader。不要填写客户端 downloadSettings / xmux。</small></label></div><p class="validation-note">两端最低 Xray 24.12.15；当前验证版本 26.3.27。此版本 Reality auto：无分离使用 stream-one，有分离使用 stream-up；TLS auto 使用 packet-up。建议保持 auto，按入口能力选择显式模式。HTTP/1.1 单 ALPN 使用标准 TLS 指纹（unsafe 仅表示不用 uTLS，证书验证仍开启）。完整 Xray JSON 支持所有参数；复杂分离不降级为不完整分享链接。</p></details>`;
  return basic + tlsPanel + split + advanced;
}

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
    else Object.assign(security, {publicKey:values.get("reality_public_key") || "", shortId:values.get("reality_short_id") || ""});
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

export function bindXHTTPOptions(form) {
  const port=form.elements.namedItem("port");
  let previousPort=port?.value;
  if (form.elements.namedItem("xhttp_mode")) bindEvent(form,"input",()=>{
    if (port?.value===previousPort) return;
    for(const name of ["client_port","xhttp_tls_client_port"]) {
      const endpoint=form.elements.namedItem(name);
      if(endpoint?.value===previousPort) endpoint.value=port.value;
    }
    previousPort=port?.value;
  });
  const split = form.elements.namedItem("xhttp_split");
  const download = form.querySelector("[data-xhttp-download]");
  if (split && download) bindEvent(split, "change", () => {
    form.dataset.xhttpSplitChanged = "true";
    setVisible(download, split.checked);
  });
  const raw = form.elements.namedItem("xhttp_extra");
  if (split && download && raw) bindEvent(raw, "change", () => {
    try {
      const d = object(raw.value).downloadSettings;
      split.checked = Boolean(d);
      delete form.dataset.xhttpSplitChanged;
      setVisible(download, split.checked);
      if (d) {
        const x = d.xhttpSettings || {}, security = d.tlsSettings || d.realitySettings || {};
        for (const [name, value] of Object.entries({xhttp_download_address:d.address, xhttp_download_port:d.port,
          xhttp_download_sni:security.serverName, xhttp_download_host:x.host, xhttp_download_alpn:security.alpn?.join(",") || "h2"})) {
          const field = form.elements.namedItem(name);
          if (field) field.value = value ?? "";
        }
      }
    } catch { /* Saving reports malformed JSON without discarding text. */ }
  });
  bindEvent(form.querySelector("[data-xhttp-swap]"), "click", () => {
    for (const [up, down] of [["client_address", "xhttp_download_address"], ["client_port", "xhttp_download_port"]]) {
      const a = form.elements.namedItem(up), b = form.elements.namedItem(down);
      [a.value, b.value] = [b.value, a.value];
    }
    form.dispatchEvent(new Event("input", {bubbles:true}));
  });
}
