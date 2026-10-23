import { updateFeedback } from "./presence-motion.js";
import { bindEvent } from "./refresh.js";
import { ConfigFormatError, formatConfigContent } from "./code-format.js";

export function bindCodeEditors(root = document) {
  const formatCodeBytes = (value) => {
    if (value < 1024) return `${value} B`;
    if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
    return `${(value / (1024 * 1024)).toFixed(2)} MiB`;
  };
  root.querySelectorAll("[data-code-editor]").forEach((editor) => {
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
    const baseline = () => editor.codeBaseline || { content: original, status: baselineStatus, validation: baselineValidation };
    input.setAttribute("wrap", "off");
    // Mobile keyboards must not rewrite JSON: no autocapitalisation, no
    // autocorrection and no smart quotes inside the source editor.
    input.setAttribute("autocapitalize", "off");
    input.setAttribute("autocorrect", "off");
    input.setAttribute("autocomplete", "off");
    input.setAttribute("spellcheck", "false");
    // Replace the whole document through an editing command so Ctrl+Z can
    // return to the text before a programmatic rewrite.
    const replaceWithHistory = (next) => {
      try { input.focus?.(); input.select?.(); } catch { /* headless smoke DOM */ }
      let applied = false;
      try { applied = document.execCommand("insertText", false, next); } catch { applied = false; }
      if (!applied || input.value !== next) {
        input.value = next;
        input.dispatchEvent(new Event("input", { bubbles: true }));
      }
    };
    const jumpTo = (line, column) => {
      const lines = input.value.split("\n");
      const offset = lines.slice(0, Math.max(0, line - 1)).reduce((total, item) => total + item.length + 1, 0) + Math.max(0, column - 1);
      input.focus();
      input.setSelectionRange(offset, offset);
      const lineHeight = typeof getComputedStyle === "function"
        ? Number.parseFloat(getComputedStyle(input).lineHeight) || 20
        : 20;
      input.scrollTop = Math.max(0, lineHeight * (line - 1) - input.clientHeight / 2);
      updatePosition();
    };
    const updatePosition = () => {
      if (!position) return;
      const before = input.value.slice(0, input.selectionStart);
      const lineStart = before.lastIndexOf("\n") + 1;
      position.textContent = `行 ${before.split("\n").length}，列 ${before.length - lineStart + 1}`;
    };
    const resetLabel = reset?.textContent || "恢复";
    const oversizeMessage = (size) => {
      const over = size - maxBytes;
      const lines = input.value.split("\n");
      let total = 0;
      let line = lines.length;
      for (let index = lines.length - 1; index >= 0; index -= 1) {
        total += new TextEncoder().encode(lines[index]).length + (index < lines.length - 1 ? 1 : 0);
        line = index + 1;
        if (total >= over) break;
      }
      return `配置源码超出 2 MiB 上限 ${formatCodeBytes(over)}；约从第 ${line} 行起超限，可删除该行之后的内容，或改用「导入文件」分批导入。`;
    };
    const inspect = () => {
      const size = new Blob([input.value]).size;
      if (size > maxBytes)
        return {
          valid: false,
          status: "内容过大",
          message: oversizeMessage(size),
          size,
        };
      if (!input.value.trim())
        return {
          valid: false,
          status: "内容为空",
          message: `配置源码不能为空；点击「${resetLabel}」可取回已保存内容。`,
          size,
        };
      if ((editor.dataset.codeLanguage || "").toUpperCase() === "JSON") {
        try {
          JSON.parse(input.value);
          return { valid: true, json: true, size };
        } catch (error) {
          const text = String(error?.message || "");
          const explicit = /line (\d+) column (\d+)/.exec(text);
          const offset = /position (\d+)/.exec(text);
          let line = Number(explicit?.[1]) || 0;
          let column = Number(explicit?.[2]) || 0;
          if ((!line || !column) && offset) {
            const before = input.value.slice(0, Number(offset[1]));
            line = before.split("\n").length;
            column = before.length - before.lastIndexOf("\n");
          }
          if (!line || !column) {
            // Parsers without an offset (truncated input) fail at the end.
            line = input.value.split("\n").length;
            column = input.value.length - input.value.lastIndexOf("\n");
          }
          const reason = text
            .replace(/\s*in JSON at position \d+.*$/, "")
            .replace(/\s*at position \d+.*$/, "")
            .trim() || "内容无法解析";
          return {
            valid: false,
            status: "语法错误",
            message: line
              ? `JSON 语法错误（第 ${line} 行第 ${column} 列）：${reason}。`
              : `JSON 语法错误：${reason}。`,
            line,
            column,
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
    let gutterLines = -1;
    const updateGutter = (count) => {
      // Line numbers only depend on the line count, so typing inside a line
      // must not rebuild hundreds of gutter nodes.
      if (count === gutterLines) return;
      gutterLines = count;
      gutter.textContent = Array.from({ length: count }, (_, index) => String(index + 1)).join("\n");
    };
    const update = () => {
      const result = inspect();
      const dirty = editor.configFileController ? editor.configFileController.dirty() : input.value !== baseline().content;
      updateGutter(Math.max(1, input.value.split("\n").length));
      if (byteLabel)
        byteLabel.textContent =
          `${formatCodeBytes(result.size)}${result.size > maxBytes ? " / 2 MiB" : ""}`;
      editor.dataset.dirty = dirty ? "1" : "0";
      editor.dataset.codeValid = result.valid ? "1" : "0";
      input.classList.toggle("is-invalid", !result.valid);
      if (reset) reset.disabled = !(editor.configFileController?.currentDirty?.() ?? dirty) || input.readOnly || input.disabled;
      if (format) format.disabled = input.readOnly || input.disabled;
      if (!result.valid) {
        if (status) updateFeedback(status, result.status);
        if (validation) {
          validation.querySelector?.("[data-code-jump]")?.remove();
          updateFeedback(validation, result.message);
          if (result.line && typeof document.createElement === "function") {
            const jump = document.createElement("button");
            jump.type = "button";
            jump.className = "button small";
            jump.dataset.codeJump = "1";
            jump.textContent = `跳到第 ${result.line} 行`;
            jump.addEventListener("click", () => jumpTo(result.line, result.column));
            validation.append?.(jump);
          }
        }
        if (statusDot) statusDot.style.background = "var(--red)";
      } else if (dirty) {
        if (status) updateFeedback(status, "未保存");
        if (validation)
          updateFeedback(validation, result.json && !editor.configFileController
            ? "JSON 语法有效；提交后仍会由节点内核校验。"
            : baseline().validation);
        if (statusDot) statusDot.style.background = "var(--amber)";
      } else {
        if (status) updateFeedback(status, baseline().status);
        if (validation) updateFeedback(validation, baseline().validation);
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
      if (input.readOnly || input.disabled) return;
      if (event.key === "Enter" && !event.shiftKey && !event.altKey && !event.ctrlKey && !event.metaKey) {
        const start = input.selectionStart;
        const before = input.value.slice(0, start);
        const lineStart = before.lastIndexOf("\n") + 1;
        const indent = /^[ \t]*/.exec(before.slice(lineStart))[0];
        if (!indent) return;
        event.preventDefault();
        let applied = false;
        try { applied = document.execCommand("insertText", false, "\n" + indent); } catch { applied = false; }
        if (!applied) {
          input.setRangeText("\n" + indent, start, input.selectionEnd, "end");
          input.dispatchEvent(new Event("input", { bubbles: true }));
        }
        return;
      }
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
      else replaceWithHistory(baseline().content);
      input.focus?.();
      input.setSelectionRange(0, 0);
      update();
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.focus();
    });
    const runFormat = () => {
      if (!format || input.readOnly || input.disabled) return;
      const selectionStart = input.selectionStart;
      const selectionEnd = input.selectionEnd;
      const scrollTop = input.scrollTop;
      const scrollLeft = input.scrollLeft;
      if (new Blob([input.value]).size > maxBytes) {
        if (validation) updateFeedback(validation, "配置源码超过 2 MiB 上限，无法格式化。");
        if (status) updateFeedback(status, "内容过大");
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
            updateFeedback(validation, editor.configFileController ? "无需格式化" : "内容已符合排版格式。");
          return;
        }
        if (new Blob([formatted]).size > maxBytes) {
          if (validation)
            updateFeedback(validation, "格式化后超过 2 MiB 上限，已保留原文。");
          if (status) updateFeedback(status, "内容过大");
          if (statusDot) statusDot.style.background = "var(--red)";
          return;
        }
        replaceWithHistory(formatted);
        const nextLength = input.value.length;
        input.setSelectionRange(
          Math.min(selectionStart, nextLength),
          Math.min(selectionEnd, nextLength),
        );
        input.scrollTop = scrollTop;
        input.scrollLeft = scrollLeft;
        update();
        input.dispatchEvent(new Event("input", { bubbles: true }));
        if (validation)
          updateFeedback(validation, editor.configFileController ? "格式化成功" : "已格式化；内容未保存，需提交校验。");
        input.focus();
      } catch (error) {
        input.setSelectionRange(
          Math.min(selectionStart, input.value.length),
          Math.min(selectionEnd, input.value.length),
        );
        input.scrollTop = scrollTop;
        input.scrollLeft = scrollLeft;
        if (validation)
          updateFeedback(validation, error instanceof ConfigFormatError
              ? error.message
              : "当前内容无法安全格式化。");
        if (status) updateFeedback(status, "无法格式化");
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
