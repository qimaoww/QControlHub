import { setVisible, updateFeedback } from "./presence-motion.js";
import { bindOutboundPresetController } from "./outbound-preset-controller.js";
export { outboundProtocols, outboundPreset, outboundPeers } from "./outbound-preset-model.js";

export function bindOutboundPresets(options) {
  const {engine} = options;
  const host = document.createElement("div");
  host.className = "config-outbound-presets";
  host.innerHTML = `<label class="field-value-label">出站来源<select data-outbound-mode><option value="preset">填写协议预设</option><option value="node">选择其他节点入站</option><option value="json">高级 JSON</option></select></label>
    <fieldset data-outbound-builder><div class="config-outbound-fields"><label>出站标签<input data-outbound-value="tag" autocomplete="off" maxlength="100"></label>
      <label data-outbound-protocol-label>协议预设<select data-outbound-protocol></select></label></div>
      <div data-outbound-manual class="config-outbound-fields">
        <label data-preset-server>服务器地址<input data-outbound-value="server" autocomplete="off" placeholder="域名或 IP"></label>
        <label data-preset-server>端口<input data-outbound-value="port" type="number" min="1" max="65535" value="443"></label>
        <label data-preset-credential><span data-outbound-credential-label>密码</span><input data-outbound-value="credential" type="password" autocomplete="new-password" spellcheck="false"></label>
        <label data-preset-username>用户名（可选）<input data-outbound-value="username" autocomplete="off"></label>
        <label data-preset-password>认证密码<input data-outbound-value="password" type="password" autocomplete="new-password"></label>
        <label data-preset-method>加密方法<select data-outbound-value="method">
          <option>aes-128-gcm</option><option>aes-256-gcm</option><option>chacha20-ietf-poly1305</option>
          <option>2022-blake3-aes-128-gcm</option><option>2022-blake3-aes-256-gcm</option><option>2022-blake3-chacha20-poly1305</option></select></label>
        <label data-preset-transport>传输<select data-outbound-value="transport"><option value="tcp">TCP</option><option value="ws">WebSocket</option><option value="grpc">gRPC</option>${engine === "xray" ? '<option value="xhttp">XHTTP</option>' : ""}</select></label>
        <label data-preset-path>路径 / ServiceName<input data-outbound-value="path" autocomplete="off" placeholder="/"></label>
        <label data-preset-ws-host>WS Host<input data-outbound-value="host" autocomplete="off" placeholder="留空使用 TLS ServerName 或服务器地址"></label>
        <label data-preset-security>传输安全<select data-outbound-value="security"><option value="none">无 TLS</option><option value="tls">TLS</option><option value="reality">Reality</option></select></label>
        <label data-preset-tls>ServerName<input data-outbound-value="serverName" autocomplete="off" placeholder="留空使用服务器地址"></label>
        <label data-preset-reality>Reality 公钥<input data-outbound-value="publicKey" autocomplete="off" spellcheck="false"></label>
        <label data-preset-reality>Reality Short ID<input data-outbound-value="shortId" autocomplete="off" maxlength="16"></label>
        <label data-preset-flow>Flow<select data-outbound-value="flow"><option value="">不设置</option><option value="xtls-rprx-vision">xtls-rprx-vision</option></select></label>
      </div>
      <div data-outbound-node hidden><div class="config-outbound-fields"><label>出口节点<select data-outbound-node-select aria-label="出口节点"></select></label><label>目标入站<select data-outbound-peer-select aria-label="目标入站"></select></label></div>
        <p class="field-editor-note" data-outbound-peer-status role="status"></p><button class="button small" type="button" data-outbound-peer-reload>刷新可用入站</button>
        <p class="field-editor-note">只使用有权读取的已部署入站；仅复制连接参数，不修改目标节点，也不自动跟随后续变更。请避免节点间循环转发。</p></div>
    </fieldset>`;
  return bindOutboundPresetController({...options,host,visibility:setVisible,feedback:updateFeedback});
}
