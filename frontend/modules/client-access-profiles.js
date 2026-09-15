import { bindEvent } from "./refresh.js";

import { copyClientValue } from "./client-clipboard.js";
export function createClientAccessProfiles({ api, state, can, notify }, { clientAccess }) {
  return () => {
    document.querySelectorAll("[data-secret-visibility]").forEach((button) => {
      button.onclick = () => {
        const input = button.parentElement.querySelector("input, textarea");
        const reveal = input.matches("textarea")
          ? input.classList.contains("is-masked")
          : input.type === "password";
        if (input.matches("textarea")) input.classList.toggle("is-masked", !reveal);
        else input.type = reveal ? "text" : "password";
        button.textContent = reveal ? "隐藏" : "显示";
        button.setAttribute("aria-pressed", String(reveal));
      };
    });
    document.querySelectorAll("[data-client-parameter-open]").forEach((button) => {
      button.onclick = () => {
        const dialog = document.getElementById(button.dataset.clientParameterOpen);
        dialog?.showModal();
      };
    });
    document.querySelectorAll("[data-client-parameter-close]").forEach((button) => {
      button.onclick = () => button.closest("dialog")?.close();
    });
    document.querySelectorAll("[data-client-display-open]").forEach((button) => {
      button.onclick = () => {
        const dialog = document.getElementById(button.dataset.clientDisplayOpen);
        // Keyed refresh reuses the dialog node, so a dirty input keeps its old
        // value even after the attributes were rewritten. Opening the dialog
        // re-syncs every control with the latest rendered defaults.
        dialog?.querySelectorAll("input[name], select[name]").forEach((control) => {
          if (control.tagName === "SELECT") control.value = control.dataset.savedMode || "auto";
          else control.value = control.defaultValue;
        });
        dialog?.showModal();
      };
    });
    document.querySelectorAll("[data-client-display-close]").forEach((button) => {
      button.onclick = () => button.closest("dialog")?.close();
    });
    document.querySelectorAll(".client-parameter-dialog").forEach((dialog) => {
      dialog.onclick = (event) => {
        if (event.target === dialog) dialog.close();
      };
    });
    document.querySelectorAll(".client-display-dialog").forEach((dialog) => {
      dialog.onclick = (event) => {
        if (event.target === dialog) dialog.close();
      };
    });
    document.querySelectorAll("[data-copy-target]").forEach((button) => {
      button.onclick = async () => {
        const input = document.querySelector(button.dataset.copyTarget);
        try {
          await copyClientValue(input);
          button.textContent = "已复制";
          button.dataset.copyState = "success";
          setTimeout(() => {
            if (!button.isConnected) return;
            button.textContent = "复制";
            delete button.dataset.copyState;
          }, 1600);
        } catch (error) {
          notify(`复制失败：${error.message}`, "error");
        }
      };
    });
    document.querySelectorAll("[data-config-agent]").forEach((link) => {
      link.onclick = () => {
        state.data.agentId = link.dataset.configAgent;
        state.data.engine = link.dataset.configEngine;
        state.data.liveAgent = link.dataset.configAgent;
        state.data.liveEngine = link.dataset.configEngine;
        state.data.liveConfigSource = "";
        link.href = `#live-config?${new URLSearchParams({agent:state.data.liveAgent, engine:state.data.liveEngine})}`;
      };
    });
    document.querySelectorAll("[data-client-address-agent]").forEach((form) => {
      bindEvent(form, "submit", async (event) => {
        event.preventDefault();
        if (!can("agents.manage") || form.dataset.busy === "1") return;
        const button = form.querySelector("button[type=submit]");
        const formData = new FormData(form);
        const address = String(formData.get("address") || "").trim();
        const name = String(formData.get("name") || "").trim();
        const payload = { name, profile: {
          engine: form.dataset.clientProfileEngine,
          tag: form.dataset.clientProfileTag,
          port: Number(form.dataset.clientProfilePort),
        } };
        const addressInput = form.elements.namedItem("address");
        if (address !== addressInput.defaultValue.trim()) payload.address = address;
        const modeInput = form.elements.namedItem("address_mode");
        if (modeInput && !modeInput.disabled && modeInput.value !== (modeInput.dataset.savedMode || "auto"))
          payload.address_mode = modeInput.value;
        form.dataset.busy = "1";
        if (button) button.disabled = true;
        try {
          await api(
            `/agents/${encodeURIComponent(form.dataset.clientAddressAgent)}/client-address`,
            { method: "PUT", body: JSON.stringify(payload) },
          );
          const input = form.elements.namedItem("address");
          if (input) {
            input.value = address;
            input.defaultValue = address;
          }
          form.closest("dialog")?.close();
          notify("客户端显示参数已保存");
          await clientAccess();
        } catch (error) {
          notify(error.message, "error");
        } finally {
          delete form.dataset.busy;
          if (button) button.disabled = false;
        }
      });
    });
    document.querySelectorAll("[data-clear-client-address]").forEach((button) => {
      bindEvent(button, "click", async () => {
        button.disabled = true;
        try {
          await api(
            `/agents/${encodeURIComponent(button.dataset.clearClientAddress)}/client-address`,
            { method: "PUT", body: JSON.stringify({ address: "", profile: {
              engine: button.dataset.clearClientProfileEngine,
              tag: button.dataset.clearClientProfileTag,
              port: Number(button.dataset.clearClientProfilePort),
            } }) },
          );
          const input = button.form?.elements.namedItem("address");
          if (input) {
            input.value = "";
            input.defaultValue = "";
          }
          notify("已恢复自动识别连接地址");
          await clientAccess();
        } catch (error) {
          notify(error.message, "error");
          button.disabled = false;
        }
      });
    });
  };

}
