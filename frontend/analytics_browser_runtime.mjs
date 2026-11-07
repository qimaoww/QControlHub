// Isolated browser-test entrypoint. Production selection remains guarded by
// the complete-theme registry; this never runs a classic renderer.
import { accountRequest } from "./modules/account-request.js";
import { startAnalytics } from "./analytics/app.js";
const session = await accountRequest("/auth/session").catch(error => { if (error.status === 401) return null; throw error; });
// Reloads after a real preference save enter the production gateway.
history.replaceState(null, "", `/${location.hash}`);
startAnalytics({ app: document.querySelector("#app"), session });
