import { validateMieruNonce } from "../modules/inbound-options-model.js";
import { isXHTTPGroup } from "../modules/inbound-options-model.js";
import { actions, button, field, node, muted } from "./ui.js";

const grid = (...fields) => node("div", { className: "a-form-grid" }, fields);
const group = (title, ...content) => node("details", { className: "a-disclosure", open: true }, node("summary", {}, title), node("div", { className: "a-disclosure-body a-stack" }, content));

export function outboundPresetFields(engine) {
  const mark = (control, selector) => { control.setAttribute(selector, ""); return control; };
  const input = (label, key, value = "", options = {}, selector) => {
    const wrap = field(label, `outbound_${key}`, value, options);
    wrap.querySelector("input,select").dataset.outboundValue = key;
    if (selector) mark(wrap, selector);
    return wrap;
  };
  const picker = (label, name, selector, options = []) => {
    const wrap = field(label, name, "", { options });
    mark(wrap.querySelector("select"), selector); return wrap;
  };
  const credential = input("密码", "credential", "", { type: "password", autocomplete: "new-password" }, "data-preset-credential");
  mark(credential.querySelector("label"), "data-outbound-credential-label");
  const protocol = picker("协议预设", "outbound_protocol", "data-outbound-protocol");
  mark(protocol, "data-outbound-protocol-label");
  return node("div", { className: "a-stack" },
    picker("出站来源", "outbound_mode", "data-outbound-mode", [["preset", "填写协议预设"], ["node", "选择其他节点入站"], ["json", engine === "mihomo" ? "高级 YAML" : "高级 JSON"]]),
    node("fieldset", { className: "a-form-fields a-stack", "data-outbound-builder": "" },
      grid(input("出站标签", "tag", "", { maxlength: 100 }), protocol),
      mark(grid(
        input("服务器地址", "server", "", { placeholder: "域名或 IP" }, "data-preset-server"),
        input("端口", "port", 443, { type: "number", min: 1, max: 65535 }, "data-preset-server"), credential,
        input("用户名（可选）", "username", "", {}, "data-preset-username"),
        input("认证密码", "password", "", { type: "password", autocomplete: "new-password" }, "data-preset-password"),
        input("加密方法", "method", "aes-128-gcm", { options: ["aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"] }, "data-preset-method"),
        input("传输", "transport", "tcp", { options: [["tcp", "TCP"], ["ws", "WebSocket"], ["grpc", "gRPC"], ...(engine === "xray" ? [["xhttp", "XHTTP"]] : [])] }, "data-preset-transport"),
        input("路径 / ServiceName", "path", "", { placeholder: "/" }, "data-preset-path"),
        input("WS Host", "host", "", { placeholder: "留空使用 TLS ServerName 或服务器地址" }, "data-preset-ws-host"),
        input("传输安全", "security", "none", { options: [["none", "无 TLS"], ["tls", "TLS"], ["reality", "Reality"]] }, "data-preset-security"),
        input("ServerName", "serverName", "", {}, "data-preset-tls"),
        input("Reality 公钥", "publicKey", "", { spellcheck: false }, "data-preset-reality"),
        input("Reality Short ID", "shortId", "", { maxlength: 16 }, "data-preset-reality"),
        input("Flow", "flow", "", { options: [["", "不设置"], ["xtls-rprx-vision", "xtls-rprx-vision"]] }, "data-preset-flow"),
      ), "data-outbound-manual"),
      node("div", { className: "a-stack", "data-outbound-node": "", hidden: true },
        grid(picker("出口节点", "outbound_node", "data-outbound-node-select"), picker("目标入站", "outbound_peer", "data-outbound-peer-select")),
        node("p", { className: "a-muted", "data-outbound-peer-status": "", role: "status" }),
        actions(button("刷新可用入站", () => {}, { "data-outbound-peer-reload": "" })),
        muted("复制有权读取的已部署入站连接参数；不会修改目标节点或自动跟随变更。请避免循环转发。"),
      ),
    ),
  );
}

