import { validateTCPSelection } from "./system-bbr-model.js";

export const systemTCPPresets = Object.freeze([
  Object.freeze({
    id: "bbr-32m",
    name: "BBR · 32 MiB 缓冲区",
    settings: Object.freeze({
      "net.core.default_qdisc": "fq",
      "net.ipv4.tcp_congestion_control": "bbr",
      "net.core.rmem_max": "33554432",
      "net.core.wmem_max": "33554432",
      "net.ipv4.tcp_rmem": "4096 65536 33554432",
      "net.ipv4.tcp_wmem": "4096 65536 33554432",
      "net.ipv4.tcp_mtu_probing": "1",
      "net.ipv4.tcp_window_scaling": "1",
    }),
  }),
]);

export function prepareTCPPreset(presetID, rules, currentParameters, draft = {}) {
  const preset = systemTCPPresets.find((entry) => entry.id === presetID);
  if (!preset) throw new Error("未找到此 TCP 预设，请刷新页面后重试。");
  const missing = Object.keys(preset.settings).filter((key) => !Object.hasOwn(currentParameters || {}, key));
  if (missing.length) throw new Error(`此节点未上报预设所需参数：${missing.join("、")}`);
  const settings = validateTCPSelection(preset.settings, rules);
  return { ...draft, ...settings };
}
