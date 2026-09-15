import { createIPQualityView, demoIPQuality } from "./ip-quality-view.js";
import { createIPQualityBindings } from "./ip-quality-bindings.js";

const today = () => {
  const now = new Date();
  return [now.getFullYear(), String(now.getMonth() + 1).padStart(2, "0"), String(now.getDate()).padStart(2, "0")].join("-");
};

export function installIPQuality(ctx) {
  const { api, state, shell } = ctx;
  const view = createIPQualityView(ctx);
  let request = 0;
  const render = (date, data, error = "") => { view({ date, data, error }); bind(); };
  const bind = createIPQualityBindings({ state, render, load });
  async function load(date = state.data.ipQualityDate || today(), { force = false } = {}) {
    state.data.ipQualityDate = date;
    const serial = ++request;
    let data;
    let error = "";
    if (!force && state.data.ipQualityCache?.[date]) data = state.data.ipQualityCache[date];
    if (!data) {
      try {
        data = await api(`/ip-quality?date=${encodeURIComponent(date)}`);
        state.data.ipQualityCache ||= {};
        state.data.ipQualityCache[date] = data;
      } catch (err) {
        error = err?.message || "接口暂不可用";
        data = demoIPQuality(date);
      }
    }
    if (serial !== request || state.route !== "ip-quality") return;
    render(date, data, error);
  }
  return load;
}
