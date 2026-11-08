import { applySiteBranding, renderSiteLogo } from "../modules/site-branding.js";
import { node, icon, button, actions, section, field, muted, notice } from "./ui.js";

export async function siteBrandingSettings(ctx, setDirty) {
  let saved = await ctx.api("/settings/branding"), draft = saved.logo || "", phase = "idle", reading = 0;
  applySiteBranding(saved);
  const mark = node("span", { className: "a-mark" }, icon("brand"));
  const status = muted("已保存"); status.setAttribute("role", "status");
  const feedback = notice("");
  const upload = field("Logo 图片", "logo_file", "", { type: "file", accept: "image/png,image/jpeg", hint: "PNG / JPEG，最大 256 KiB" });
  const file = upload.querySelector("input");
  file.hidden = true;
  const filename = node("p", { className: "a-muted" }, "未选择图片");
  const choose = button("选择图片", () => file.click());
  upload.querySelector("label").htmlFor = "site-logo-picker";
  choose.id = "site-logo-picker";
  file.after(actions(choose, filename));
  const reset = button("恢复默认", () => { ++reading; draft = ""; file.value = ""; phase = "idle"; feedback.textContent = ""; sync(); });
  const preview = node("div", { className: "a-field" }, node("span", { className: "a-field-label" }, "预览"), actions(mark, "QControlHub", reset));
  const form = ctx.form([node("div", { className: "a-form-grid" }, upload, preview), status, feedback], "保存全站 Logo", async () => {
    const submitted = draft;
    phase = "saving"; sync();
    try {
      const result = await ctx.api("/settings/branding", { method: "PUT", body: { logo: submitted, revision: saved.revision } });
      if (!ctx.current()) return;
      saved = result; applySiteBranding(result); phase = "idle"; sync();
      ctx.announce("全站 Logo 已保存");
    } catch (error) { if (ctx.current()) { phase = "failed"; sync(); } throw error; }
  });
  form.id = "site-branding-form";
  const save = form.querySelector('button[type="submit"]');
  function sync() {
    const changed = draft !== (saved.logo || "");
    setDirty(changed);
    save.disabled = !changed || phase === "saving" || phase === "reading";
    reset.disabled = phase === "saving" || (!draft && phase !== "reading");
    filename.textContent = file.files[0]?.name || "未选择图片";
    status.textContent = phase === "saving" ? "正在保存…" : phase === "reading" ? "正在读取图片…" : phase === "failed" ? "保存失败 · 预览尚未生效" : changed ? "未保存预览" : "已保存";
    renderSiteLogo(mark, draft);
  }
  file.addEventListener("change", async () => {
    const selected = file.files[0], request = ++reading;
    if (!selected) { phase = "idle"; sync(); return; }
    feedback.textContent = "";
    if (!["image/png", "image/jpeg"].includes(selected.type) || selected.size > 256 * 1024) {
      feedback.textContent = "请选择不超过 256 KiB 的 PNG 或 JPEG 图片。"; file.value = ""; phase = "idle"; sync(); return;
    }
    phase = "reading"; sync();
    try {
      const value = await new Promise((resolve, reject) => {
        const reader = new FileReader(); reader.onload = () => resolve(reader.result); reader.onerror = () => reject(new Error("图片读取失败")); reader.readAsDataURL(selected);
      });
      const picture = new Image();
      await new Promise((resolve, reject) => { picture.onload = resolve; picture.onerror = () => reject(new Error("图片内容无效")); picture.src = value; });
      if (picture.naturalWidth > 4096 || picture.naturalHeight > 4096 || picture.naturalWidth * picture.naturalHeight > 4194304) throw new Error("图片宽高须不超过 4096 像素且总像素不超过 4194304");
      if (ctx.current() && request === reading) draft = value;
    } catch (error) { if (ctx.current() && request === reading) feedback.textContent = error.message; }
    finally { if (ctx.current() && request === reading) { phase = "idle"; sync(); } }
  });
  save.addEventListener("mutationend", sync);
  const refresh = async () => {
    const latest = await ctx.api("/settings/branding");
    if (!ctx.current()) return;
    if (draft !== (saved.logo || "") || phase === "saving" || phase === "reading") {
      if (latest.revision !== saved.revision) feedback.textContent = "全站 Logo 已由其他会话修改，当前预览已保留。";
      return;
    }
    saved = latest; draft = latest.logo || ""; applySiteBranding(latest); feedback.textContent = ""; phase = "idle"; sync();
  };
  sync();
  return { element: section("全站 Logo", node("div", { className: "a-card-body a-stack" }, muted("登录页与所有账号统一使用。"), form)), refresh };
}