export function protocolOptions(form, protocol, plan) {
  const sections = [];
  if (protocol.transports?.includes("xhttp") && !isXHTTPGroup(protocol.key)) {
    let extra = {};
    try { extra = JSON.parse(plan.xhttp_extra || "{}"); } catch { /* The source editor preserves invalid input for correction. */ }
    const download = extra.downloadSettings, settings = download?.xhttpSettings || {};
    const security = download?.tlsSettings || download?.realitySettings || {};
    const tls = protocol.key === "vless-xhttp-tls";
    const toggle = field("启用独立下载参数", "xhttp_split", Boolean(download), { type: "checkbox", value: "1" });
    const area = grid(
      field("下载连接地址", "xhttp_download_address", download?.address || ""),
      field("下载连接端口", "xhttp_download_port", download?.port || plan.client_port || plan.port, { type: "number", min: 1, max: 65535 }),
      field("下载 SNI", "xhttp_download_sni", security.serverName || (tls ? plan.tls_server_name : plan.reality_server_name) || ""),
      field("下载 XHTTP Host", "xhttp_download_host", settings.host || ""),
      tls && field("下载 TLS ALPN", "xhttp_download_alpn", security.alpn?.join(",") || "h2"),
    );
    const swap = button("交换上传与下载地址", () => {
      for (const [up, down] of [["client_address", "xhttp_download_address"], ["client_port", "xhttp_download_port"]]) {
        const a = form.elements.namedItem(up), b = form.elements.namedItem(down);
        if (a && b) [a.value, b.value] = [b.value, a.value];
      }
      form.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const update = () => { area.hidden = swap.hidden = !toggle.querySelector("input").checked; };
    toggle.addEventListener("change", () => { form.dataset.xhttpSplitChanged = "true"; update(); });
    const panel = group("XHTTP 下载分离", toggle, area, actions(swap), muted("两端使用同一入站与路径；stream-one 不支持下载分离。"));
    update(); sections.push(panel);
    // The raw JSON and structured controls edit the same downloadSettings.
    const raw = form.elements.namedItem("xhttp_extra");
    raw?.addEventListener("change", () => {
      try {
        const d = JSON.parse(raw.value || "{}").downloadSettings;
        toggle.querySelector("input").checked = Boolean(d);
        delete form.dataset.xhttpSplitChanged;
        if (d) {
          const x = d.xhttpSettings || {}, s = d.tlsSettings || d.realitySettings || {};
          for (const [name, value] of Object.entries({ xhttp_download_address: d.address, xhttp_download_port: d.port,
            xhttp_download_sni: s.serverName, xhttp_download_host: x.host, xhttp_download_alpn: s.alpn?.join(",") || "h2" })) {
            const control = panel.querySelector(`[name=${name}]`); if (control) control.value = value ?? "";
          }
        }
        update();
      } catch { /* Saving reports malformed JSON and retains the draft. */ }
    });
  }
  if (protocol.key === "mieru") {
    const custom = plan.mieru_custom_pattern || {};
    const controls = [
      field("启用 TCP 分片", "mieru_custom_tcp_fragment", custom.tcp_fragment ?? true, { type: "checkbox" }),
      field("最大等待（毫秒）", "mieru_custom_max_sleep_ms", custom.max_sleep_ms ?? 8, { type: "number", min: 0, max: 100 }),
      field("Nonce 类型", "mieru_custom_nonce_type", custom.nonce_type || "PRINTABLE", { options: [["RANDOM", "随机"], ["PRINTABLE", "可打印字符"], ["PRINTABLE_SUBSET", "可打印字符子集"], ["FIXED", "固定前缀"]] }),
      field("应用于每个 UDP 包", "mieru_custom_nonce_apply_to_all_udp", custom.nonce_apply_to_all_udp ?? true, { type: "checkbox" }),
      ...[["nonce_min_len", "最短长度（字节）", 6, 12], ["nonce_max_len", "最长长度（字节）", 8, 12], ["padding_middle", "中间填充上限（字节）", 64, 255], ["padding_end", "末尾填充上限（字节）", 128, 255]].map(([key, label, fallback, max]) => field(label, `mieru_custom_${key}`, custom[key] ?? fallback, { type: "number", min: 0, max })),
      field("固定前缀（Hex）", "mieru_custom_nonce_fixed_hex", custom.nonce_fixed_hex || "", { placeholder: "多个值用逗号分隔，每个最多 12 字节" }),
      field("低熵处理", "mieru_custom_low_entropy_mode", custom.low_entropy_mode ?? 56, { options: [[0, "关闭"], ...[32, 40, 48, 56].map(value => [value, String(value)])] }),
      field("掩码旋转", "mieru_custom_mask_rotation", custom.mask_rotation || "right_7", { options: [["none", "不旋转"], ...["right", "left"].flatMap(direction => Array.from({ length: 15 }, (_, index) => [`${direction}_${index + 1}`, `${direction === "right" ? "右移" : "左移"} ${index + 1} 位`]))] }),
    ];
    const panel = group("Mieru 自定义流量模式", grid(...controls));
    const update = () => {
      panel.hidden = form.elements.namedItem("mieru_traffic_pattern")?.value !== "custom";
      const current = name => panel.querySelector(`[name=mieru_custom_${name}]`);
      const nonce = current("nonce_type").value;
      for (const name of ["nonce_min_len", "nonce_max_len"]) current(name).closest(".a-field,.a-check").hidden = !["PRINTABLE", "PRINTABLE_SUBSET"].includes(nonce);
      current("nonce_fixed_hex").closest(".a-field,.a-check").hidden = nonce !== "FIXED";
      current("max_sleep_ms").closest(".a-field,.a-check").hidden = !current("tcp_fragment").checked;
      current("mask_rotation").closest(".a-field,.a-check").hidden = current("low_entropy_mode").value === "0";
      for (const control of panel.querySelectorAll("input,select")) {
        const visible = !panel.hidden && !control.closest(".a-field,.a-check").hidden;
        control.disabled = !visible;
        if (control.type === "number") control.required = visible;
      }
      current("nonce_fixed_hex").required = !panel.hidden && nonce === "FIXED";
      validateMieruNonce({mode:form.elements.namedItem("mieru_traffic_pattern")?.value, nonce,
        min:current("nonce_min_len"), max:current("nonce_max_len"), fixed:current("nonce_fixed_hex")});
    };
    panel.addEventListener("input", update);
    panel.addEventListener("change", update);
    form.elements.namedItem("mieru_traffic_pattern")?.addEventListener("input", update);
    form.elements.namedItem("mieru_traffic_pattern")?.addEventListener("change", update);
    update(); sections.push(panel);
  }
  return sections;
}
