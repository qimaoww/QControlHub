// Managed WS/HTTPUpgrade presets follow the script's domain -> proxy ->
// loopback flow. Generated core settings remain editable in one disclosure.
export function managedTLSPlanFields(protocol, plan, esc) {
  const native = !plan.tls_mode || plan.tls_mode === "native";
  return `<div class="builder-sections managed-tls-plan">
    <section class="builder-section" id="connection">
      <header><strong>域名接入</strong></header>
      <div class="plan-fields two">
        <label><span data-tls-domain-label>域名</span><input name="tls_server_name" value="${esc(plan.tls_server_name || "")}" placeholder="例如 proxy.example.com" autocomplete="off"><small data-managed-tls-hint></small></label>
        <label data-managed-tls>公网 HTTPS 端口<input name="tls_public_port" type="number" min="1" max="65535" value="${Number(plan.tls_public_port || 443)}"><small>客户端连接此端口，默认 443，可自定义。</small></label>
      </div>
      <p class="tls-help" data-managed-tls-route></p>
    </section>
    <section class="builder-section">
      <details class="preset-option-panel" data-tls-advanced ${native ? "open" : ""}>
        <summary><b>高级设置</b><small>反代方式 · 内部端口 · 路径 · 凭据</small></summary>
        <div class="tls-settings" data-native-tls>
          <div class="plan-fields one"><label>证书方式<select name="tls_mode">
            <option value="caddy" ${plan.tls_mode === "caddy" ? "selected" : ""}>Caddy 自动证书（默认）</option>
            ${plan.transport !== "http" ? `<option value="nginx" ${plan.tls_mode === "nginx" ? "selected" : ""}>Nginx 自签证书</option>` : ""}
            <option value="external" ${plan.tls_mode === "external" ? "selected" : ""}>外部反代 · 自行管理 TLS</option>
            <option value="native" ${native ? "selected" : ""}>已有证书 · 原生 TLS</option>
          </select></label></div>
          <div class="plan-fields three">
            <label>入站标签<input name="tag" maxlength="64" required value="${esc(plan.tag)}"></label>
            <label>监听地址<input name="listen" required value="${esc(plan.listen)}"></label>
            <label><span data-listen-port-label>内部监听端口</span><input name="port" type="number" min="1" max="65535" required value="${Number(plan.port)}"></label>
          </div>
          <div class="plan-fields two">
            <label>用户名或备注<input name="username" maxlength="64" required value="${esc(plan.username)}"></label>
            <label class="secret-input">${esc(protocol.credential_label || "凭据")}<span class="secret-value-control"><input type="password" name="credential" required value="${esc(plan.credential)}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span></label>
          </div>
          <div class="plan-fields two">
            <label>传输<select name="transport"><option value="${esc(plan.transport)}">${plan.transport === "http" ? "HTTP / H2" : plan.transport === "httpupgrade" ? "HTTPUpgrade" : "WebSocket"}</option></select></label>
            <label data-transport-path>路径<input name="transport_path" value="${esc(plan.transport_path)}"><small>已自动生成，反代与客户端使用相同路径。</small></label>
            <label data-transport-host>HTTP Host<input name="transport_host" value="${esc(plan.transport_host || "")}" maxlength="253" placeholder="留空使用域名"><small>需要独立 Host 时填写；不会修改证书域名。</small></label>
            ${plan.transport === "http" ? `<label data-http-method>HTTP 方法<input name="transport_http_method" value="${esc(plan.transport_http_method || "")}" placeholder="留空使用 PUT"></label>` : ""}
            <label>ALPN<input name="tls_alpn" value="${esc(plan.tls_alpn || "")}" placeholder="留空使用传输默认值"></label>
          </div>
          <div class="plan-fields one" data-caddy-acme><label>ACME 邮箱（可选覆盖）<input name="tls_acme_email" type="email" maxlength="254" value="${esc(plan.tls_acme_email || "")}" placeholder="留空使用本账号系统设置中的邮箱"><small>预设值优先于账号默认值；两处均未设置时仅尝试 Let’s Encrypt。填写邮箱可启用 ZeroSSL 自动账户绑定与回退。</small></label></div>
          <div class="plan-fields two" data-native-certificate>
            <label>证书路径<input name="certificate_path" value="${esc(plan.certificate_path || "")}"></label>
            <label>私钥路径<input name="private_key_path" value="${esc(plan.private_key_path || "")}"></label>
          </div>
          <div class="plan-fields one" data-native-certificate><label>客户端信任证书（可选 PEM）<textarea name="tls_client_certificate" rows="4" placeholder="留空使用系统信任根；自签证书填入公共证书">${esc(plan.tls_client_certificate || "")}</textarea><small>仅填写公共证书；指定后使用 sing-box JSON 导出。</small></label></div>
        </div>
      </details>
    </section>
    <input type="checkbox" name="tls_enabled" value="1" checked disabled hidden>
    <input type="hidden" name="reality_enabled" value="0">
    <input type="hidden" name="secondary_credential" value="">
    <input type="hidden" name="method" value="">
    <input type="hidden" name="flow" value="">
  </div>`;
}
