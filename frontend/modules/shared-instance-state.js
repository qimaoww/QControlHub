import { sharedInstancePresentation } from "./shared-instance-model.js";
export { sharedConfigPresentation, sharedInstancePresentation, sharedAllocationText } from "./shared-instance-model.js";

export function updateSharedInstanceMarkup(root, agent, engine, format) {
  const status = sharedInstancePresentation(agent, engine);
  if (!status) return;
  const version = root.querySelector(`[data-instance-version="${engine}"]`);
  if (version) {
    version.textContent = status.version;
    version.title = status.instance?.observed_at ? `实例状态更新于 ${format(status.instance.observed_at)}` : "等待实例状态上报";
  }
  const reason = root.querySelector(`[data-instance-reason="${engine}"]`);
  if (reason) {
    reason.textContent = status.reason;
    reason.hidden = !status.reason;
  }
}
