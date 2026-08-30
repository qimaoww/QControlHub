import { bindEvent } from "./refresh.js";

export function installAccessControl(ctx) {
  const { api, state, can, esc, engineName, shell, notify, confirmAction } = ctx;

  const editable = () => can("agent-config.write") && can("tasks.execute");

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
    const cards = visible
      .map(
        (entry) => `<article class="access-control-card" data-refresh-key="access-control-${esc(entry.agent_id)}-${esc(entry.engine)}-${esc(entry.tag)}">
          <header><div><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><span><strong>${esc(entry.tag)}</strong><small>${esc(entry.kind || "入站")} · ${esc(entry.agent_name)}</small></span></div><code>:${Number(entry.port)}</code></header>
          <form data-access-control-form data-agent-status="${esc(entry.agent_status)}">
            <input type="hidden" name="agent_id" value="${esc(entry.agent_id)}">
            <input type="hidden" name="engine" value="${esc(entry.engine)}">
            <input type="hidden" name="tag" value="${esc(entry.tag)}">
            <input type="hidden" name="port" value="${Number(entry.port)}">
            <input type="hidden" name="expected_version" value="${Number(entry.config_version)}">
            <label class="access-control-option"><input type="checkbox" name="block_mainland_destination" ${entry.block_mainland_destination ? "checked" : ""} ${editable() ? "" : "disabled"}><span><b>禁止访问大陆目标</b><small>${entry.engine === "ss-rust" ? "由 ssserver 原生 ACL 文件拒绝 chnroutes2 IPv4 与大陆域名；同一 ssserver 进程的全部入站共同生效。" : "经此入站代理的连接，目标 IPv4 / IPv6 命中大陆 CIDR 时由内核拒绝。"}</small></span><em>${entry.block_mainland_destination ? "已启用" : "未启用"}</em></label>
            <label class="access-control-option"><input type="checkbox" name="block_mainland_source" ${entry.block_mainland_source ? "checked" : ""} ${editable() ? "" : "disabled"}><span><b>禁止大陆来源连接</b><small>${entry.engine === "ss-rust" ? "使用 nftables 按入站端口拒绝 chnroutes2 IPv4 来源，规则独立于 ssserver 配置。" : "来源 IPv4 / IPv6 命中大陆 CIDR 时，仅拒绝连接这个入站端口。"}</small></span><em>${entry.block_mainland_source ? "已启用" : "未启用"}</em></label>
            <footer><span class="access-control-scope" aria-live="polite"><i class="${entry.agent_status === "online" ? "ok" : ""}"></i><span data-access-control-state-text>${entry.agent_status === "online" ? "节点在线" : "节点离线，暂不可提交"}</span></span>${editable() ? `<div><button class="button small" type="submit" data-access-intent="validate" ${entry.agent_status === "online" ? "" : "disabled"}>保存并校验</button><button class="button small primary" type="submit" data-access-intent="deploy" ${entry.agent_status === "online" ? "" : "disabled"}>保存并部署</button></div>` : ""}</footer>
          </form>
        </article>`,
      )
      .join("");
    const title = selected
      ? entries.find((entry) => entry.agent_id === selected)?.agent_name || "所选节点"
      : "全部节点";
    shell(
      `<div class="access-control-workspace"><section class="access-control-toolbar"><dl class="access-control-stats"><div><dt>当前范围</dt><dd>${esc(title)}</dd></div><div><dt>入站端口</dt><dd>${visible.length}</dd></div><div><dt>禁止目标</dt><dd>${activeDestination}</dd></div><div><dt>禁止来源</dt><dd>${activeSource}</dd></div></dl><a class="access-control-source" href="https://github.com/misakaio/chnroutes2" target="_blank" rel="noopener noreferrer"><span><small>大陆 CIDR 数据源</small><b>chnroutes2 IPv4 + 大陆 IPv6</b></span><em>核心规则集与 ACL 均按文件加载</em><strong>查看 ↗</strong></a></section>${cards ? `<section class="access-control-grid">${cards}</section>` : '<div class="empty large"><strong>当前范围没有可限制的入站端口</strong><p>保存 Mihomo、Xray、Sing-box 或 Shadowsocks Rust 的命名入站配置后会显示在这里。</p></div>'}</div>`,
      "访问限制",
    );
    bind(entries);
  }

  function bind(entries) {
    const confirmDiscard = async (event) => {
      const dirty = document.querySelector(
        "[data-access-control-form].is-dirty",
      );
      if (!dirty) return true;
      event.preventDefault();
      return confirmAction(
        "当前入站有未保存的访问限制修改，确定放弃并离开？",
        "放弃更改",
      );
    };
    document.querySelectorAll("[data-access-control-agent]").forEach((link) => {
      link.onclick = async (event) => {
        if (!(await confirmDiscard(event))) return;
        state.data.accessControlAgent = link.dataset.accessControlAgent;
        if (event.defaultPrevented) {
          const href = link.getAttribute("href");
          if (location.hash === href) await accessControl();
          else location.hash = href;
        }
      };
    });
    document.querySelectorAll('.app-dock a[href^="#"]').forEach((link) => {
      link.onclick = async (event) => {
        if (!(await confirmDiscard(event))) return;
        if (event.defaultPrevented) location.hash = link.getAttribute("href");
      };
    });
    if (matchMedia("(max-width: 820px) and (pointer: coarse)").matches) {
      requestAnimationFrame(() =>
        document
          .querySelector("[data-access-control-agent].active")
          ?.scrollIntoView({ block: "nearest", inline: "center" }),
      );
    }
    document.querySelectorAll("[data-access-control-form]").forEach((form) => {
      const inputs = [...form.querySelectorAll('input[type="checkbox"]')];
      const cleanState =
        form.dataset.agentStatus === "online"
          ? "节点在线"
          : "节点离线，暂不可提交";
      inputs.forEach((input) => {
        input.dataset.initialChecked = String(input.checked);
        input.onchange = () => {
          const label = input.closest("label");
          if (label?.querySelector("em")) {
            const unchanged =
              input.checked === (input.dataset.initialChecked === "true");
            label.querySelector("em").textContent = unchanged
              ? input.checked
                ? "已启用"
                : "未启用"
              : input.checked
                ? "待保存"
                : "待关闭";
          }
          const dirty = inputs.some(
            (candidate) =>
              candidate.checked !==
              (candidate.dataset.initialChecked === "true"),
          );
          form.classList.toggle("is-dirty", dirty);
          const stateText = form.querySelector(
            "[data-access-control-state-text]",
          );
          if (stateText)
            stateText.textContent = dirty ? "有未保存更改" : cleanState;
        };
      });
      bindEvent(form, "submit", async (event) => {
        event.preventDefault();
        const submitter = event.submitter;
        const intent = submitter?.dataset.accessIntent || "validate";
        const values = new FormData(form);
        if (
          intent === "deploy" &&
          !(await confirmAction(
            `确定保存并部署 ${values.get("tag")} :${values.get("port")} 的大陆访问限制？`,
            "保存并部署",
          ))
        )
          return;
        const buttons = form.querySelectorAll("button");
        buttons.forEach((button) => (button.disabled = true));
        try {
          const result = await api("/access-controls", {
            method: "PUT",
            body: JSON.stringify({
              agent_id: values.get("agent_id"),
              engine: values.get("engine"),
              tag: values.get("tag"),
              port: Number(values.get("port")),
              expected_version: Number(values.get("expected_version")),
              block_mainland_destination: values.has(
                "block_mainland_destination",
              ),
              block_mainland_source: values.has("block_mainland_source"),
              intent,
            }),
          });
          await accessControl();
          notify(
            intent === "deploy"
              ? `访问限制已保存，部署任务 ${result.task.id.slice(0, 12)} 已创建`
              : `访问限制已保存，校验任务 ${result.task.id.slice(0, 12)} 已创建`,
          );
        } catch (error) {
          notify(error.message, "error");
          buttons.forEach((button) => (button.disabled = false));
        }
      });
    });
  }

  async function accessControl() {
    const entries = await api("/access-controls");
    state.data.accessControls = entries;
    render(entries);
  }

  return accessControl;
}
