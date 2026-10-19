import { bindEvent } from "./refresh.js";
import { configJSONMembers } from "./config-json.js";
import { updateFeedback } from "./presence-motion.js";

// Deliberately check only recognizable foreign formats. Unknown native fields
// remain editable and are checked by the selected core on explicit validation.
export function configSourceProblem(engine, content) {
  if (!content.trim()) return "配置内容为空，请输入完整配置。";
  if (new TextEncoder().encode(content).length > 2097152) return "配置超过 2 MiB 上限。";
  if (content.includes("\u0000")) return "文件含有二进制内容，请选择 UTF-8 配置文本。";
  let document;
  try {
    if (engine !== "mihomo") configJSONMembers(content);
    document = JSON.parse(content);
  } catch (error) {
    return engine === "mihomo" ? "" : `JSON 格式错误：${error.message}`;
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
  let request = 0, applying = false, previewTimer, previewController;
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
    const problem = configSourceProblem(engine, content);
    updateFeedback(status, problem || (name ? `已导入 ${name}，尚未保存。` : "已创建空白草稿，请输入完整配置。"));
    if (problem || !content.trim()) return;
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
        updateFeedback(status, `草稿已保留：${error.message}`);
    }
  };
  bindEvent(root.querySelector("[data-config-file-open]"), "click", () => { if (current()) picker.click(); });
  bindEvent(root.querySelector("[data-config-new]"), "click", () => replace(""));
  bindEvent(picker, "change", async () => {
    const file = picker.files?.[0]; picker.value = "";
    if (!file || !current()) return;
    const ticket = ++request;
    if (file.size > 2097152) { notify("文件超过 2 MiB 上限，当前草稿已保留。", "error"); return; }
    try {
      const bytes = await file.arrayBuffer();
      const content = new TextDecoder("utf-8", {fatal:true}).decode(bytes);
      if (current() && ticket === request) await replace(content, file.name, ticket);
    } catch (error) { if (current() && ticket === request) notify(`无法读取 UTF-8 配置文件：${error.message}。当前草稿已保留。`, "error"); }
  });
  bindEvent(input, "input", () => {
    if (applying) return;
    const ticket = ++request;
    cancelPreview();
    if (["source", "common"].includes(files.document.selected())) {
      const problem = configSourceProblem(engine, files.document.selected() === "source" || !files.document.segmented ? input.value : (() => { try { return files.content(); } catch { return input.value; } })());
      if (problem) updateFeedback(status, problem);
      else if (status?.textContent.startsWith("JSON 格式错误") || status?.textContent.startsWith("当前选择") || status?.textContent.startsWith("配置内容为空")) updateFeedback(status, "");
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
            updateFeedback(status, `草稿已保留：${error.message}`);
          }
        }
      }, 250);
    }
  }, {bindingKey:"config-source"});
}
