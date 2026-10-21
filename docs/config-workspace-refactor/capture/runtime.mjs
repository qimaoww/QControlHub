import {installAgentFixture} from "/assets/browser/agent-fixture.mjs";
const params = new URLSearchParams(location.search);
window.reviewScenario = installAgentFixture("config-layout");
if (params.get("kind") === "form") {
  const {createShellAppearance} = await import("/assets/modules/shell-appearance.js");
  createShellAppearance({data:{}}).applyTheme();
  const {createConfigFixture} = await import("/assets/browser/config-fixture.mjs");
  window.reviewConfig = await createConfigFixture(params.get("engine") || "xray", {protocol:params.get("protocol") || "vless"});
} else {
  location.hash = `#live-config?agent=alpha&engine=${params.get("engine") || "xray"}`;
  await import("/assets/app.js");
}
document.documentElement.dataset.reviewReady = "true";
