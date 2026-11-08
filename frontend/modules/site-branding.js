// Public site state is deliberately separate from authenticated appearance.
let logo = "", sequence = 0;
let iconValue;
const marks = new Map();

function updateSiteIcon(value) {
  if (iconValue === value) return;
  iconValue = value;
  let icon = document.querySelector('#qch-site-icon');
  if (!icon) {
    icon = document.createElement('link'); icon.id = 'qch-site-icon'; icon.rel = 'icon'; document.head.append(icon);
  }
  const restore = () => { icon.type = 'image/svg+xml'; icon.href = '/assets/favicon.svg'; };
  if (!value) { restore(); return; }
  const picture = new Image();
  picture.onload = () => {
    if (iconValue !== value) return;
    try {
      const canvas = document.createElement('canvas'); canvas.width = canvas.height = 64;
      const scale = Math.min(64 / picture.naturalWidth, 64 / picture.naturalHeight);
      const width = picture.naturalWidth * scale, height = picture.naturalHeight * scale;
      canvas.getContext('2d').drawImage(picture, (64 - width) / 2, (64 - height) / 2, width, height);
      icon.type = 'image/png'; icon.href = canvas.toDataURL('image/png');
    } catch { restore(); }
  };
  picture.onerror = () => { if (iconValue === value) restore(); };
  picture.src = value;
}

export function renderSiteLogo(mark, value) {
  if (mark.siteLogoValue === value) return;
  const fallback = mark.siteLogoFallback ||= [...mark.childNodes].map(child => child.cloneNode(true));
  const restore = () => mark.replaceChildren(...fallback.map(child => child.cloneNode(true)));
  mark.siteLogoValue = value;
  if (!value) { restore(); return; }
  const picture = document.createElement("img");
  picture.alt = "";
  picture.className = mark.className;
  picture.width = picture.height = mark.classList.contains("qch-startup-mark") ? 58 : 32;
  picture.onload = () => { if (mark.siteLogoValue === value) mark.replaceChildren(picture); };
  picture.onerror = () => { if (mark.siteLogoValue === value) restore(); };
  picture.src = value;
}

export function bindSiteLogo(mark) {
  marks.set(mark, true);
  renderSiteLogo(mark, logo);
  return mark;
}

export function applySiteBranding(value = {}) {
  ++sequence;
  logo = typeof value.logo === "string" && /^data:image\/(png|jpeg);base64,/.test(value.logo) ? value.logo : "";
  updateSiteIcon(logo);
  for (const mark of marks.keys()) {
    if (!mark.isConnected) { marks.delete(mark); continue; }
    if (mark.siteLogoValue !== logo) renderSiteLogo(mark, logo);
  }
}

export async function refreshSiteBranding() {
  const request = ++sequence;
  try {
    const response = await fetch("/api/v1/branding", { credentials: "omit", cache: "no-store", signal: AbortSignal.timeout(4000) });
    if (!response.ok) return;
    const value = await response.json();
    if (request === sequence) applySiteBranding(value);
  } catch { /* Branding cannot block startup, login or a usable current logo. */ }
}
