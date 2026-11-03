import { refreshSelectMenu } from "./select-menu.js";
import { preparePresetFields } from "./preset-runtime.js";
const prefix = "/etc/qcontrolhub/panel-certificates/";
// Install into the existing protocol form so create/edit and every theme use
// the same controls, permissions, draft lifecycle and submission path.
export async function bindCertificateSelector({ api, form, protocol, plan, visible }) {
  if (!form || protocol?.uses_reality || protocol?.uses_wireguard || !(protocol?.supports_tls || protocol?.requires_tls) || !form.elements.namedItem("certificate_path") || form.querySelector("[data-panel-certificate]")) return;
  const field = document.createElement("div");
  field.dataset.panelCertificate = "1";
  field.className = "plan-fields one";
  const label = document.createElement("label"); label.textContent = "证书来源";
  const select = document.createElement("select"); select.name = "panel_certificate_id";select.setAttribute("aria-label","证书来源");
  const local = new Option("节点已有证书 / 原有 TLS 方式", ""); select.add(local);
  label.append(select); field.append(label);
  const summary = document.createElement("p"); summary.className="settings-hint";summary.dataset.panelCertificateSummary="1";summary.setAttribute("role","status");field.append(summary);
  let sniLabel;
  if (!form.elements.namedItem("tls_server_name")) { sniLabel=document.createElement("label");sniLabel.textContent="TLS SNI / 证书域名";const sni=document.createElement("input");sni.name="tls_server_name";sni.value=plan?.tls_server_name||"";sni.placeholder="例如 edge.example.com";sniLabel.append(sni);field.append(sniLabel); }
  const target = form.querySelector("[data-native-certificate]") || form.querySelector("[data-native-tls]");
  if (!target) return; target.before(field);
  if (form.closest(".config-inbound-dialog")) preparePresetFields(field);
  if (protocol.key === "vless-ws-tls") {
    const mode = form.elements.namedItem("tls_mode");
    const updateMode = () => {
      field.hidden = ["caddy", "nginx", "external"].includes(mode?.value);
      select.disabled = field.hidden;
    };
    mode?.addEventListener("change", updateMode);
    updateMode();
  }
  const initial = form.elements.namedItem("certificate_path").value;
  const selected = initial.startsWith(prefix) ? initial.slice(prefix.length).split("/")[0] : "";
  const localPaths = new Map(["certificate_path", "private_key_path"].map(name => { const value=form.elements.namedItem(name)?.value||"";return [name,value.startsWith(prefix)?"":value]; }));
  let managed = Boolean(selected);
  let items = [], loadError = "", loading = true, signature = "";
  const retry = document.createElement("button");
  retry.type = "button"; retry.className = "button small"; retry.textContent = "重试"; retry.hidden = true; field.append(retry);
  const live = () => form.isConnected && visible();
  const availability = cert => {
    if (!cert) return "证书已删除或授权已撤销";
    if (!cert.not_after || !cert.version) return "尚未签发";
    if (Date.parse(cert.not_before) > Date.now()) return "尚未生效";
    if (Date.parse(cert.not_after) <= Date.now()) return "已过期";
    if (!Number.isFinite(Date.parse(cert.not_after))) return "有效期无效";
    return "";
  };
  const matches = (domain, name) => domain === name || domain.startsWith("*.") && name.endsWith(domain.slice(1)) && name.split(".").length === domain.split(".").length;
  const state = cert => {
    const reason = availability(cert);
    const renewal = {queued:"等待更新",running:"更新中",retrying:"更新重试中",validating:"更新验证中",waiting_dns:"等待 DNS 验证",failed:"更新失败",canceled:"更新已取消",canceling:"取消更新中"}[cert.status];
    const sourceUpdate = ["caddy","node"].includes(cert.source) ? {pending:"等待来源更新",synced:"来源已同步",blocked:"来源更新受阻",paused:"来源同步已暂停",detached:"来源已解除",unverified:"来源待核实"}[cert.sync_status] : "";
    const expiry = cert.not_after ? `${new Date(cert.not_after).toLocaleDateString()} 到期` : "";
    const valid = reason || (Date.parse(cert.not_after) - Date.now() < 30*86400000 ? "临近到期" : "有效");
    const sync = cert.binding_statuses || {};
    const syncLabels = {pending:"等待同步",running:"同步中",synced:"已同步",failed:"同步失败",blocked:"同步受阻",disabled:"未启用同步"};
    const targets = Object.entries(sync).filter(([,count])=>count).map(([key,count])=>`${syncLabels[key]||key} ${count}`);
    const partial = sync.synced && (sync.pending || sync.running || sync.failed || sync.blocked) ? "部分完成" : "";
    return [valid,expiry,sourceUpdate,renewal && `${renewal}${!reason ? "，现有证书有效" : ""}`,partial,...targets].filter(Boolean).join(" · ");
  };
  const name = cert => cert.name?.trim() || cert.domains.join(", ");
  const description = cert => {
    const domains = [...new Set(cert.domains)].filter(domain=>domain.toLowerCase()!==name(cert).toLowerCase());
    return [domains.join(", "),{node:"节点证书",caddy:"Caddy",acme:"ACME",import:"导入","self-signed":"自签"}[cert.source]||cert.source,state(cert)].filter(Boolean).join(" · ");
  };
  select.value = selected;
  const update = (event, refreshOnly = false) => {
    const active = Boolean(select.value);
    const cert=items.find(item=>item.id===select.value);
    const sniValue = form.elements.namedItem("tls_server_name")?.value.trim().toLowerCase();
    const reason = active && !loading ? availability(cert) || (!sniValue ? "请填写 TLS SNI" : "") || (sniValue && !cert.domains.some(domain=>matches(domain.toLowerCase(),sniValue)) ? "证书不覆盖当前 TLS SNI" : "") : "";
    select.setCustomValidity(reason || (active && loading ? "正在加载证书，请稍候" : "") || (active && loadError ? "证书列表加载失败，请重试" : ""));
    summary.hidden = false;
    summary.textContent = loading ? "正在加载证书…" : loadError ? "证书列表加载失败，已保留选择" : reason || (cert ? description(cert) : items.length ? "使用节点原有证书配置" : "暂无可选证书，可使用节点原有配置");
    select.title = cert ? `${name(cert)} · ${description(cert)}` : summary.textContent;
    if (refreshOnly) return;
    const sni=form.elements.namedItem("tls_server_name");if(event&&active&&cert?.domains.length===1&&!cert.domains[0].startsWith("*.")&&sni&&!sni.value)sni.value=cert.domains[0];
    const mode = form.elements.namedItem("tls_mode");
    if (active && mode && (event || protocol.key !== "vless-ws-tls")) { mode.value = "native"; mode.dispatchEvent(new Event("change", { bubbles: true })); }
    const tls = form.elements.namedItem("tls_enabled");
    if (active && tls && !tls.checked) { tls.checked = true; tls.dispatchEvent(new Event("change", { bubbles: true })); }
    for (const [name, file] of [["certificate_path", "cert.pem"], ["private_key_path", "key.pem"]]) {
      const control = form.elements.namedItem(name); if (!control) continue;
      if (active) {
        if (!managed) localPaths.set(name, control.value);
        control.value = `${prefix}${select.value}/${file}`;
      } else if (managed) control.value = localPaths.get(name) || "";
      control.readOnly = active;
      control.closest("label").hidden = active;
    }
    for (const note of form.querySelectorAll("p[data-native-certificate]")) { note.hidden = active; }
    managed = active;
    if (event) update(undefined,true);
  };
  select.addEventListener("change", update);
  form.elements.namedItem("tls_server_name")?.addEventListener("input", () => update(undefined,true));
  // Initial managed references must remain present while data is loading.
  if (selected) { select.add(new Option("正在加载所选证书…", selected)); select.value = selected; }
  update();
  const refresh = async () => {
    if (!live()) return;
    retry.disabled = true;
    try {
      const response = await api("/certificates");
      if (!live()) return;
      items = response.items || []; loadError = ""; loading = false;
      const nextSignature = JSON.stringify(items.map(cert=>[cert,availability(cert),description(cert)]));
      if (signature !== nextSignature) {
        signature = nextSignature;
        refreshSelectMenu(select, () => {
          const value = select.value;
          select.replaceChildren(local);
          for (const owned of [true,false]) {
            const group = document.createElement("optgroup"); group.label = owned ? "我的证书" : "授权给我的证书";
            for (const cert of items.filter(item=>item.owned===owned)) {
              const option = new Option(name(cert),cert.id);
              option.disabled = Boolean(availability(cert));
              option.dataset.description = description(cert);
              option.title = `${name(cert)} · ${description(cert)}`;
              group.append(option);
            }
            if (group.childElementCount) select.append(group);
          }
          if (value && !items.some(cert=>cert.id===value)) {
            const missing = new Option("所选证书不可用",value); missing.disabled = true; select.add(missing);
          }
          select.value = value;
        });
      }
    } catch {
      if (!live()) return;
      loading = false; loadError = "failed";
    } finally {
      retry.disabled = false; retry.hidden = !loadError;
      if (live()) update(undefined,true);
    }
  };
  retry.addEventListener("click", refresh);
  // Recursive polling stops after the form/instance closes; no stale callback
  // changes form fields, TLS mode, SNI, local paths, focus or draft contents.
  const poll = async () => { await refresh(); if (live()) setTimeout(poll,15000); };
  await poll();
}
