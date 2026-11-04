import { bindEvent } from "./refresh.js";
import { configJSONMembers } from "./config-json.js";
import { updateFeedback } from "./presence-motion.js";
import { redactDiagnostic } from "./errors.js";

// Deliberately check only recognizable foreign formats. Unknown native fields
// remain editable and are checked by the selected core on explicit validation.
const formatBytes = value => value < 1024
  ? `${value} B`
  : value < 1024 * 1024 ? `${(value / 1024).toFixed(1)} KiB` : `${(value / (1024 * 1024)).toFixed(2)} MiB`;

const jsonProblem = (content, error) => {
  const text = String(error?.message || "");
  const explicit = /line (\d+) column (\d+)/.exec(text);
  const offset = /position (\d+)/.exec(text);
  let line = Number(explicit?.[1]) || 0;
  let column = Number(explicit?.[2]) || 0;
  if ((!line || !column) && offset) {
    const before = content.slice(0, Number(offset[1]));
    line = before.split("\n").length;
    column = before.length - before.lastIndexOf("\n");
  }
  if (!line || !column) {
    // Parsers without an offset (truncated input) fail at the end.
    line = content.split("\n").length;
    column = content.length - content.lastIndexOf("\n");
  }
  const reason = text
    .replace(/\s*in JSON at position \d+.*$/, "")
    .replace(/\s*at position \d+.*$/, "")
    .trim() || "内容无法解析";
  return line ? `JSON 语法错误（第 ${line} 行第 ${column} 列）：${reason}。` : `JSON 语法错误：${reason}。`;
};

export function configSourceProblem(engine, content) {
  if (!content.trim()) return "配置源码不能为空；点击「恢复当前对象」可取回已保存内容。";
  const size = new TextEncoder().encode(content).length;
  if (size > 2097152) {
    const over = size - 2097152;
    const lines = content.split("\n");
    let total = 0;
    let line = lines.length;
    for (let index = lines.length - 1; index >= 0; index -= 1) {
      total += new TextEncoder().encode(lines[index]).length + (index < lines.length - 1 ? 1 : 0);
      line = index + 1;
      if (total >= over) break;
    }
    return `配置源码超出 2 MiB 上限 ${formatBytes(over)}；约从第 ${line} 行起超限，可删除该行之后的内容，或改用「导入文件」分批导入。`;
  }
  if (content.includes("\u0000")) return "文件含有二进制内容，请选择 UTF-8 配置文本。";
  let document;
  try {
    if (engine !== "mihomo") configJSONMembers(content);
    document = JSON.parse(content);
  } catch (error) {
    return engine === "mihomo" ? "" : jsonProblem(content, error);
  }
  if (!document || Array.isArray(document) || typeof document !== "object") return "完整配置必须是对象。";
  const inbounds = Array.isArray(document.inbounds) ? document.inbounds : [];
  if (engine === "xray" && inbounds.some(entry => entry?.type && !entry.protocol)) return "当前选择 Xray，文件包含 sing-box 的 type 入站字段。请核对内核或修改配置。";
  if (engine === "sing-box" && inbounds.some(entry => entry?.protocol && !entry.type)) return "当前选择 sing-box，文件包含 Xray 的 protocol 入站字段。请核对内核或修改配置。";
  if (engine === "ss-rust" && document.inbounds) return "当前选择 Shadowsocks Rust，文件包含其他内核的 inbounds 字段。";
  if (engine !== "ss-rust" && document.server_port !== undefined && document.method) return "文件包含 Shadowsocks Rust 服务端配置，请核对所选内核。";
  if (engine === "mihomo" && document.inbounds) return "当前选择 Mihomo，文件包含其他内核的 inbounds 字段。";
  return "";
}

