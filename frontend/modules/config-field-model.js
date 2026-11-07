// The existing root-field API replaces a whole field. Inbound structures and
// outbounds (which include exits paired with individual JSON files) must not be
// presented as common-only mutations. They remain in the source/advanced editor.
export function commonConfigFields(engine, fields) {
  const structural = new Set(["inbounds", "endpoints", "listeners", "servers", "shadowsocks", "outbounds"]);
  if (engine === "mihomo") {
    for (const key of ["port", "socks-port", "redir-port", "tproxy-port", "mixed-port",
      "ss-config", "vmess-config", "tunnels", "tun", "tuic-server"]) structural.add(key);
  }
  return fields.filter(field => !structural.has(field.key) &&
    (engine === "ss-rust" ? ["global", "override"].includes(field.scope) : !["inbound", "structure"].includes(field.scope)));
}
