import { createAccessControlView } from "./access-control-view.js";
import { createAccessControlBindings } from "./access-control-bindings.js";
import { createAccessControlDialog } from "./access-control-dialog.js";

// Shared by the route and the live-config editor. Embedded dialogs supply
// onSaved and never load or render the standalone route.
export function createAccessControlController(ctx, accessControl = () => {}) {
  const { can } = ctx;
  const editable = () => can("agent-config.write") && can("tasks.execute");
  const { render, card } = createAccessControlView(ctx, { editable });
  const bind = createAccessControlBindings(ctx, { editable, accessControl });
  return { render, bind, open: createAccessControlDialog(ctx, { card, bind }) };
}
