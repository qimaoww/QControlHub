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
  let sniLabel;
  if (!form.elements.namedItem("tls_server_name")) { sniLabel=document.createElement("label");sniLabel.textContent="TLS SNI / 证书域名";const sni=document.createElement("input");sni.name="tls_server_name";sni.value=plan?.tls_server_name||"";sni.placeholder="例如 edge.example.com";sniLabel.append(sni);field.append(sniLabel); }
  const target = form.querySelector("[data-native-certificate]") || form.querySelector("[data-native-tls]");
  if (!target) return; target.before(field);
  const initial = form.elements.namedItem("certificate_path").value;
  const selected = initial.startsWith(prefix) ? initial.slice(prefix.length).split("/")[0] : "";
  const localPaths = new Map(["certificate_path", "private_key_path"].map(name => { const value=form.elements.namedItem(name)?.value||"";return [name,value.startsWith(prefix)?"":value]; }));
  let managed = Boolean(selected);
  const { items } = await api("/certificates");
  if (!form.isConnected || !visible()) return;
  for (const owned of [true, false]) {
    const group = document.createElement("optgroup"); group.label = owned ? "我的证书" : "授权给我的证书";
    for (const cert of items.filter(item => item.owned === owned)) {
      const expired = !cert.not_after || Date.parse(cert.not_after) <= Date.now();
      const option = new Option(`${cert.name} · ${cert.domains.join(", ")} · v${cert.version}${expired ? " · 不可用" : ""}`, cert.id);
      option.disabled = expired; group.append(option);
    }
    select.append(group);
  }
  select.value = selected;
  const update = () => {
    const active = Boolean(select.value);
    const mode = form.elements.namedItem("tls_mode");
    if (active && mode) { mode.value = "native"; mode.dispatchEvent(new Event("change", { bubbles: true })); }
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
    for (const note of form.querySelectorAll("p[data-native-certificate]")) { note.hidden = active; note.style.display = active ? "none" : ""; }
    if (sniLabel) sniLabel.hidden = !active;
    managed = active;
  };
  select.addEventListener("change", update); update();
}
