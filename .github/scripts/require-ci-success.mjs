// A skipped/cancelled/missing job is not success, including when GitHub's
// default needs semantics would otherwise skip the required check entirely.
const required = ["debian", "alpine", "upgrade-sandbox", "mieru-udp", "core-permissions", "build-images"];
export function requireSuccess(needs) {
  for (const name of required) if (needs[name]?.result !== "success") {
    throw new Error(`Required job ${name}: ${needs[name]?.result || "missing"}`);
  }
}
if (process.env.QCH_CI_NEEDS) requireSuccess(JSON.parse(process.env.QCH_CI_NEEDS));
