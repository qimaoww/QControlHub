import { bindEvent } from "./refresh.js";

export function renderXrayExports(profile, id, esc) {
  if (!profile?.xray_json && !profile?.variants?.length) return "";
  const variants = profile.variants?.length ? profile.variants : [{name:"Xray 客户端配置", profile}];
  return `<section class="xhttp-client-exports"><h3>完整 Xray JSON</h3><p class="validation-note">导入后使用本地 SOCKS 127.0.0.1:10808。凭据属于当前配置，请妥善保存。</p>${variants.map((variant, i) => {
    const p = variant.profile, target = `${id}-xray-${i}`;
    return `<details class="preset-option-panel"><summary><b>${esc(variant.name)}</b></summary><p class="validation-note">${esc(p.uri_error || "分享链接：支持基础连接参数。")}${p.sing_box_error ? ` ${esc(p.sing_box_error)}` : ""}${p.mihomo_error ? ` ${esc(p.mihomo_error)}` : " Mihomo：基础参数支持（验证 1.19.32）。"}</p><form class="client-export-control" action="#"><textarea class="client-share-yaml is-masked" id="${target}" readonly autocomplete="off" spellcheck="false" aria-label="${esc(variant.name)} Xray JSON">${esc(p.xray_json || "")}</textarea><button class="button small" type="button" data-secret-visibility aria-controls="${target}" aria-pressed="false">显示</button><button class="button small" type="button" data-copy-target="#${target}">复制 JSON</button><button class="button small" type="button" data-download-xray="${target}" data-download-name="xray-${i + 1}.json">下载 JSON</button></form></details>`;
  }).join("")}</section>`;
}

export function bindXrayDownloads(root = document) {
  root.querySelectorAll("[data-download-xray]").forEach(button => bindEvent(button, "click", () => {
    const source = document.getElementById(button.dataset.downloadXray);
    if (!source?.value) return;
    const url = URL.createObjectURL(new Blob([source.value], {type:"application/json"}));
    const link = document.createElement("a");
    link.href = url;
    link.download = button.dataset.downloadName || "xray-client.json";
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }));
}
