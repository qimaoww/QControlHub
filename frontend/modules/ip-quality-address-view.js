import { ipQualityValue } from "./ip-quality-model.js";

// Keep the address intact when copied, with wrapping opportunities only after
// whole colon separators (including ::). Always escape before adding markup.
export function ipQualityAddressMarkup(ip, esc) {
  return `<bdi dir="ltr" class="ip-quality-address-value">${esc(ipQualityValue(ip)).replace(/(:+)/g, "$1<wbr>")}</bdi>`;
}
