import { bindEvent } from "./refresh.js";
export function createAccessControlBindings({ api, state, notify, confirmAction, beforeDeploy }, { editable, accessControl }) {
  function bind(entries, root = document, onSaved = null, isCurrent = () => true) {
    const data = state.data;
    const confirmDiscard = async (event) => {
      const dirty = root.querySelector(
        "[data-access-control-form].is-dirty",
      );
      if (!dirty) return true;
      event.preventDefault();
      return confirmAction(
        "当前入站有未保存的访问限制修改，确定放弃并离开？",
        "放弃更改",
      );
    };
    root.querySelectorAll("[data-access-control-agent]").forEach((link) => {
      link.onclick = async (event) => {
        if (!(await confirmDiscard(event)) || state.data !== data) return;
        state.data.accessControlAgent = link.dataset.accessControlAgent;
        if (event.defaultPrevented) {
          const href = link.getAttribute("href");
          if (location.hash === href) await accessControl();
          else location.hash = href;
        }
      };
    });
    root.querySelectorAll('.app-dock a[href^="#"]').forEach((link) => {
      link.onclick = async (event) => {
        if (!(await confirmDiscard(event)) || state.data !== data) return;
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
    root.querySelectorAll("[data-access-control-form]").forEach((form) => {
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
        if (!editable() || form.dataset.agentStatus !== "online" || form.dataset.busy === "1" || !isCurrent()) return;
        form.dataset.busy = "1";
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
          { delete form.dataset.busy; return; }
        if (state.data !== data || !isCurrent()) { delete form.dataset.busy; return; }
        const buttons = form.querySelectorAll("button");
        const stateText = form.querySelector("[data-access-control-state-text]");
        buttons.forEach((button) => (button.disabled = true));
        try {
          if (intent === "deploy" && beforeDeploy) {
            if (stateText) stateText.textContent = "正在核验 Agent 当前配置…";
            await beforeDeploy();
            if (state.data !== data || !isCurrent()) return;
          }
          if (stateText) stateText.textContent = "正在保存配置并提交任务…";
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
          if (state.data !== data || !isCurrent()) return;
          if (onSaved) await onSaved(result);
          else if (state.route === "access-control") await accessControl();
          notify(
            intent === "deploy"
              ? `访问限制已保存，部署任务 ${result.task.id.slice(0, 12)} 已创建`
              : `访问限制已保存，校验任务 ${result.task.id.slice(0, 12)} 已创建`,
          );
        } catch (error) {
          delete form.dataset.busy;
          if (state.data !== data || !isCurrent() || error.name === "AbortError") return;
          if (stateText) stateText.textContent = error.deployPreflight ? "部署前核验未通过，未保存" : "保存失败，可重试";
          notify(error.message, "error");
          buttons.forEach((button) => (button.disabled = false));
        }
      });
    });
  }

  return bind;
}
