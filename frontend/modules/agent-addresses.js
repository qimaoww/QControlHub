import { setVisible, updateFeedback } from "./presence-motion.js";
import { publicAddressRows, manualConnectionAddressNote } from "./agent-address-model.js";
export { publicAddressRows, manualConnectionAddressNote } from "./agent-address-model.js";

// Builds a host:port display string. An IPv6 literal must be bracketed before
// appending the port; a raw "2606:...:443" is not a usable address preview.
export function formatHostPort(address, port) {
  const raw = String(address || "").trim();
  if (!raw) return "";
  let host = raw;
  if (raw.startsWith("[") && raw.endsWith("]")) {
    const inner = raw.slice(1, -1);
    if (inner.includes(":")) host = inner;
  }
  if (!port) return host;
  return host.includes(":") ? `[${host}]:${port}` : `${host}:${port}`;
}

export function updatePublicIPDisplays(root, metrics, labels = {}, features = []) {
  const rows = publicAddressRows(metrics || {}, labels || {}, features || []);
  const connectionNote = manualConnectionAddressNote(labels);
  for (const container of root.querySelectorAll(".node-card-ips, .node-public-ips")) {
    const isCard = container.classList.contains("node-card-ips");
    for (const row of rows) {
      const selector = isCard
        ? `.card-ip-row[data-ip-family="${row.cls}"]`
        : `.public-ip-row[data-ip-family="${row.cls}"]`;
      const line = container.querySelector(selector);
      if (!line) continue;
      setVisible(line, row.value);
      if (line.dataset) line.dataset.ipSource = row.source;
      const code = line.querySelector("code");
      if (code) {
        code.textContent = row.value || "未探测到";
        code.title = row.value || "";
      }
      line.classList.toggle("empty", !row.value);
      if (!isCard) {
        const source = line.querySelector("small");
        if (source) source.textContent = row.source;
        continue;
      }
      const copy = line.querySelector("[data-copy-ip]");
      if (copy) {
        copy.dataset.copyIp = row.value || "";
        copy.title = row.value ? `复制 ${row.label} 地址` : "暂无地址";
        setVisible(copy, row.value);
        copy.setAttribute("aria-label", `复制 ${row.label} 公网地址 ${row.value || ""}`);
      }
    }
  }
  for (const note of root.querySelectorAll("[data-node-connection-address]")) {
    updateFeedback(note, connectionNote);
  }
}
