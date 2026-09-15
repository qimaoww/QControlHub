export function createAccessControlView({ state, esc, engineName, shell }, { editable }) {
  const card = (entry) => `<article class="access-control-card" data-refresh-key="access-control-${esc(entry.agent_id)}-${esc(entry.engine)}-${esc(entry.tag)}">
          <header><div><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><span><strong>${esc(entry.tag)}</strong><small>${esc(entry.kind || "入站")} · ${esc(entry.agent_name)}</small></span></div><code>:${Number(entry.port)}</code></header>
          <form data-access-control-form data-agent-status="${esc(entry.agent_status)}">
            <input type="hidden" name="agent_id" value="${esc(entry.agent_id)}">
            <input type="hidden" name="engine" value="${esc(entry.engine)}">
            <input type="hidden" name="tag" value="${esc(entry.tag)}">
            <input type="hidden" name="port" value="${Number(entry.port)}">
            <input type="hidden" name="expected_version" value="${Number(entry.config_version)}">
            <label class="access-control-option"><input type="checkbox" name="block_mainland_destination" ${entry.block_mainland_destination ? "checked" : ""} ${editable() ? "" : "disabled"}><span><b>禁止访问大陆目标</b><small>${entry.engine === "ss-rust" ? "由 ssserver 原生 ACL 文件拒绝当前 CN IP 源的 IPv4 与大陆域名；同一 ssserver 进程的全部入站共同生效。" : "经此入站代理的连接，目标 IPv4 / IPv6 命中大陆 CIDR 时由内核拒绝。"}</small></span><em>${entry.block_mainland_destination ? "已启用" : "未启用"}</em></label>
            <label class="access-control-option"><input type="checkbox" name="block_mainland_source" ${entry.block_mainland_source ? "checked" : ""} ${editable() ? "" : "disabled"}><span><b>禁止大陆来源连接</b><small>${entry.engine === "ss-rust" ? "使用 nftables 按入站端口拒绝当前 CN IP 源的 IPv4 来源，规则独立于 ssserver 配置。" : "来源 IPv4 / IPv6 命中大陆 CIDR 时，仅拒绝连接这个入站端口。"}</small></span><em>${entry.block_mainland_source ? "已启用" : "未启用"}</em></label>
            <footer><span class="access-control-scope" aria-live="polite"><i class="${entry.agent_status === "online" ? "ok" : ""}"></i><span data-access-control-state-text>${entry.agent_status === "online" ? "节点在线" : "节点离线，暂不可提交"}</span></span>${editable() ? `<div><button class="button small" type="submit" data-access-intent="validate" ${entry.agent_status === "online" ? "" : "disabled"}>保存并校验</button><button class="button small primary" type="submit" data-access-intent="deploy" ${entry.agent_status === "online" ? "" : "disabled"}>保存并部署</button></div>` : ""}</footer>
          </form>
        </article>`;

  function render(entries) {
    if (state.anchor === "access-control-all") {
      state.data.accessControlAgent = "";
    } else if (state.anchor?.startsWith("access-control-agent-")) {
      state.data.accessControlAgent = state.anchor.slice(21);
    }
    const selected = state.data.accessControlAgent || "";
    const visible = selected
      ? entries.filter((entry) => entry.agent_id === selected)
      : entries;
    const activeDestination = visible.filter(
      (entry) => entry.block_mainland_destination,
    ).length;
    const activeSource = visible.filter(
      (entry) => entry.block_mainland_source,
    ).length;
    const cards = visible.map(card).join("");
    const title = selected
      ? entries.find((entry) => entry.agent_id === selected)?.agent_name || "所选节点"
      : "全部节点";
    shell(
      `<div class="access-control-workspace"><section class="access-control-toolbar"><dl class="access-control-stats"><div><dt>当前范围</dt><dd>${esc(title)}</dd></div><div><dt>入站端口</dt><dd>${visible.length}</dd></div><div><dt>禁止目标</dt><dd>${activeDestination}</dd></div><div><dt>禁止来源</dt><dd>${activeSource}</dd></div></dl><a class="access-control-source" href="https://github.com/misakaio/chnroutes2" target="_blank" rel="noopener noreferrer"><span><small>大陆 CIDR 数据源</small><b>chnroutes2 IPv4 + 大陆 IPv6</b></span><em>核心规则集与 ACL 均按文件加载</em><strong>查看 ↗</strong></a></section>${cards ? `<section class="access-control-grid">${cards}</section>` : '<div class="empty large"><strong>当前范围没有可限制的入站端口</strong><p>保存 Mihomo、Xray、Sing-box 或 ss-rust 的命名入站配置后会显示在这里。</p></div>'}</div>`,
      "访问限制",
      { viewKey: `access-control-${selected || "all"}` },
    );
  }

    return { render, card };
}
