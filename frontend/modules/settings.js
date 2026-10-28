import { bindAPIKeys } from "./settings-api-keys.js";
import { createSettingsView } from "./settings-view.js";
import { createSettingsBindings } from "./settings-bindings.js";
import { bindSettingsNavigation, focusSettingsSection, syncSettingsSelection } from "./settings-nav.js";

export function installSettings(ctx) {
  const { api, state } = ctx;
  const render = createSettingsView(ctx);
  const bind = createSettingsBindings(ctx);
  let settingsRequest = 0;

  async function settings() {
    if (document.querySelector('#settings-form[aria-busy="true"], .api-key-dialog[open]')) return false;
    const request = ++settingsRequest;
    const accountData = state.data;
    const [item, deployment] = await Promise.all([api("/settings"), api("/settings/deployment")]);
    if (request !== settingsRequest || state.route !== "settings" || state.data !== accountData) return;
    if (document.querySelector('#settings-form[aria-busy="true"], .api-key-dialog[open]')) return false;
    state.data.settings = item;
    const { writable } = render(item, deployment);
    syncSettingsSelection(state.anchor);
    bindSettingsNavigation({ state });
    bind({ item, writable, accountData });
    bindAPIKeys(ctx);
  }
  // A settings section link keeps this rendered page and only moves inside it.
  settings.focusSection = (hash) => focusSettingsSection({ state, hash });
  return settings;
}
