import { createAccessControlController } from "./access-control-controller.js";

export function installAccessControl(ctx) {
  const { api, state } = ctx;
  const { render, bind, open } = createAccessControlController(ctx, accessControl);
  accessControl.open = open;

  async function accessControl() {
    const data = state.data, epoch = state.navigationEpoch;
    const entries = await api("/access-controls");
    if (state.data !== data || state.navigationEpoch !== epoch || state.route !== "access-control") return;
    state.data.accessControls = entries;
    render(entries);
    bind(entries);
  }

  return accessControl;
}
