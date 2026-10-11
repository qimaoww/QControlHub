import { updateFeedback } from "./presence-motion.js";
import { bindEvent } from "./refresh.js";
import { bindDialogBackdrop } from "./popup.js";
import { copyClientValue } from "./client-clipboard.js";

function maskSecrets(container) {
  container.querySelectorAll("[data-secret-visibility]").forEach(button => {
    const input = document.getElementById(button.getAttribute("aria-controls"));
    if (input?.matches("textarea")) input.classList.add("is-masked");
    else if (input) input.type = "password";
    button.textContent = "显示";
    button.setAttribute("aria-pressed", "false");
  });
}
function syncAddressDraft(form) {
  const manual = Boolean(form.elements.namedItem("address").value.trim());
  const mode = form.elements.namedItem("address_mode");
  if (mode) mode.disabled = manual;
  form.querySelector("[data-client-address-state]").textContent = manual ? "手动覆盖" : "自动识别";
  const help = form.querySelector("[data-client-mode-help]");
  if (help) help.textContent = manual ? "使用手动地址中；恢复自动识别后可切换协议栈。" : "自动识别按此偏好选择节点地址；指定协议栈不可用时自动回退。";
}

export function createClientAccessProfiles({ api, state, can, notify }, { clientAccess }) {
  return () => {
    document.querySelectorAll("[data-secret-visibility]").forEach(button => {
      button.onclick = () => {
        const input = document.getElementById(button.getAttribute("aria-controls"));
        const reveal = input.matches("textarea") ? input.classList.contains("is-masked") : input.type === "password";
        if (input.matches("textarea")) input.classList.toggle("is-masked", !reveal);
        else input.type = reveal ? "text" : "password";
        updateFeedback(button, reveal ? "隐藏" : "显示");
        button.setAttribute("aria-pressed", String(reveal));
      };
    });
    document.querySelectorAll(".client-export-preview").forEach(details => {
      bindEvent(details, "toggle", () => { if (!details.open) maskSecrets(details); });
    });
    document.querySelectorAll("[data-client-parameter-open]").forEach(button => {
      button.onclick = () => {
        const dialog = document.getElementById(button.dataset.clientParameterOpen);
        maskSecrets(dialog);
        dialog?.showModal();
      };
    });
    document.querySelectorAll("[data-client-display-open]").forEach(button => {
      button.onclick = () => {
        const dialog = document.getElementById(button.dataset.clientDisplayOpen);
        const form = dialog.querySelector("form");
        if (form.dataset.busy === "1") return;
        // Reconcile preserves dirty controls; start a new edit from saved defaults.
        dialog.querySelectorAll("input[name], select[name]").forEach(control => {
          control.value = control.tagName === "SELECT" ? control.dataset.savedMode || "auto" : control.defaultValue;
        });
        form.querySelector("[data-client-display-error]").hidden = true;
        syncAddressDraft(form);
        dialog?.showModal();
      };
    });
    document.querySelectorAll("[data-client-parameter-close], [data-client-display-close]").forEach(button => {
      button.onclick = () => button.closest("dialog")?.close();
    });
    document.querySelectorAll(".client-parameter-dialog, .client-display-dialog").forEach(dialog => {
      bindDialogBackdrop(dialog);
      bindEvent(dialog, "close", () => maskSecrets(dialog));
    });
    document.querySelectorAll("[data-copy-target]").forEach(button => {
      button.onclick = async () => {
        if (button.disabled) return;
        const targetID = button.dataset.copyTarget;
        if (!targetID) return;
        const input = document.getElementById(targetID.replace(/^#/, ""));
        const label = button.dataset.copyLabel || "复制";
        const focused = document.activeElement === button;
        button.disabled = true;
        try {
          await copyClientValue(input);
          if (!button.isConnected || button.dataset.copyTarget !== targetID || (button.dataset.copyLabel || "复制") !== label) return;
          updateFeedback(button, "已复制");
          button.dataset.copyState = "success";
          const status = document.querySelector("[data-client-copy-status]");
          if (status) status.textContent = `${label}成功`;
          setTimeout(() => {
            if (!button.isConnected || button.dataset.copyTarget !== targetID) return;
            // A format switch may have reconciled this button while feedback was visible.
            updateFeedback(button, button.dataset.copyLabel || label);
            delete button.dataset.copyState;
          }, 1600);
        } catch (error) {
          notify(`复制失败：${error.message}。请展开配置后手动复制。`, "error");
        } finally {
          if (button.dataset.copyTarget) button.disabled = false;
          if (focused && document.activeElement === document.body && button.isConnected) button.focus({ preventScroll: true });
        }
      };
    });
    document.querySelectorAll("[data-config-agent]").forEach(link => {
      link.onclick = () => {
        state.data.agentId = state.data.liveAgent = link.dataset.configAgent;
        state.data.engine = state.data.liveEngine = link.dataset.configEngine;
        state.data.liveConfigSource = "";
        link.href = `#live-config?${new URLSearchParams({ agent: state.data.liveAgent, engine: state.data.liveEngine })}`;
      };
    });
    document.querySelectorAll("[data-client-address-agent]").forEach(form => {
      bindEvent(form.elements.namedItem("address"), "input", () => syncAddressDraft(form));
      bindEvent(form.querySelector("[data-clear-client-address]"), "click", () => {
        form.elements.namedItem("address").value = "";
        syncAddressDraft(form);
        form.elements.namedItem("address").focus();
      });
      bindEvent(form, "submit", async event => {
        event.preventDefault();
        if (!can("agents.manage") || form.dataset.busy === "1") return;
        const addressInput = form.elements.namedItem("address");
        const address = addressInput.value.trim();
        const payload = { name: form.elements.namedItem("name").value.trim(), profile: {
          engine: form.dataset.clientProfileEngine, tag: form.dataset.clientProfileTag, port: Number(form.dataset.clientProfilePort),
        } };
        if (address !== addressInput.defaultValue.trim()) payload.address = address;
        const modeInput = form.elements.namedItem("address_mode");
        if (modeInput && !address && modeInput.value !== (modeInput.dataset.savedMode || "auto")) payload.address_mode = modeInput.value;
        const errorBox = form.querySelector("[data-client-display-error]");
        errorBox.hidden = true;
        form.dataset.busy = "1";
        form.setAttribute("aria-busy", "true");
        const controls = [...form.querySelectorAll("input, select, button")].filter(control => !control.disabled);
        controls.forEach(control => { control.disabled = true; });
        try {
          await api(`/agents/${encodeURIComponent(form.dataset.clientAddressAgent)}/client-address`, { method: "PUT", body: JSON.stringify(payload) });
          // Close only after the write succeeds; refresh all exports because a config can be reused.
          form.closest("dialog")?.close();
          const refreshed = await clientAccess();
          notify(refreshed ? "客户端显示参数已保存" : "参数已保存，请重试刷新以获取最新配置", refreshed ? "success" : "error");
        } catch (error) {
          errorBox.textContent = `保存失败：${error.message}`;
          errorBox.hidden = false;
        } finally {
          delete form.dataset.busy;
          form.removeAttribute("aria-busy");
          controls.forEach(control => { control.disabled = false; });
          syncAddressDraft(form);
        }
      });
    });
  };
}
