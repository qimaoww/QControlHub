const statusMessages = {
  400: "提交的参数无效，请检查输入后重试。",
  401: "登录已失效，请重新登录。",
  403: "请求被拒绝，请检查账号权限和面板访问地址。",
  404: "请求的资源不存在或已删除，请刷新页面后重试。",
  405: "服务不支持此操作，请刷新页面；若仍失败，请检查前后端版本是否一致。",
  408: "请求超时，请检查网络后重试。",
  409: "数据已发生变化或存在冲突，请刷新页面后重试。",
  413: "提交的内容过大，请缩小内容后重试。",
  422: "配置校验未通过，请检查配置内容后重试。",
  429: "请求过于频繁，请稍后重试。",
  500: "服务器处理失败，请稍后重试；若持续失败，请联系管理员查看服务日志。",
  502: "无法连接后端服务，请稍后重试，或联系管理员检查反向代理与后端服务。",
  503: "服务暂不可用，请稍后重试。",
  504: "后端响应超时，请稍后刷新确认操作结果，避免重复提交。",
};

// Also tolerate older backends and native browser errors during rolling updates.
const legacyMessages = {
  "cross-site login request rejected": "登录请求被安全校验拒绝，请使用配置的 HTTPS 地址，或联系管理员检查反向代理及 QCH_CORS_ORIGINS 中的协议、域名和端口。",
  "origin is not allowed": "当前访问地址未获允许，请联系管理员检查 QCH_CORS_ORIGINS 配置。",
  "invalid admin token": "用户名、密码或管理员令牌不正确，请检查后重试。",
  "session expired": statusMessages[401],
  "authentication required": "请先登录后再操作。",
  "missing or invalid CSRF token": "页面安全凭据已失效，请刷新页面或重新登录后重试。",
  "too many authentication failures": "登录失败次数过多，请稍后再试。",
  "Failed to fetch": "无法连接服务器，请检查网络和面板地址后重试。",
  "NetworkError when attempting to fetch resource.": "无法连接服务器，请检查网络和面板地址后重试。",
  "Load failed": "无法连接服务器，请检查网络和面板地址后重试。",
};

export function errorMessage(value, status = 0) {
  const message = typeof value === "string" ? redactDiagnostic(value.trim()) : "";
  if (Object.hasOwn(legacyMessages, message)) return `${legacyMessages[message]}\n原始错误：${message}`;
  if (/\p{Script=Han}/u.test(message)) return message;
  const summary = statusMessages[status] || "操作未完成，请查看诊断详情。";
  return message ? `${summary}\n原始错误：${message}` : `${summary}\n来源未返回具体原因。`;
}

export function errorDiagnostic(error, seen = new Set()) {
  if (error == null || seen.has(error)) return "";
  if (typeof error !== "object") return redactDiagnostic(error);
  seen.add(error);
  return redactDiagnostic([error.message || String(error), error.stack?.replace(`${error.name || "Error"}: ${error.message}`, "").trim(),
    error.cause && `原因：${errorDiagnostic(error.cause, seen)}`,
    ...(Array.isArray(error.errors) ? error.errors.map(item => errorDiagnostic(item, seen)) : []),
  ].filter(Boolean).join("\n"));
}

