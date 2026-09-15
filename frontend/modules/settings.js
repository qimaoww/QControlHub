import { createSettingsView } from "./settings-view.js";
import { createSettingsBindings } from "./settings-bindings.js";

export function installSettings(ctx) {
  const { api, state } = ctx;
  const render = createSettingsView(ctx);
  const bind = createSettingsBindings(ctx);
  let settingsRequest = 0;

  async function settings() {
    const request = ++settingsRequest;
    const accountData = state.data;
    const [item, deployment] = await Promise.all([api("/settings"), api("/settings/deployment")]);
    if (request !== settingsRequest || state.route !== "settings" || state.data !== accountData) return;
    state.data.settings = item;
    const { writable } = render(item, deployment);
    bind({ item, writable, accountData });
  }
  return settings;
}
