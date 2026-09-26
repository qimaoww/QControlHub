import { bindEvent } from "./refresh.js";
import { ConfigFormatError, formatConfigContent } from "./code-format.js";

export function bindCodeEditors() {
  const formatCodeBytes = (value) => {
    if (value < 1024) return `${value} B`;
    if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
    return `${(value / (1024 * 1024)).toFixed(2)} MiB`;
  };
  document.querySelectorAll("[data-code-editor]").forEach((editor) => {
    const input = editor.querySelector("[data-code-input]");
    const gutter = editor.querySelector("[data-line-numbers]");
    const byteLabel = editor.querySelector("[data-code-bytes]");
    const position = editor.querySelector("[data-code-position]");
    const status = editor.querySelector("[data-code-status]");
    const statusDot = editor.querySelector("[data-code-status-dot]");
    const validation = editor.querySelector("[data-code-validation]");
    const reset = editor.querySelector("[data-code-reset]");
    const format = editor.querySelector("[data-code-format]");
    if (!input || !gutter) return;
    const form = input.closest("form");
    const maxBytes = Number(editor.dataset.codeMaxBytes) || 2 * 1024 * 1024;
    const original = input.value;
    const baselineStatus = status?.textContent || "已保存";
    const baselineValidation = validation?.textContent || "";
    input.setAttribute("wrap", "off");
    const updatePosition = () => {
      if (!position) return;
      const before = input.value.slice(0, input.selectionStart);
      const lineStart = before.lastIndexOf("\n") + 1;
      position.textContent = `行 ${before.split("\n").length}，列 ${before.length - lineStart + 1}`;
    };
    const inspect = () => {
      const size = new Blob([input.value]).size;
      if (size > maxBytes)
        return {
          valid: false,
          status: "内容过大",
          message: "配置源码超过 2 MiB 上限，无法提交。",
          size,
        };
      if (!input.value.trim())
        return {
          valid: false,
          status: "内容为空",
          message: "配置源码不能为空。",
          size,
        };
      if ((editor.dataset.codeLanguage || "").toUpperCase() === "JSON") {
        try {
          JSON.parse(input.value);
          return { valid: true, json: true, size };
        } catch {
          return {
            valid: false,
            status: "语法错误",
            message: "JSON 语法错误，请检查括号、逗号和引号。",
            size,
          };
        }
      }
      return { valid: true, size };
    };
    const blockSubmit = (blocked) => {
      form
        ?.querySelectorAll('button[type="submit"], input[type="submit"]')
        .forEach((control) => {
          if (blocked && !control.disabled) {
            control.disabled = true;
            control.dataset.codeBlocked = "1";
          } else if (!blocked && control.dataset.codeBlocked === "1") {
            control.disabled = false;
            delete control.dataset.codeBlocked;
          }
        });
    };
    const update = () => {
      const result = inspect();
      const dirty = editor.configFileController ? editor.configFileController.dirty() : input.value !== original;
      gutter.textContent = Array.from(
        { length: Math.max(1, input.value.split("\n").length) },
        (_, index) => String(index + 1),
      ).join("\n");
      if (byteLabel)
        byteLabel.textContent =
          `${formatCodeBytes(result.size)}${result.size > maxBytes ? " / 2 MiB" : ""}`;
      editor.dataset.dirty = dirty ? "1" : "0";
      editor.dataset.codeValid = result.valid ? "1" : "0";
      input.classList.toggle("is-invalid", !result.valid);
      if (reset) reset.disabled = !(editor.configFileController?.currentDirty?.() ?? dirty) || input.readOnly;
      if (!result.valid) {
        if (status) status.textContent = result.status;
        if (validation) validation.textContent = result.message;
        if (statusDot) statusDot.style.background = "var(--red)";
      } else if (dirty) {
        if (status) status.textContent = "未保存";
        if (validation)
          validation.textContent = result.json
            ? "JSON 语法有效；提交后仍会由节点内核校验。"
            : baselineValidation;
        if (statusDot) statusDot.style.background = "var(--amber)";
      } else {
        if (status) status.textContent = baselineStatus;
        if (validation) validation.textContent = baselineValidation;
        if (statusDot) statusDot.style.background = "var(--green)";
      }
      blockSubmit(!result.valid);
      updatePosition();
    };
    bindEvent(input, "input", update);
    bindEvent(input, "scroll", () => {
      gutter.scrollTop = input.scrollTop;
    });
    ["click", "keyup", "select"].forEach((name) =>
      bindEvent(input, name, updatePosition),
    );
    bindEvent(input, "keydown", (event) => {
      if (
        event.key !== "Tab" ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey
      )
        return;
      event.preventDefault();
      const start = input.selectionStart;
      const end = input.selectionEnd;
      if (!event.shiftKey && start === end) {
        input.setRangeText("  ", start, end, "end");
      } else {
        const value = input.value;
        const lineStart = value.lastIndexOf("\n", Math.max(0, start - 1)) + 1;
        const nextBreak = value.indexOf("\n", end);
        const lineEnd = nextBreak === -1 ? value.length : nextBreak;
        const replacement = value
          .slice(lineStart, lineEnd)
          .split("\n")
          .map((line) =>
            event.shiftKey ? line.replace(/^(?: {1,2}|\t)/, "") : `  ${line}`,
          )
          .join("\n");
        input.setRangeText(replacement, lineStart, lineEnd, "select");
      }
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    bindEvent(reset, "click", () => {
      if (input.readOnly) return;
      if (editor.configFileController) editor.configFileController.reset();
      else input.value = original;
      input.setSelectionRange(0, 0);
      update();
      input.focus();
    });
    const runFormat = () => {
      if (!format || input.readOnly || input.disabled) return;
      const selectionStart = input.selectionStart;
      const selectionEnd = input.selectionEnd;
      const scrollTop = input.scrollTop;
      const scrollLeft = input.scrollLeft;
      if (new Blob([input.value]).size > maxBytes) {
        if (validation) validation.textContent = "配置源码超过 2 MiB 上限，无法格式化。";
        if (status) status.textContent = "内容过大";
        if (statusDot) statusDot.style.background = "var(--red)";
        return;
      }
      try {
        const formatted = formatConfigContent(
          input.value,
          editor.dataset.codeLanguage || "",
        );
        if (formatted === input.value) {
          if (validation)
            validation.textContent = "内容已符合排版格式。";
          return;
        }
        if (new Blob([formatted]).size > maxBytes) {
          if (validation)
            validation.textContent = "格式化后超过 2 MiB 上限，已保留原文。";
          if (status) status.textContent = "内容过大";
          if (statusDot) statusDot.style.background = "var(--red)";
          return;
        }
        input.value = formatted;
        const nextLength = input.value.length;
        input.setSelectionRange(
          Math.min(selectionStart, nextLength),
          Math.min(selectionEnd, nextLength),
        );
        input.scrollTop = scrollTop;
        input.scrollLeft = scrollLeft;
        update();
        if (validation)
          validation.textContent = "已格式化；内容未保存，需提交校验。";
        input.focus();
      } catch (error) {
        input.setSelectionRange(
          Math.min(selectionStart, input.value.length),
          Math.min(selectionEnd, input.value.length),
        );
        input.scrollTop = scrollTop;
        input.scrollLeft = scrollLeft;
        if (validation)
          validation.textContent =
            error instanceof ConfigFormatError
              ? error.message
              : "当前内容无法安全格式化。";
        if (status) status.textContent = "无法格式化";
        if (statusDot) statusDot.style.background = "var(--red)";
      }
    };
    bindEvent(format, "click", runFormat);
    bindEvent(
      form,
      "submit",
      (event) => {
        if (inspect().valid) return;
        event.preventDefault();
        update();
        input.focus();
      },
      { capture: true },
    );
    update();
  });
}