export function bindConfigSourceEditor({state, api, confirmAction, notify}, {root, files, workspace, workspaces, current}) {
  const picker = root?.querySelector("[data-config-file-input]");
  if (!picker || !files) return;
  const input = root.querySelector("[data-code-input]"), status = root.querySelector("[data-workspace-result]");
  const engine = workspace.engine;
  let request = 0, applying = false, previewTimer, previewController, sourceMessage;
  const report = (message, tone = "") => {
    if (!status) return;
    sourceMessage = message;
    status.className = message ? `alert preset-submit-status ${tone}` : "";
    status.setAttribute("role", tone === "error" ? "alert" : "status");
    updateFeedback(status, message);
  };
  const jumpToProblem = (line, column) => {
    if (!input || input.readOnly) return;
    const lines = input.value.split("\n");
    const offset = lines.slice(0, Math.max(0, line - 1)).reduce((total, item) => total + item.length + 1, 0) + Math.max(0, column - 1);
    input.focus();
    input.setSelectionRange(offset, offset);
    const lineHeight = Number.parseFloat(getComputedStyle(input).lineHeight) || 20;
    input.scrollTop = Math.max(0, lineHeight * (line - 1) - input.clientHeight / 2);
  };
  const showProblem = problem => {
    const validation = root.querySelector("[data-code-validation]");
    if (validation && root.querySelector("[data-code-editor]")?.dataset.codeValid === "0") {
      if (status?.textContent === sourceMessage) report("");
      validation.querySelector?.("[data-code-jump]")?.remove();
      updateFeedback(validation, problem);
      const found = /第 (\d+) 行第 (\d+) 列/.exec(problem);
      if (found && typeof document.createElement === "function") {
        const jump = document.createElement("button");
        jump.type = "button";
        jump.className = "button small";
        jump.dataset.codeJump = "1";
        jump.textContent = `跳到第 ${found[1]} 行`;
        jump.addEventListener("click", () => jumpToProblem(Number(found[1]), Number(found[2])));
        validation.append?.(jump);
      }
    } else report(problem, "error");
  };
  const cancelPreview = () => { clearTimeout(previewTimer); previewController?.abort(); };
  state.routeSignal?.addEventListener("abort", cancelPreview, {once:true});
  const replace = async (content, name, ticket = ++request) => {
    if (!current() || ticket !== request) return;
    workspaces.capture();
    if (workspaces.dirty() && !(await confirmAction("替换完整配置会覆盖当前工作区未保存的内容，确定继续？", name ? "导入本地配置文件" : "新建空白配置"))) return;
    if (!current() || ticket !== request) return;
    state.data.presetDrafts?.clear(`${workspace.agentId}|${engine}|${workspace.sourceMode}|`);
    workspace.formDrafts = [];
    applying = true;
    try { files.replace(content); } finally { applying = false; }
    workspaces.capture();
    input.focus({preventScroll:true});
    if (!name && !content.trim()) { report("空白草稿已创建", "success"); return; }
    const problem = configSourceProblem(engine, content);
    if (problem) { showProblem(problem); return; }
    report(`已导入 ${name}`, "success");
    try {
      const preview = await api(`/agents/${encodeURIComponent(workspace.agentId)}/configs/${encodeURIComponent(engine)}/workspace/preview`, {
        method:"POST", body:JSON.stringify({content, expected_version:workspace.saved?.version || 0}),
      });
      if (!current() || ticket !== request || files.document.text() !== content) return;
      files.document.updateInbounds(preview.inbound_targets || preview.inbounds || []);
      applying = true;
      try { files.display(); } finally { applying = false; }
    } catch (error) {
      if (current() && ticket === request && files.document.text() === content)
        report(redactDiagnostic(error.message) || "配置校验失败", "error");
    }
  };
  bindEvent(root.querySelector("[data-config-file-open]"), "click", () => { if (current()) picker.click(); });
  bindEvent(root.querySelector("[data-config-new]"), "click", () => replace(""));
  bindEvent(picker, "change", async () => {
    const file = picker.files?.[0]; picker.value = "";
    if (!file || !current()) return;
    const ticket = ++request;
    if (file.size > 2097152) { notify("文件超过 2 MiB 上限", "error"); return; }
    try {
      const bytes = await file.arrayBuffer();
      const content = new TextDecoder("utf-8", {fatal:true}).decode(bytes);
      if (current() && ticket === request) await replace(content, file.name, ticket);
    } catch (error) { if (current() && ticket === request) notify(`无法读取 UTF-8 配置文件：${redactDiagnostic(error.message)}`, "error"); }
  });
  bindEvent(input, "input", () => {
    if (applying) return;
    const ticket = ++request;
    cancelPreview();
    if (status?.textContent === sourceMessage) report("");
    if (["source", "common"].includes(files.document.selected())) {
      const problem = configSourceProblem(engine, files.document.selected() === "source" || !files.document.segmented ? input.value : (() => { try { return files.content(); } catch { return input.value; } })());
      if (problem) showProblem(problem);
    }
    if (!files.document.segmented && !input.readOnly && !configSourceProblem(engine, input.value)) {
      const content = input.value;
      previewTimer = setTimeout(async () => {
        if (!current() || ticket !== request) return;
        previewController = new AbortController();
        try {
          const preview = await api(`/agents/${encodeURIComponent(workspace.agentId)}/configs/${encodeURIComponent(engine)}/workspace/preview`, {
            method:"POST", body:JSON.stringify({content, expected_version:workspace.saved?.version || 0}), signal:previewController.signal,
          });
          if (!current() || ticket !== request) return;
          workspace.inbounds = preview.inbound_targets || preview.inbounds || [];
          files.document.updateInbounds(workspace.inbounds); files.refreshObjects();
        } catch (error) {
          if (error.name !== "AbortError" && current() && ticket === request) {
            files.document.updateInbounds([]); files.refreshObjects();
            report(redactDiagnostic(error.message) || "配置校验失败", "error");
          }
        }
      }, 250);
    }
  }, {bindingKey:"config-source"});
}
