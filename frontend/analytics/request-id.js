// getRandomValues is also available on explicitly allowed HTTP deployments.
export function requestID() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, "0")).join("");
}
