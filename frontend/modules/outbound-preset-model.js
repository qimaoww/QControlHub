import { orderNodesBySavedOrder } from "./node-order.js";

export function outboundProtocols(engine) {
  const protocols = [["direct", "直连"], ["vless", "VLESS"], ["vmess", "VMess"], ["trojan", "Trojan"],
    ["shadowsocks", "Shadowsocks / 2022"], ["socks", "SOCKS5"], ["http", "HTTP"]];
  return engine === "sing-box" ? [...protocols, ["hysteria2", "Hysteria 2"], ["tuic", "TUIC"], ["anytls", "AnyTLS"]] : protocols;
}

// Deliberately generate only supported, independent exits. Advanced JSON is
// preserved separately, never reverse-converted through a lossy preset form.
export function outboundPreset(engine, protocol, values, validate = true) {
  if (!["xray", "sing-box"].includes(engine) || !outboundProtocols(engine).some(([key]) => key === protocol))
    throw Error("当前内核不支持此出站预设");
  const tag = values.tag?.trim(), server = values.server?.trim().replace(/^\[|\]$/g, ""), port = Number(values.port);
  if (validate && (!tag || /^qch-(trf-|stat-)/.test(tag))) throw Error("请填写有效的非系统出站标签");
  if (protocol === "direct") return engine === "xray" ? {tag, protocol:"freedom"} : {tag, type:"direct"};
  if (validate && (!server || /[\s/@?#]/.test(server) || !Number.isInteger(port) || port < 1 || port > 65535))
    throw Error("请填写出口服务器地址和 1–65535 范围内的端口，不要填写 URL");
  const credential = values.credential || "", password = values.password || "", username = values.username || "";
  const uuid = ["vless", "vmess", "tuic"].includes(protocol), authentication = ["socks", "http"].includes(protocol);
  if (validate && !authentication && !credential) throw Error(uuid ? "请填写目标入站的 UUID" : "请填写目标入站的密码");
  if (validate && uuid && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(credential))
    throw Error("请填写有效的用户 UUID");
  if (validate && (protocol === "tuic" && !password || authentication && Boolean(username) !== Boolean(password)))
    throw Error("请完整填写认证用户名和密码；TUIC 还需用户密码");
  const transportable = ["vless", "vmess", "trojan"].includes(protocol);
  const tlsRequired = ["hysteria2", "tuic", "anytls"].includes(protocol);
  const security = tlsRequired ? "tls" : transportable || protocol === "http" ? values.security || "none" : "none";
  const transport = transportable ? values.transport || "tcp" : "tcp";
  if (security === "reality" && protocol !== "vless") throw Error("Reality 预设仅用于 VLESS");
  if (!["none", "tls", "reality"].includes(security) || !["tcp", "ws", "grpc", ...(engine === "xray" ? ["xhttp"] : [])].includes(transport))
    throw Error("当前内核不支持此安全或传输选项");
  if (validate && security === "reality" && (!values.serverName?.trim() || !values.publicKey?.trim()))
    throw Error("Reality 需要填写 ServerName 和目标入站的公钥，不是服务端私钥");
  if (validate && security === "reality" && !/^(?:[0-9a-f]{2}){0,8}$/i.test(values.shortId || ""))
    throw Error("Reality Short ID 必须是最多 16 位的偶数长度十六进制字符");
  const sni = values.serverName?.trim() || server;
  const wsHost = transport === "ws" ? values.host?.trim() || (security !== "none" ? sni : "") : "";
  if (validate && wsHost && /[\s/@?#\\]/.test(wsHost)) throw Error("请填写有效的 WS Host，不要填写 URL 或路径");
  const flow = protocol === "vless" ? values.flow || "" : "";
  if (validate && flow && (flow !== "xtls-rprx-vision" || transport !== "tcp" || security === "none"))
    throw Error("Vision 预设需要 TCP + TLS / Reality；其他组合请使用高级 JSON 并校验");
  const out = {tag};
  if (engine === "xray") {
    out.protocol = protocol;
    const peer = {address:server, port};
    if (["vless", "vmess"].includes(protocol)) {
      peer.users = [{id:credential, ...(protocol === "vless" ? {encryption:"none", ...(flow ? {flow} : {})} : {security:"auto"})}];
      out.settings = {vnext:[peer]};
    } else {
      if (authentication) {
        if (username) peer.users = [{user:username, pass:password}];
      } else peer.password = credential;
      if (protocol === "shadowsocks") peer.method = values.method;
      out.settings = {servers:[peer]};
    }
    if (transportable || protocol === "http" && security !== "none") {
      const stream = {network:transport};
      if (transport === "ws") stream.wsSettings = {path:values.path || "/", ...(wsHost ? {host:wsHost} : {})};
      if (transport === "grpc") stream.grpcSettings = {serviceName:values.path || ""};
      if (transport === "xhttp") stream.xhttpSettings = {path:values.path || "/", mode:"auto"};
      if (security === "tls") Object.assign(stream, {security:"tls", tlsSettings:{serverName:sni}});
      if (security === "reality") Object.assign(stream, {security:"reality",
        realitySettings:{serverName:sni, fingerprint:"chrome", publicKey:values.publicKey?.trim(), shortId:values.shortId || ""}});
      out.streamSettings = stream;
    }
  } else {
    Object.assign(out, {type:protocol, server, server_port:port});
    if (uuid) out.uuid = credential;
    if (protocol === "vmess") Object.assign(out, {security:"auto", alter_id:0});
    if (flow) out.flow = flow;
    if (authentication) {
      if (username) Object.assign(out, {username, password});
      if (protocol === "socks") out.version = "5";
    } else if (protocol === "tuic") Object.assign(out, {password, congestion_control:"bbr"});
    else if (!uuid) out.password = credential;
    if (protocol === "shadowsocks") out.method = values.method;
    if (transport === "ws") out.transport = {type:"ws", path:values.path || "/", ...(wsHost ? {headers:{Host:wsHost}} : {})};
    if (transport === "grpc") out.transport = {type:"grpc", service_name:values.path || ""};
    if (security !== "none") out.tls = {enabled:true, server_name:sni};
    if (security === "reality") Object.assign(out.tls, {utls:{enabled:true, fingerprint:"chrome"},
      reality:{enabled:true, public_key:values.publicKey?.trim(), short_id:values.shortId || ""}});
  }
  return out;
}

export function outboundPeers(entries, agentId) {
  return entries.filter(entry => entry.agent_id !== agentId).flatMap(entry =>
    (entry.profiles || []).map(profile => ({...profile, agentId:entry.agent_id, agentName:entry.agent_name,
      agentStatus:entry.agent_status, engine:entry.engine})));
}
