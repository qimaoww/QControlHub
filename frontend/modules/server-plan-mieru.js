import { listenerAdvancedOptions } from "./server-plan-form.js";

const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (char) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);

export function mieruProtocolOptions(plan) {
  const transport = plan.mieru_transport || "TCP";
  const trafficPattern = plan.mieru_traffic_pattern || "off";
  const custom = plan.mieru_custom_pattern || {};
  const selected = (value, current) => String(value) === String(current) ? "selected" : "";
  const checked = (value) => value ? "checked" : "";
  const rotations = ['<option value="none" ' + selected("none", custom.mask_rotation ?? "right_7") + '>不旋转</option>',
    ...["right", "left"].flatMap((direction) => Array.from({ length: 15 }, (_, index) => {
      const value = `${direction}_${index + 1}`;
      return `<option value="${value}" ${selected(value, custom.mask_rotation ?? "right_7")}>${direction === "right" ? "右移" : "左移"} ${index + 1} 位</option>`;
    }))].join("");
  return `<div class="preset-protocol-options" data-mieru-options>
    <details class="preset-option-panel" open><summary><b>Mieru 传输</b></summary><div class="plan-fields one"><label>传输协议<select name="mieru_transport"><option value="TCP" ${selected("TCP", transport)}>TCP</option><option value="UDP" ${selected("UDP", transport)}>UDP</option></select><small>客户端须使用相同传输协议；两种方式均可代理 TCP 与 UDP 流量。</small></label></div></details>
    <details class="preset-option-panel" open><summary><b>网络流量模式</b></summary>
      <div class="plan-fields one"><label>模式<select name="mieru_traffic_pattern"><option value="off" ${selected("off", trafficPattern)}>关闭</option><option value="light" ${selected("light", trafficPattern)}>轻度</option><option value="balanced" ${selected("balanced", trafficPattern)}>均衡</option><option value="aggressive" ${selected("aggressive", trafficPattern)}>激进</option><option value="custom" ${selected("custom", trafficPattern)}>自定义</option></select><small>关闭：使用 Mieru 默认行为；轻度：少量分片与填充，开销较低；均衡：增加填充与低熵处理；激进：整形更强，流量开销最高，可能增加延迟；自定义：自行设定参数。</small></label></div>
      <div class="mieru-custom-fields" data-mieru-custom-fields ${trafficPattern === "custom" ? "" : "hidden"}>
        <fieldset class="mieru-custom-group"><legend>TCP 分片</legend><div class="plan-fields two">
          <label class="plan-check"><input type="checkbox" name="mieru_custom_tcp_fragment" ${checked(custom.tcp_fragment ?? true)}><span><b>启用 TCP 分片</b><small>仅对 TCP 传输生效。</small></span></label>
          <label data-mieru-fragment-sleep>最大等待（毫秒）<input type="number" name="mieru_custom_max_sleep_ms" min="0" max="100" step="1" value="${esc(custom.max_sleep_ms ?? 8)}"></label>
        </div></fieldset>
        <fieldset class="mieru-custom-group"><legend>Nonce</legend><div class="plan-fields two">
          <label>类型<select name="mieru_custom_nonce_type"><option value="RANDOM" ${selected("RANDOM", custom.nonce_type ?? "PRINTABLE")}>随机</option><option value="PRINTABLE" ${selected("PRINTABLE", custom.nonce_type ?? "PRINTABLE")}>可打印字符</option><option value="PRINTABLE_SUBSET" ${selected("PRINTABLE_SUBSET", custom.nonce_type ?? "PRINTABLE")}>可打印字符子集</option><option value="FIXED" ${selected("FIXED", custom.nonce_type ?? "PRINTABLE")}>固定前缀</option></select></label>
          <label class="plan-check"><input type="checkbox" name="mieru_custom_nonce_apply_to_all_udp" ${checked(custom.nonce_apply_to_all_udp ?? true)}><span><b>应用于每个 UDP 包</b><small>关闭时仅应用于首个 UDP 包。</small></span></label>
          <label data-mieru-nonce-range>最短长度（字节）<input type="number" name="mieru_custom_nonce_min_len" min="0" max="12" step="1" value="${esc(custom.nonce_min_len ?? 6)}"></label>
          <label data-mieru-nonce-range>最长长度（字节）<input type="number" name="mieru_custom_nonce_max_len" min="0" max="12" step="1" value="${esc(custom.nonce_max_len ?? 8)}"></label>
          <label class="mieru-custom-wide" data-mieru-nonce-fixed>固定前缀（Hex）<input name="mieru_custom_nonce_fixed_hex" value="${esc(custom.nonce_fixed_hex ?? "")}" placeholder="例如 00010203, a1b2c3d4" spellcheck="false"><small>多个值用逗号分隔；每个最多 12 字节。</small></label>
        </div></fieldset>
        <fieldset class="mieru-custom-group"><legend>填充</legend><div class="plan-fields two">
          <label>中间填充上限（字节）<input type="number" name="mieru_custom_padding_middle" min="0" max="255" step="1" value="${esc(custom.padding_middle ?? 64)}"></label>
          <label>末尾填充上限（字节）<input type="number" name="mieru_custom_padding_end" min="0" max="255" step="1" value="${esc(custom.padding_end ?? 128)}"></label>
        </div></fieldset>
        <fieldset class="mieru-custom-group"><legend>低熵处理</legend><div class="plan-fields two">
          <label>模式<select name="mieru_custom_low_entropy_mode"><option value="0" ${selected(0, custom.low_entropy_mode ?? 56)}>关闭</option><option value="32" ${selected(32, custom.low_entropy_mode ?? 56)}>32（开销最高）</option><option value="40" ${selected(40, custom.low_entropy_mode ?? 56)}>40</option><option value="48" ${selected(48, custom.low_entropy_mode ?? 56)}>48</option><option value="56" ${selected(56, custom.low_entropy_mode ?? 56)}>56（开销较低）</option></select></label>
          <label data-mieru-mask-rotation>掩码旋转<select name="mieru_custom_mask_rotation">${rotations}</select></label>
        </div></fieldset>
      </div>
    </details>
    ${listenerAdvancedOptions(plan)}</div>`;
}
