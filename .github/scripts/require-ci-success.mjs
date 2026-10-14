// A skipped/cancelled/missing job is not success, including when GitHub's
// default needs semantics would otherwise skip the required check entirely.
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
const required = ["debian", "alpine", "upgrade-sandbox", "core-permissions", "build-images"];
export function requireSuccess(needs) {
  for (const name of required) if (needs[name]?.result !== "success") {
    throw new Error(`Required job ${name}: ${needs[name]?.result || "missing"}`);
  }
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  requireSuccess(JSON.parse(process.env.QCH_CI_NEEDS || "{}"));
}
