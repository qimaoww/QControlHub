// NextTrace v1.7.6 printer/realtime_common.go: formatWhoisPrefix and
// highlightRealtimeBackbone. Keep the original case-sensitive network labels.
export function networkName(geo) {
  const prefix = String(geo?.whois || "").split("-").slice(0, 2).join("-");
  return !prefix || prefix.startsWith("RFC") || prefix.startsWith("DOD") ? "—" : `[${prefix}]`;
}
export function networkKind(geo, ip) {
  const asn = String(geo?.asnumber || geo?.asn || "").replace(/^AS/i, "");
  return ["58807", "10099", "4809", "9929", "23764"].includes(asn) ||
    ["[CTG-CN]", "[CNC-BACKBONE]", "[CUG-BACKBONE]", "[CMIN2-NET]"].includes(networkName(geo)) ||
    String(ip || "").startsWith("59.43.") ? "hop" : "asn";
}
