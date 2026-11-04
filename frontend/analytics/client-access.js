import { clientAccessAddressChoices, clientAccessEntryForAddress, clientAccessExport, filterClientAccessEntries } from "../modules/client-access-model.js";
import { node, button, actions, stack, section, table, badge, field, code, muted, list, notice } from "./ui.js";

export async function clientAccessPage(ctx) {
  const entries = list(await ctx.api("/client-access"));
  const search = field("搜索接入配置", "search", "", { type: "search", placeholder: "节点、协议、入站或地址" });
  const format = field("导出格式", "format", "url", { options: [["url", "URL"], ["mihomo", "Mihomo YAML"]] });
  const result = node("div", { className: "a-stack" });
  const addressModes = new Map();
  function draw() {
    const shown = filterClientAccessEntries(entries, { query: search.querySelector("input").value });
    result.replaceChildren(...shown.map((entry, index) => {
      const key = JSON.stringify([entry.agent_id, entry.engine, entry.instance_id || "", index]);
      const choices = clientAccessAddressChoices(entry);
      const selected = clientAccessEntryForAddress(entry, addressModes.get(key) || "auto");
      const address = choices.length ? field("接入地址", "address", addressModes.get(key) || "auto", { options: choices.map(choice => [choice.value, choice.label]) }) : muted(selected.address || "暂无接入地址");
      address.querySelector("select")?.addEventListener("change", event => { addressModes.set(key, event.target.value); draw(); });
      const profiles = list(selected.profiles);
      return section(entry.agent_name || entry.agent_id,
        stack(node("div", { className: "a-card-body" }, address),
          entry.error && node("div", { className: "a-card-body" }, notice(entry.error)),
          table(["入站", "协议", "客户端", "操作"], profiles.map(profile => {
            const value = clientAccessExport(profile, format.querySelector("select").value);
            return [profile.tag || "—", profile.protocol || "—", profile.client_name || entry.client_name || "—",
              actions(button("查看配置", () => ctx.dialog(profile.tag || "客户端配置", stack(
                value.error && notice(value.error),
                !value.error && code(value.value),
                profile.profile?.native && section("原生配置", code(typeof profile.profile.native === "string" ? profile.profile.native : JSON.stringify(profile.profile.native, null, 2))),
              ), { wide: true })),
              button("复制", event => void ctx.mutate(event.currentTarget, async () => {
                await navigator.clipboard.writeText(value.value);
                ctx.announce("配置已复制");
              }), { disabled: Boolean(value.error || !value.value) }),
              button("下载", () => {
                const blob = new Blob([value.value], { type: "text/plain;charset=utf-8" });
                const url = URL.createObjectURL(blob);
                const anchor = node("a", { href: url, download: `client-${entry.engine}.${format.querySelector("select").value === "mihomo" ? "yaml" : "txt"}` });
                anchor.click();
                setTimeout(() => URL.revokeObjectURL(url), 0);
              }, { disabled: Boolean(value.error || !value.value) }))];
          }), { label: "客户端接入" })), badge(entry.engine));
    }));
    if (!shown.length) result.replaceChildren(node("p", { className: "a-empty" }, "没有符合条件的接入配置"));
  }
  search.querySelector("input").addEventListener("input", draw);
  format.querySelector("select").addEventListener("change", draw);
  draw();
  return stack(section("接入配置", node("div", { className: "a-card-body a-form-grid" }, search, format)), result);
}
