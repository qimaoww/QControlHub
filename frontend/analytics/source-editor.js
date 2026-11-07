import { formatConfigContent } from "../modules/code-format.js";
import { actions, badge, button, muted } from "./ui.js";

export function bindSourceEditorTools(ctx, form, engine, original, changed, writable) {
  const input = form.elements.content;
  const feedback = muted("");
  feedback.id = `${input.id}-feedback`;
  feedback.setAttribute("role", "status");
  input.setAttribute("aria-describedby", [input.getAttribute("aria-describedby"), feedback.id].filter(Boolean).join(" "));
  const language = badge("");
  const busy = () => form.getAttribute("aria-busy") === "true";
  const reset = writable && button("重置源码", () => {
    if (busy()) return;
    input.value = original(); changed(); update();
  });
  const format = writable && button("格式化源码", () => {
    if (busy()) return;
    try { input.value = formatConfigContent(input.value, engine() === "mihomo" ? "YAML" : "JSON"); changed(); update(); }
    catch (error) { feedback.textContent = engine() === "mihomo" && input.value.trim() ? "暂不支持 YAML 格式化，已保留原文。" : error.message; }
  });
  function update() {
    const json = engine() !== "mihomo";
    language.textContent = `${engine()} · ${json ? "JSON" : "YAML"}`;
    input.setAttribute("aria-label", `${engine()} 配置源码`);
    let error = !input.value.trim() ? "配置源码不能为空。" : new Blob([input.value]).size > 2 * 1024 * 1024 ? "配置源码超过 2 MiB 上限。" : "";
    if (!error && json) {
      try { JSON.parse(input.value); } catch { error = "JSON 语法错误，请检查括号、逗号和引号。"; }
    }
    input.setCustomValidity(error); feedback.textContent = error;
    const submit = form.querySelector('[type="submit"]');
    if (submit && !busy()) submit.disabled = Boolean(error);
    if (reset) reset.disabled = busy() || input.value === original();
    if (format) format.disabled = busy();
  }
  input.closest(".a-editor").prepend(actions(language, reset, format));
  input.parentElement.append(feedback);
  form.addEventListener("input", update);
  form.addEventListener("change", update);
  form.addEventListener("submit", update);
  form.querySelector('[type="submit"]')?.addEventListener("mutationend", update);
  update();
  return update;
}