// Preserve failure stages and field locations while masking secret material,
// including historical output returned by Agents predating the fix.
export function redactDiagnostic(value) {
  return redactCredentialContainers(String(value ?? ""))
    .replace(/-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)/g, "[redacted private PEM]")
    .replace(/\bBearer\s+[a-z0-9._~+/=-]+/gi, "Bearer [redacted]")
    .replace(/((?:["']?(?:authorization|proxy-authorization|cookie|set-cookie)["']?)\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\r\n]+)/gim, "$1[redacted]")
    .replace(/((?:["']?(?:pass|password|passwd|passphrase|private[-_]?key|pre[-_]?shared[-_]?key|psk|decryption|secret|token|credential|api[-_]?key|webhook[-_]?url|authorization|cookie|uuid|mldsa65[-_]?seed)["']?)\s*[:=]\s*)(?:\[redacted(?: private PEM)?\]|"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}\]]+)/gi, "$1[redacted]")
    .replace(/\bBearer\s+[a-z0-9._~+/=-]+/gi, "Bearer [redacted]")
    .replace(/qch_key_[A-Za-z0-9_-]{43}/g, "[redacted]")
    .replace(/([a-zA-Z][a-zA-Z0-9+.-]*:\/\/)[^\s/@]+@/g, "$1[redacted]@")
    .replace(/([?&](?:token|key|api_key|secret|password|access_token)=)[^\s&#"']+/gi, "$1[redacted]")
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g, "�")
    .replace(/(read certificate\/key \/var\/lib\/qcontrolhub-mihomo(?:-shr_[a-f0-9]+)?\/)[A-Za-z0-9_-]{43}(?=["'\s]|$)/g, "$1[redacted]");
}

function redactCredentialContainers(value) {
  const matches = [...value.matchAll(/["']?(?:passwords?|private[-_]?keys?|secrets?|tokens?|credentials?|short[-_]?ids?)["']?\s*[:=]\s*([\[{])/gim)];
  for (const match of matches.reverse()) {
    const start = match.index + match[0].length - 1;
    if (value.slice(start).startsWith("[redacted")) continue;
    let depth = 0, quote = false, escaped = false, end = start;
    for (; end < value.length; end++) {
      const character = value[end];
      if (quote) {
        if (escaped) escaped = false;
        else if (character === "\\") escaped = true;
        else if (character === '"') quote = false;
      } else if (character === '"') quote = true;
      else if (character === '[' || character === '{') depth++;
      else if ((character === ']' || character === '}') && --depth === 0) { end++; break; }
    }
    value = value.slice(0,start) + '"[redacted]"' + value.slice(end);
  }
  return value;
}

export function diagnosticError(value) {
  if (!value) return "";
  const raw = redactDiagnostic(value);
  if (/\p{Script=Han}/u.test(raw)) return raw;
  // The Agent lists all relevant fields for locating failures. A coexisting
  // Reality field does not establish that a TLS/protocol error came from it.
  const cause = raw.replace(/configuration fields: [^;\r\n]*;\s*/gi, "");
  const certificateFormatMessage = "证书或 TLS 私钥格式错误。请检查 PEM 编码、证书与私钥是否匹配，并按下方字段修正配置。";
  const explanations = [
    [/lookup api\.github\.com[^\r\n]*(?:timeout|timed out)|GitHub API domain resolution timed out/i, "GitHub API 域名解析超时。请检查节点 DNS；仅 IPv6 节点需使用可达的 IPv6 DNS。下方原始错误会说明是否尝试加速及其结果。"],
    [/GitHub API.*rate limit.*(?:exhausted|exceeded)/i, "GitHub API 请求额度已耗尽，请按下方提示的重置时间重试；加速服务可能共享上游额度。"],
    [/(?:GitHub API metadata|release download) returned HTTP 404/i, "GitHub Release 或下载资源不存在，请检查所选核心、版本及发布来源。"],
    [/downloaded release asset (?:SHA-256|size) does not match/i, "下载资源校验失败，安装已中止。请检查发布资源完整性与下载通道后重试。"],
    [/context canceled/i, "操作被中断；请根据下方记录核对原因与最终结果。"],
    [/GitHub download network failure/i, "GitHub 下载网络故障。请根据下方直连与加速结果检查 DNS 和 HTTPS 出站；若两边解析均失败，先修复节点 DNS。"],
    [/stable core installation failed/i, "稳定版内核安装失败，未继续校验或部署配置。请检查安装错误后重试；已有内核不会自动切换版本。"],
    [/preset-auto-install-v2/i, "Agent 版本不支持自动安装，请到节点设置升级 Agent 后重新提交。断线前的执行结果可能需要核对。"],
    [/rollback (?:also )?failed|service recovery failed|binary rollback also failed/i, "变更失败，且回滚或服务恢复失败，请立即检查节点服务状态；确认恢复前不要重复部署。"],
    [/rolled back/i, "变更失败，已自动回滚，请确认服务状态并检查配置后重试。"],
    // Mihomo tries loading paths after an inline PEM parse failure. A missing
    // fallback path containing PEM (or a masked inline key) is a format error.
    [/parse certificate failed[\s\S]*(?:-----BEGIN [A-Z ]+-----|\[redacted(?: private PEM)?\])|private key does not match/i, certificateFormatMessage],
    [/(?:certificate\/key file not found|(?:certificate|key).*no such file or directory)/i, "证书或私钥文件不存在。请按下方配置字段和路径安装文件，或修正引用路径。"],
    [/(?:SAFE_PATHS|not a safe path|ProtectHome|PrivateTmp|certificate\/key file unreadable|(?:read|prepare).*certificate\/key|(?:certificate|private[- ]key).*(?:permission denied|operation not permitted))/i, "证书或私钥文件无法读取。请检查下方字段、路径、父目录穿越权限、qcontrolhub-core 读取权限及服务沙箱允许的路径。"],
    [/reality.*(?:target|SNI|handshake|TLS 1\.3).*(?:failed|failure|error|timeout|refused|x509)|(?:target|handshake).*reality.*(?:failed|error)/i, "Reality 目标握手失败。请从执行内核的节点检查目标 DNS、地址族连通性、TLS 1.3、证书域名和 ALPN，并根据下方具体原因修复。"],
    [/(?:invalid|parse|decode).*reality|reality.*(?:invalid|parse|decode)/i, "Reality 配置解析失败。请检查下方字段的内联私钥格式，并核对客户端 ServerName、公钥和 Short ID。"],
    [/(?:failed to find|failed to parse|malformed|invalid).*PEM|(?:certificate|private key).*(?:format error|malformed|invalid format)|parse.*(?:certificate|private key)|x509: malformed|(?:tls|x509):.*invalid.*private[-_ ]?key/i, certificateFormatMessage],
    [/(?:invalid|parse|decode).*private[-_ ]?key/i, "私钥配置解析失败。请按下方字段所属协议检查私钥格式；内联密钥应填写在协议对应字段，文件引用应使用实际可读路径。"],
    [/x509:.*(?:unknown authority|expired|not yet valid|valid for|cannot validate)|certificate verify failed|certificate verification failed|unknown certificate authority/i, "证书信任或域名验证失败。请根据下方原因检查信任链、证书域名、有效期及系统时间。"],
    [/rejected the configuration|invalid configuration|configuration validation failed/i, "配置未通过内核校验，请根据下方诊断修正配置后重试。"],
    [/lookup [^\r\n]*?(?:timeout|timed out)|(?:DNS|domain resolution|name resolution)[^\r\n]*?(?:timeout|timed out)/i, "域名解析超时，请检查节点 DNS 服务器是否可达；仅 IPv6 节点需使用可达的 IPv6 DNS。"],
    [/deadline exceeded|timed? ?out|timeout/i, "节点操作超时，请检查节点连接和服务状态，确认执行结果后再重试。"],
    [/connection refused/i, "目标服务拒绝连接，请检查服务是否启动及监听地址、端口是否正确。"],
    [/no such host|name resolution/i, "域名解析失败，请检查节点的 DNS 设置和网络连接。"],
    [/permission denied|operation not permitted/i, "节点执行权限不足，请检查运行账号、文件权限和系统限制。"],
    [/no space left on device/i, "节点磁盘空间不足，请清理空间后重试。"],
    [/no such file or directory|executable file not found/i, "节点缺少所需文件或程序，请检查内核安装状态及配置路径。"],
    [/certificate|x509/i, "TLS 或证书相关操作失败。请按下方失败阶段、字段位置和具体原因排查。"],
  ];
  const message = explanations.find(([pattern]) => pattern.test(cause))?.[1]
    || "节点操作失败，请根据下方原始错误检查节点、配置和服务状态。";
  return message === raw ? raw : `${message}\n原始错误（供排查）：${raw}`;
}

export async function requestJSON(url, options, {
  fetchImpl = globalThis.fetch,
  isLogin = false,
  isCurrent = () => true,
  onUnauthorized = () => {},
} = {}) {
  const assertCurrent = () => {
    if (!isCurrent()) throw Object.assign(new DOMException("Account changed", "AbortError"), {intentionalCancellation:true});
  };
  const canceled = () => options?.signal?.aborted && options.signal.reason?.name !== "TimeoutError";
  let response;
  // Validators are opt-in and belong to an exact in-memory representation.
  // Never let a bodyless 304 stand in for a value the caller no longer owns.
  const { conditional, onResponse, ...requestOptions } = options || {};
  const readOnly = ["GET", "HEAD"].includes((requestOptions.method || "GET").toUpperCase());
  const validator = conditional?.etag && Object.hasOwn(conditional, "value") ? conditional : null;
  if (validator) requestOptions.headers = { ...requestOptions.headers, "If-None-Match": validator.etag };
  try {
    response = await fetchImpl(url, requestOptions);
  } catch (cause) {
    assertCurrent();
    // Navigation cancellation must remain silent and retain its identity.
    if (canceled(cause)) { cause.intentionalCancellation=true; throw cause; }
    const message = readOnly ? cause?.name === "TimeoutError"
      ? "读取超时，请检查网络后重试。"
      : "无法读取数据，请检查网络和面板地址后重试。"
      : cause?.name === "TimeoutError"
      ? "请求超时，请检查网络；若刚提交了操作，请刷新确认结果，避免重复提交。"
      : "无法连接服务器，请检查网络和面板地址；若刚提交了操作，请刷新确认结果，避免重复提交。";
    throw Object.assign(new Error(`${message}\n原始错误：${errorDiagnostic(cause)}`, { cause }), { stage: "transport", outcomeUnknown: !readOnly });
  }
  assertCurrent();
  if (response.status === 304 && validator) {
    onResponse?.(response);
    return validator.value;
  }
  if (!response.ok) {
    let body, raw = "", parseError;
    try {
      if (response.text) {
        raw = await responseDiagnosticText(response);
        try { body = JSON.parse(raw); } catch (cause) { parseError = cause; }
      } else if(response.status!==401) body = await response.json();
    } catch (cause) {
      assertCurrent();
      if (canceled(cause)) { cause.intentionalCancellation=true; throw cause; }
      parseError=cause;
    }
    assertCurrent();
    let message = response.status === 401 && !isLogin ? `${statusMessages[401]}\n${errorMessage(body?.error,response.status)}` : errorMessage(body?.error, response.status);
    const detail = body?.detail || (!body ? raw : "");
    if (detail && !message.includes(detail)) message += `\n诊断详情：\n${redactDiagnostic(detail)}`;
    if (parseError) message += `\n响应格式：${response.headers?.get?.("Content-Type") || "未提供 Content-Type"}\n${errorDiagnostic(parseError)}`;
    if (readOnly && response.status===504) message = message.replace(statusMessages[504],"读取超时，请重试。");
    const requestID = body?.request_id || response.headers?.get?.("X-Request-ID");
    const safeRequestID = typeof requestID === "string" && /^[a-zA-Z0-9_-]{1,128}$/.test(requestID) ? requestID : "";
    const error = new Error(`${message}${safeRequestID ? `（请求 ID：${safeRequestID}）` : ""}`);
    error.status = response.status;
    error.requestID = safeRequestID;
    error.code = body?.code;
    error.stage = body?.stage || "api_request";
    error.failureTaskID = body?.failure_task_id;
    error.failureSaved = body?.failure_saved;
    error.retentionError = body?.retention_error;
    if (body?.code === "task_schema_incompatible" && body?.stage === "task_creation" && response.status === 500) {
      error.code = body.code;
      error.stage = body.stage;
    }
    if (body?.code === "port_conflict") {
      if (["mihomo","xray","sing-box","ss-rust"].includes(body.recovery_engine)) error.recoveryEngine = body.recovery_engine;
      error.code = body.code;
      error.field = ["port", "tls_public_port", "xhttp_backend_port", "xhttp_tls_port"].includes(body.field) ? body.field : "";
    }
    if (response.status === 401 && !isLogin) onUnauthorized(error.message);
    throw error;
  }
  if (response.status === 204) return null;
  let raw;
  try {
    raw=response.text ? await responseDiagnosticText(response) : undefined;
    const body = raw===undefined ? await response.json() : JSON.parse(raw);
    assertCurrent();
    onResponse?.(response);
    return body;
  } catch (cause) {
    assertCurrent();
    if (canceled(cause)) { cause.intentionalCancellation=true; throw cause; }
    const error = new Error(`${readOnly ? "服务器响应不完整或格式无效，请重试。"
      : "服务器响应不完整或格式无效，请刷新确认操作结果；若持续失败，请联系管理员检查后端和反向代理。"}\n原始错误：${errorDiagnostic(cause)}`, { cause });
    error.status = response.status;
    error.stage="response_decoding";
    if(raw)error.message+=`\n响应正文：\n${redactDiagnostic(raw)}`;
    throw error;
  }
}

async function responseDiagnosticText(response) {
  const limit=response.ok ? 64<<20 : 8<<20;
  if(!response.body?.getReader) {
    const value=await response.text();
    return value.length<=limit ? value : value.slice(0,limit)+"\n[响应达到采集上限，此诊断不完整]";
  }
  const reader=response.body.getReader(), decoder=new TextDecoder();
  let size=0, text="";
  try {
    while(true) {
      const {done,value}=await reader.read();
      if(done)return text+decoder.decode();
      const remaining=limit-size;
      text+=decoder.decode(value.subarray(0,remaining),{stream:true});size+=value.byteLength;
      if(size>limit){await reader.cancel();return text+decoder.decode()+"\n[响应达到采集上限，此诊断不完整]";}
    }
  } finally {reader.releaseLock();}
}
