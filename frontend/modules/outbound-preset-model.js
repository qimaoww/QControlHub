export function outboundProtocols(engine) {
  const protocols = [["direct", "直连"], ["vless", "VLESS"], ["vmess", "VMess"], ["trojan", "Trojan"],
    ["shadowsocks", "Shadowsocks / 2022"], ["socks", "SOCKS5"], ["http", "HTTP"]];
  return ["sing-box", "mihomo"].includes(engine) ? [...protocols, ["hysteria2", "Hysteria 2"], ["tuic", "TUIC"], ["anytls", "AnyTLS"]] : protocols;
}

// Deliberately generate only supported, independent exits. Advanced JSON is
// preserved separately, never reverse-converted through a lossy preset form.
export function outboundPreset(engine, protocol, values, validate = true) {
  if (!["xray", "sing-box", "mihomo"].includes(engine) || !outboundProtocols(engine).some(([key]) => key === protocol))
    throw Error("当前内核不支持此出站预设");
  const invalid = (message, key) => Object.assign(new Error(message), {field:`outbound_${key}`});
  const tag = values.tag?.trim(), server = values.server?.trim().replace(/^\[|\]$/g, ""), port = Number(values.port);
  if (validate && (!tag || /^qch-(trf-|stat-)/.test(tag))) throw invalid("请填写有效的非系统出站标签", "tag");
  if (protocol === "direct") return engine === "mihomo" ? {name:tag,type:"direct"} : engine === "xray" ? {tag, protocol:"freedom"} : {tag, type:"direct"};
  if (validate && (!server || /[\s/@?#]/.test(server) || !Number.isInteger(port) || port < 1 || port > 65535))
    throw invalid("请填写出口服务器地址和 1–65535 范围内的端口，不要填写 URL", !server || /[\s/@?#]/.test(server) ? "server" : "port");
  const credential = values.credential || "", password = values.password || "", username = values.username || "";
  const uuid = ["vless", "vmess", "tuic"].includes(protocol), authentication = ["socks", "http"].includes(protocol);
  if (validate && !authentication && !credential) throw invalid(uuid ? "请填写目标入站的 UUID" : "请填写目标入站的密码", "credential");
  if (validate && uuid && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(credential))
    throw invalid("请填写有效的用户 UUID", "credential");
  if (validate && (protocol === "tuic" && !password || authentication && Boolean(username) !== Boolean(password)))
    throw invalid("请完整填写认证用户名和密码；TUIC 还需用户密码", "password");
  const transportable = ["vless", "vmess", "trojan"].includes(protocol);
  const tlsRequired = ["hysteria2", "tuic", "anytls"].includes(protocol);
  const security = tlsRequired ? "tls" : transportable || protocol === "http" ? values.security || "none" : "none";
  const transport = transportable ? values.transport || "tcp" : "tcp";
  if (security === "reality" && protocol !== "vless") throw Error("Reality 预设仅用于 VLESS");
  if (!["none", "tls", "reality"].includes(security) || !["tcp", "ws", "grpc", ...(engine === "xray" ? ["xhttp"] : [])].includes(transport))
    throw Error("当前内核不支持此安全或传输选项");
  if (validate && security === "reality" && (!values.serverName?.trim() || !values.publicKey?.trim()))
    throw invalid("Reality 需要填写 ServerName 和目标入站的公钥，不是服务端私钥", !values.serverName?.trim() ? "serverName" : "publicKey");
  if (validate && security === "reality" && !/^(?:[0-9a-f]{2}){0,8}$/i.test(values.shortId || ""))
    throw invalid("Reality Short ID 必须是最多 16 位的偶数长度十六进制字符", "shortId");
  const sni = values.serverName?.trim() || server;
  const wsHost = transport === "ws" ? values.host?.trim() || (security !== "none" ? sni : "") : "";
  if (validate && wsHost && /[\s/@?#\\]/.test(wsHost)) throw invalid("请填写有效的 WS Host，不要填写 URL 或路径", "host");
  const flow = protocol === "vless" ? values.flow || "" : "";
  if (validate && flow && (flow !== "xtls-rprx-vision" || transport !== "tcp" || security === "none"))
    throw Error("Vision 预设需要 TCP + TLS / Reality；其他组合请使用高级 JSON 并校验");
  const out = {tag};
  if (engine === "mihomo") {
    const out = {name:tag, type:protocol === "shadowsocks" ? "ss" : protocol === "socks" ? "socks5" : protocol, server, port, udp:true};
    if (uuid) out.uuid = credential;
    if (protocol === "vmess") Object.assign(out, {cipher:"auto", alterId:0});
    if (protocol === "shadowsocks") Object.assign(out, {cipher:values.method, password:credential});
    else if (authentication) { if (username) Object.assign(out,{username,password}); }
    else if (!uuid) out.password = credential;
    if (protocol === "tuic") Object.assign(out,{password,"congestion-controller":"bbr"});
    if (flow) out.flow = flow;
    if (security !== "none") {
      out.tls = true;
      out[["vless", "vmess"].includes(protocol) ? "servername" : "sni"] = sni;
    }
    if (["hysteria2","tuic"].includes(protocol)) out.alpn = ["h3"];
    if (security === "reality") Object.assign(out,{"client-fingerprint":"chrome","reality-opts":{"public-key":values.publicKey?.trim(),"short-id":values.shortId || ""}});
    if (transportable) out.network = transport;
    if (transport === "ws") out["ws-opts"] = {path:values.path || "/",...(wsHost ? {headers:{Host:wsHost}} : {})};
    if (transport === "grpc") out["grpc-opts"] = {"grpc-service-name":values.path || ""};
    return out;
  }
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
    if (["hysteria2", "tuic"].includes(protocol)) out.tls.alpn = ["h3"];
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

// A projection never replaces native content. The server applies changes to
// this baseline while retaining fields and accounts absent from the form.
export function readOutboundPreset(engine, out) {
  if (!out || typeof out !== "object") return null;
  let protocol = engine === "xray" ? out.protocol : out.type;
  protocol = ({freedom:"direct",ss:"shadowsocks",socks5:"socks"})[protocol] || protocol;
  if (!outboundProtocols(engine).some(([key]) => key === protocol)) return null;
  const values = {tag:out.tag || out.name,transport:"tcp",security:"none",flow:out.flow || ""};
  if (protocol === "direct") return {protocol,values};
  if (engine === "xray") {
    const peer = out.settings?.vnext?.[0] || out.settings?.servers?.[0];
    if (!peer) return null;
    const user = peer.users?.[0] || {};
    Object.assign(values,{server:peer.address,port:peer.port,credential:user.id || peer.password || "",username:user.user || "",password:user.pass || "",method:peer.method || "",flow:user.flow || ""});
    const stream=out.streamSettings || {}, tls=(stream.security === "reality" ? stream.realitySettings : stream.tlsSettings) || {};
    values.transport=stream.network || "tcp"; if(values.transport==="raw")values.transport="tcp";
    values.security=stream.security || "none";
    Object.assign(values,{serverName:tls.serverName || "",publicKey:tls.publicKey || tls.password || "",shortId:tls.shortId || "",path:stream.wsSettings?.path || stream.grpcSettings?.serviceName || stream.xhttpSettings?.path || "",host:stream.wsSettings?.host || stream.wsSettings?.headers?.Host || ""});
  } else if (engine === "mihomo") {
    Object.assign(values,{server:out.server,port:out.port,credential:out.uuid || out.password || "",username:out.username || "",password:out.password || "",method:out.cipher || "",transport:out.network || "tcp",security:out["reality-opts"] ? "reality" : out.tls || ["trojan","hysteria2","tuic","anytls"].includes(protocol) ? "tls" : "none",serverName:out.servername || out.sni || "",publicKey:out["reality-opts"]?.["public-key"] || "",shortId:out["reality-opts"]?.["short-id"] || "",path:out["ws-opts"]?.path || out["grpc-opts"]?.["grpc-service-name"] || "",host:out["ws-opts"]?.headers?.Host || ""});
  } else {
    Object.assign(values,{server:out.server,port:out.server_port,credential:out.uuid || out.password || "",username:out.username || "",password:out.password || "",method:out.method || "",transport:out.transport?.type || "tcp",security:out.tls?.enabled && out.tls?.reality?.enabled ? "reality" : out.tls?.enabled ? "tls" : "none",serverName:out.tls?.server_name || "",publicKey:out.tls?.reality?.public_key || "",shortId:out.tls?.reality?.short_id || "",path:out.transport?.path || out.transport?.service_name || "",host:out.transport?.headers?.Host || ""});
  }
  if (!["tcp","ws","grpc",...(engine === "xray" ? ["xhttp"] : [])].includes(values.transport)) return null;
  if (!["none","tls","reality"].includes(values.security) || values.security === "reality" && protocol !== "vless") return null;
  if (values.security === "reality" && (!values.serverName || !values.publicKey)) return null;
  if (values.flow && (protocol !== "vless" || values.flow !== "xtls-rprx-vision" || values.transport !== "tcp" || values.security === "none")) return null;
  if (["hysteria2","tuic","anytls"].includes(protocol) && values.security !== "tls") return null;
  return {protocol,values};
}

// Emit only generated objects. Arbitrary native YAML stays in its own editor
// and is patched on the server with the original nodes and scalar precision.
export function outboundPresetText(engine, object) {
  if (engine !== "mihomo") return JSON.stringify(object,null,2);
  const emit = (value, depth=0) => Object.entries(value).map(([key,item]) => {
    const prefix="  ".repeat(depth)+JSON.stringify(key)+":";
    return item && typeof item === "object" && !Array.isArray(item) ? prefix+"\n"+emit(item,depth+1) : prefix+" "+JSON.stringify(item);
  }).join("\n");
  return emit(object)+"\n";
}
