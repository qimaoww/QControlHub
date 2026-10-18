import { coreInstallBlockReason } from "./core-install-state.js";
import { managedTLSPlanFields } from "./server-plan-managed-tls.js";
import { xhttpProtocolOptions, isXHTTPGroup } from "./server-plan-xhttp.js";
import { protocolNavigationNames, snellProtocolOptions, sudokuProtocolOptions, wireguardProtocolOptions } from "./server-plan-form.js";
import { mieruProtocolOptions } from "./server-plan-mieru.js";
import { renderEmbeddedPreset } from "./config-inbounds.js";
import { renderPresetIdentity } from "./preset-runtime.js";
import { renderCommonFieldStudio, renderConfigSourceStudio, renderGlobalFieldStudio } from "./config-fields.js";
import { renderSSRustFieldStudio, ssRustPlanBinding } from "./ss-rust-fields.js";

export function createPresetView({ state, can, esc, engineName, shell }) {
  return ({ agent, engine, workspace, config, protocol, plan, selectedProtocolKey, selectedInbound,
    fields, ssRustGroups, selectedField, selectedInboundField, fieldValue, inboundFieldValue,
    commonMutation, engineInstalled, host }) => {
  const inboundNav = (workspace.inbounds || [])
    .map(
      (input) =>
        `<a class="${selectedInbound?.tag === input.tag ? "active" : ""}" href="#agent-config" data-inbound="${esc(input.tag)}"><span><strong>${esc(input.tag)}</strong><small>${esc(input.listen)}:${input.port}</small></span></a>`,
    )
    .join("");
  const protocolNav = workspace.protocols
    .map(
      (item) =>
        `<a class="${item.key === selectedProtocolKey ? "active" : ""}" href="#agent-config" data-protocol="${esc(item.key)}" data-protocol-group="${esc(item.group || "")}" title="${esc(item.name)}"><b>${esc(item.badge)}</b><span><strong>${esc(protocolNavigationNames[item.key] || item.name)}</strong></span></a>`,
    )
    .join("");
  const methods = (protocol?.methods || [])
    .map(
      (method) =>
        `<option value="${esc(method)}" ${method === plan.method ? "selected" : ""}>${esc(method)}</option>`,
    )
    .join("");
  const transports = (protocol?.transports || ["raw"])
    .map(
      (transport) =>
        `<option value="${esc(transport)}" ${transport === plan.transport ? "selected" : ""}>${transport === "raw" ? "Raw / TCP" : transport === "websocket" ? "WebSocket" : ({xhttp:"XHTTP",grpc:"gRPC",http:"HTTP / H2",httpupgrade:"HTTPUpgrade",quic:"QUIC"})[transport] || transport}</option>`,
    )
    .join("");
  const portForward = Boolean(protocol?.port_forward);
  const protocolOptions =
    engine === "ss-rust"
      ? `<details class="preset-option-panel" open><summary><b>当前端口 · 出站绑定</b></summary><div class="plan-fields one"><label>出站绑定 IP<input name="ss_rust_outbound_bind_addr" value="${esc(ssRustPlanBinding(plan, config, selectedInbound))}" placeholder="留空继承全局绑定"><small>须为节点本机 IPv4 / IPv6；留空删除端口覆盖，不禁用全局绑定。DNS、IPv6 优先与性能项在「全局与默认值」单独保存。</small></label></div></details>`
      : selectedProtocolKey === "mieru"
        ? mieruProtocolOptions(plan)
      : selectedProtocolKey === "snell"
      ? snellProtocolOptions(plan, false)
      : selectedProtocolKey === "snell-shadow-tls-v3"
        ? snellProtocolOptions(plan, true)
      : selectedProtocolKey === "sudoku"
        ? sudokuProtocolOptions(plan)
        : protocol?.uses_wireguard
          ? wireguardProtocolOptions(plan, protocol.uses_endpoint)
        : "";
  const vlessEncryptionOptions = protocol?.uses_vless_encryption
    ? `<div class="plan-fields one"><label class="secret-input">服务端 VLESS Decryption<span class="secret-value-control"><input type="password" name="vless_decryption" required value="${esc(plan.vless_decryption || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span><small>X25519 服务端私有值。</small></label><label>客户端 VLESS Encryption<input name="vless_encryption" required value="${esc(plan.vless_encryption || "")}"><small>客户端连接使用的公开值。</small></label></div>`
    : '<input type="hidden" name="vless_decryption" value=""><input type="hidden" name="vless_encryption" value="">';
  const identitySection = `<section class="builder-section" id="target"><header><span class="section-number">02</span><strong>转发目标</strong></header><div class="tls-settings"><div class="plan-fields three"><label>目标地址<input name="target_address" maxlength="253" required value="${esc(plan.target_address)}" placeholder="127.0.0.1 或 target.example.com"></label><label>目标端口<input type="number" name="target_port" min="1" max="65535" required value="${Number(plan.target_port)}"></label><label>转发协议<select name="network"><option value="tcp" ${plan.network === "tcp" ? "selected" : ""}>TCP</option><option value="udp" ${plan.network === "udp" ? "selected" : ""}>UDP</option><option value="tcp,udp" ${plan.network === "tcp,udp" ? "selected" : ""}>TCP + UDP</option></select></label></div><input type="hidden" name="username" value=""><input type="hidden" name="credential" value=""><input type="hidden" name="secondary_credential" value=""><input type="hidden" name="method" value=""></div></section>`;
  const xrayRealityAdvanced = engine === "xray"
    ? `<div class="plan-fields one"><label>最低客户端 Xray 版本<input name="reality_min_client_ver" required pattern="[0-9]{1,3}(\\.[0-9]{1,3}){2}" value="${esc(plan.reality_min_client_ver || "0.0.0")}"><small>0.0.0 不限版本；低于此版本的客户端会被拒绝。</small></label></div><div class="plan-fields one"><label class="secret-input">ML-DSA-65 Seed（可选，仅服务端）<span class="secret-value-control"><input type="password" name="reality_mldsa65_seed" value="${esc(plan.reality_mldsa65_seed || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span><small>xray mldsa65 生成，自动推导客户端 Verify；要求 target 证书链 &gt; 3500 bytes 且支持 X25519MLKEM768。</small></label></div>`
    : '<input type="hidden" name="reality_min_client_ver" value="0.0.0"><input type="hidden" name="reality_mldsa65_seed" value="">';
  const certificateFields = `<div class="plan-fields two" data-native-certificate><label>证书路径<input name="certificate_path" value="${esc(plan.certificate_path)}"></label><label>私钥路径<input name="private_key_path" value="${esc(plan.private_key_path)}"></label></div>`;
  const nativeTLS = engine === "sing-box" ? `<div class="tls-settings" data-native-tls><div class="plan-fields two"><label>证书域名 / TLS SNI<input name="tls_server_name" value="${esc(plan.tls_server_name || "")}" placeholder="例如 tls.example.com"><small>填写证书覆盖的域名；连接地址和 HTTP Host 分别设置。</small></label><label>ALPN<input name="tls_alpn" value="${esc(plan.tls_alpn || "")}" placeholder="留空使用传输默认值"></label></div>${certificateFields}<div class="plan-fields one" data-native-certificate><label>客户端信任证书（可选 PEM）<textarea name="tls_client_certificate" rows="4" placeholder="留空使用系统信任根；自签证书填入公共证书">${esc(plan.tls_client_certificate || "")}</textarea><small>仅填写公共证书，用于客户端验证；指定后使用 sing-box JSON 导出。</small></label></div><p class="tls-help" data-native-certificate>填写节点上的证书与私钥路径，私钥仅目标内核服务组可读。证书文件续期替换后由 sing-box 自动重载。</p></div>` : "";
  const flowOptions = selectedProtocolKey === "vless-tls" ? `<label>Flow<select name="flow"><option value="">无</option><option value="xtls-rprx-vision" ${plan.flow === "xtls-rprx-vision" ? "selected" : ""}>Vision（仅 TCP）</option></select></label>` : "";
  const nativeTransport = engine === "sing-box" ? `<label data-transport-host>HTTP Host<input name="transport_host" value="${esc(plan.transport_host || "")}" maxlength="253" placeholder="可选，例如 cdn.example.com"><small>HTTP / H2 与 HTTPUpgrade 服务端会校验此值。</small></label><label data-http-method>HTTP Method<input name="transport_http_method" value="${esc(plan.transport_http_method || "")}" placeholder="留空使用 PUT" maxlength="16" pattern="[A-Z]+"></label>` : "";
  const security = protocol?.uses_reality
    ? `<input type="hidden" name="reality_enabled" value="1"><section class="builder-section" id="security"><header><span class="section-number">04</span><strong>Reality</strong></header><div class="tls-settings"><div class="plan-fields two"><label>目标域名 / ServerName<input name="reality_server_name" list="reality-presets" required value="${esc(plan.reality_server_name)}"><datalist id="reality-presets">${workspace.reality_presets.map((value) => `<option value="${esc(value)}">`).join("")}</datalist><small>${isXHTTPGroup(selectedProtocolKey) ? "组合本地目标的 SNI，证书必须覆盖；本地目标由面板维护。" : "校验公网 DNS；拒绝 Cloudflare 与非公网地址。"}</small></label><label>Short ID<input name="reality_short_id" value="${esc(plan.reality_short_id)}"></label></div><div class="plan-fields one"><label>客户端 Public Key<input name="reality_public_key" required value="${esc(plan.reality_public_key)}"></label><label class="secret-input">服务端 Private Key<span class="secret-value-control"><input type="password" name="reality_private_key" required value="${esc(plan.reality_private_key)}"><button type="button" data-secret-visibility>显示</button></span></label></div>${engine === "sing-box" ? `<div class="plan-fields one"><label>ALPN<input name="tls_alpn" value="${esc(plan.tls_alpn || "")}" placeholder="留空使用传输默认值"></label></div>` : ""}${xrayRealityAdvanced}</div></section>`
    : protocol?.supports_tls
      ? `<input type="hidden" name="reality_enabled" value="0"><section class="builder-section" id="security"><header><span class="section-number">04</span><strong>TLS</strong></header><div class="tls-settings"><div ${engine === "sing-box" && protocol.requires_tls ? "hidden" : ""}><label class="plan-check"><input type="checkbox" name="tls_enabled" value="1" ${plan.tls_enabled || protocol.requires_tls ? "checked" : ""} ${protocol.requires_tls ? "disabled" : ""}><span><b>${protocol.requires_tls ? "TLS" : "启用 TLS"}</b></span></label></div>${engine === "sing-box" ? nativeTLS : `${certificateFields}<p class="tls-help" data-native-certificate>填写节点上的证书与私钥路径；私钥须允许内核服务组读取。</p>`}</div></section>`
      : '<input type="hidden" name="reality_enabled" value="0"><input type="hidden" name="tls_enabled" value="0">';
  const sourceStudio = renderConfigSourceStudio({ config, catalog: workspace.catalog, engineInstalled,
    open: state.data.settings?.default_config_editor === "source", ssRust: engine === "ss-rust" });
  const revisionTimeline = config
    ? `<details class="revision-timeline node-revision-timeline" id="revisions"><summary><b>版本历史</b><strong>当前 v${config.version}</strong></summary><div class="timeline-body" data-revision-body>展开加载版本历史</div></details>`
    : "";
  const installReason = coreInstallBlockReason(agent, engine, can, { automatic: true });
  const canAutoInstall = !engineInstalled && !installReason;
  const executionCallout = !engineInstalled
    ? `<aside class="config-execution-callout"><span><b>${esc(engineName(engine))} 尚未安装</b><small>${commonMutation ? "通用配置提交需要已安装内核。增加入站可自动安装稳定版；管理内核版本请到节点设置。" : canAutoInstall ? agent.can_manage === false ? "增加入站时将受控准备稳定版内核，再校验或部署您的独立实例；宿主机版本由所有者管理。" : "增加入站并提交时，将自动安装最新稳定版，再执行校验或部署。切换版本请到节点设置。" : esc(installReason)}</small></span><a class="button small" href="#node-${esc(agent.id)}">节点设置</a></aside>`
    : agent.runtime?.[engine]?.existing_config_unsupported_reason
      ? `<aside class="config-execution-callout"><span><b>当前内核暂不可提交任务</b><small>${esc(agent.runtime[engine].existing_config_unsupported_reason)}</small></span><a class="button small" href="#node-settings">检查节点配置</a></aside>`
      : agent.status !== "online"
        ? '<aside class="config-execution-callout"><span><b>节点当前离线</b><small>可以继续编辑草稿，节点上线并重新加载后可校验或部署。</small></span></aside>'
        : !can("agent-config.write") || !can("tasks.execute")
          ? '<aside class="config-execution-callout"><span><b>当前账号不可提交预设</b><small>保存并执行需要配置写入和任务执行权限。</small></span></aside>' : "";
  const advancedStudio = commonMutation
    ? renderCommonFieldStudio({ engine, fields, selected:selectedField, value:fieldValue, config,
        catalog:workspace.catalog, mutation:commonMutation })
    : engine === "ss-rust"
    ? renderSSRustFieldStudio({ scope: "inbound", fields: ssRustGroups.inbound, selected: selectedInboundField,
        value: inboundFieldValue, config, inbound: selectedInbound }) +
      renderSSRustFieldStudio({ scope: "global", fields, selected: selectedField, value: fieldValue,
        config, presentFields: workspace.present_fields }) + sourceStudio
    : renderGlobalFieldStudio({ fields, selected: selectedField, value: fieldValue, config,
        catalog: workspace.catalog, presentFields: workspace.present_fields }) + sourceStudio;
  (host ? (markup, _title, options) => renderEmbeddedPreset(host, markup, options) : shell)(
    renderPresetIdentity(`<section class="config-command-bar loaded"><header class="config-command-head"><div class="config-command-title"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><div><h2>${esc(protocol?.name || "Protocol")} · ${selectedInbound ? esc(selectedInbound.tag) : "新增入站"}</h2><small>${esc(agent.name)} · ${esc(workspace.catalog.name)}</small></div></div><div class="config-command-state"><button class="button small" type="button" data-refresh-preset>刷新状态</button><span class="status-label ${!engineInstalled ? "muted" : config ? "ok" : "warn"}">${!engineInstalled ? "内核未安装" : config ? "已读取" : "新方案"}</span><span class="recipe-version"><b>${config ? `v${config.version}` : "草稿"}</b><small>${esc(workspace.catalog.format)}</small></span><a href="${esc(protocol?.docs)}" target="_blank" rel="noopener noreferrer">文档 ↗</a></div></header><details class="config-hierarchy-menu" open><summary><b>切换入站 / 协议</b><i>＋</i></summary><div class="config-command-selectors${workspace.protocols.length > 5 ? " protocol-catalog-wide" : ""}">${inboundNav ? `<section class="inbound-browser config-selector"><header><span><b>入站</b><small>${workspace.inbounds.length} 个</small></span><button class="button small" type="button" data-new-inbound>＋ 新增</button></header><nav>${inboundNav}</nav></section>` : ""}<section class="protocol-browser config-selector"><header><span><b>协议</b><small>${workspace.protocols.length} 种</small></span></header><nav>${protocolNav}</nav></section></div></details></section>${executionCallout}<article class="recipe-workspace"><form class="server-form" id="server-plan-form"><div class="config-mutation"><label>操作<select name="operation">${selectedInbound ? `<option value="modify">修改 · ${esc(selectedInbound.tag)}</option><option value="add">新增入站</option><option value="delete">删除 · ${esc(selectedInbound.tag)}</option>` : '<option value="add">新增入站</option>'}</select></label></div>${engine === "sing-box" && protocol?.supports_managed_tls ? managedTLSPlanFields(protocol, plan, esc) : `<div class="builder-layout" data-builder-workbench><nav class="builder-index"><a href="#listen" data-builder-step="listen"><b>01</b><strong>监听</strong></a><a href="#identity" data-builder-step="identity"><b>02</b><strong>认证</strong></a>${protocol?.transport_config ? '<a href="#transport" data-builder-step="transport"><b>03</b><strong>传输</strong></a>' : ""}${protocol?.uses_reality || protocol?.supports_tls ? '<a href="#security" data-builder-step="security"><b>04</b><strong>安全</strong></a>' : ""}</nav><div class="builder-sections"><section class="builder-section" id="listen"><header><span class="section-number">01</span><strong>监听</strong></header><div class="tls-settings"><div class="plan-fields three"><label>入站标签<input name="tag" maxlength="64" required value="${esc(plan.tag)}"></label><label>监听地址<input name="listen" required value="${esc(plan.listen)}"></label><label><span data-listen-port-label>监听端口</span><input type="number" name="port" min="1" max="65535" required value="${Number(plan.port)}"></label></div></div></section><section class="builder-section" id="identity"><header><span class="section-number">02</span><strong>认证</strong></header><div class="tls-settings"><div class="plan-fields two">${protocol?.ignores_username ? '<input type="hidden" name="username" value="default">' : `<label>${selectedProtocolKey === "mieru" ? "用户名" : "用户名或备注"}<input name="username" maxlength="64" required value="${esc(plan.username)}"></label>`}${protocol?.uses_wireguard ? '<input type="hidden" name="credential" value="">' : `<label class="secret-input">${esc(protocol?.credential_label || "凭据")}<span class="secret-value-control"><input type="password" name="credential" required value="${esc(plan.credential)}"><button type="button" data-secret-visibility>显示</button></span></label>`}${protocol?.secondary_credential_label ? `<label class="secret-input">${esc(protocol.secondary_credential_label)}<span class="secret-value-control"><input type="password" name="secondary_credential" required value="${esc(plan.secondary_credential)}"><button type="button" data-secret-visibility>显示</button></span></label>` : '<input type="hidden" name="secondary_credential" value="">'}</div>${methods ? `<div class="plan-fields one"><label>加密方式<select name="method">${methods}</select></label></div>` : '<input type="hidden" name="method" value="">'}</div></section>${protocol?.transport_config ? `<section class="builder-section" id="transport"><header><span class="section-number">03</span><strong>传输</strong></header><div class="tls-settings"><div class="plan-fields two"><label>传输<select name="transport">${transports}</select></label><label data-transport-path>路径 / gRPC 服务名<input name="transport_path" value="${esc(plan.transport_path)}"></label>${nativeTransport}${flowOptions}</div></div></section>` : '<input type="hidden" name="transport" value="raw"><input type="hidden" name="transport_path" value="">'}${security}</div></div>`}<footer class="builder-actions compact"><span class="builder-regenerate-status" data-regenerate-status role="status" aria-live="polite"></span><div><button class="button" type="button" data-regenerate>重新生成参数</button><button class="button" type="submit" data-plan-intent="validate" ${agent.status !== "online" || !engineInstalled ? "disabled" : ""}>保存并校验</button><button class="button primary" type="submit" data-plan-intent="deploy" ${agent.status !== "online" || !engineInstalled ? "disabled" : ""}>保存并部署</button></div></footer></form></article>${revisionTimeline}${advancedStudio}`, protocolOptions + vlessEncryptionOptions + (engine === "xray" ? xhttpProtocolOptions(plan, protocol) : ""), portForward ? identitySection : ""),
    "节点配置",
    {
      viewKey: `agent-config-${agent.id}-${engine}-${selectedProtocolKey}-${selectedInbound?.tag || "new"}-${config?.version || 0}-${state.data.presetDraftReset || 0}`,
      commonFields: commonMutation ? fields : [],
      selectedField,
    },
  );

    return { canAutoInstall };
  };
}
