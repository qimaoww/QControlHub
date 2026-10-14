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
  if (Object.hasOwn(legacyMessages, message)) return legacyMessages[message];
  if (/\p{Script=Han}/u.test(message)) return message;
  return statusMessages[status] || "操作失败，请稍后重试；若持续失败，请查看任务详情或联系管理员。";
}

// Preserve failure stages and field locations while masking secret material,
// including historical output returned by Agents predating the fix.
export function redactDiagnostic(value) {
  return String(value ?? "")
    .replace(/-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)/g, "[redacted private PEM]")
    .replace(/((?:["']?(?:private[-_]?key|mldsa65[-_]?seed)["']?)\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}\]]+)/gi, "$1[redacted]")
    .replace(/(read certificate\/key \/var\/lib\/qcontrolhub-mihomo(?:-shr_[a-f0-9]+)?\/)[A-Za-z0-9_-]{43}(?=["'\s]|$)/g, "$1[redacted]");
}

export function diagnosticError(value) {
  if (!value) return "";
  const raw = redactDiagnostic(value);
  if (/\p{Script=Han}/u.test(raw)) return raw;
  const explanations = [
    [/stable core installation failed/i, "稳定版内核安装失败，未继续校验或部署配置。请检查安装错误后重试；已有内核不会自动切换版本。"],
    [/preset-auto-install-v1/i, "Agent 版本不支持自动安装，请到节点设置升级 Agent 后重新提交。断线前的执行结果可能需要核对。"],
    [/rollback (?:also )?failed|service recovery failed|binary rollback also failed/i, "变更失败，且回滚或服务恢复失败，请立即检查节点服务状态；确认恢复前不要重复部署。"],
    [/rolled back/i, "变更失败，已自动回滚，请确认服务状态并检查配置后重试。"],
    [/(?:certificate\/key file not found|(?:certificate|key).*no such file or directory)/i, "证书或私钥文件不存在。请按下方配置字段和路径安装文件，或修正引用路径。"],
    [/(?:SAFE_PATHS|not a safe path|ProtectHome|PrivateTmp|certificate\/key file unreadable|(?:read|prepare).*certificate\/key|(?:certificate|private[- ]key).*(?:permission denied|operation not permitted))/i, "证书或私钥文件无法读取。请检查下方字段、路径、父目录穿越权限、qcontrolhub-core 读取权限及服务沙箱允许的路径。"],
    [/reality.*(?:target|SNI|handshake|TLS 1\.3).*(?:failed|failure|error|timeout|refused|x509)|(?:target|handshake).*reality.*(?:failed|error)/i, "Reality 目标握手失败。请从执行内核的节点检查目标 DNS、地址族连通性、TLS 1.3、证书域名和 ALPN，并根据下方具体原因修复。"],
    [/(?:invalid|parse|decode).*reality|reality.*(?:invalid|parse|decode)|invalid ["']?private[-_]?key/i, "Reality 配置解析失败。请检查下方字段的内联私钥格式，并核对客户端 ServerName、公钥和 Short ID。"],
    [/(?:failed to find|failed to parse|malformed|invalid).*PEM|(?:certificate|private key).*(?:format error|malformed|invalid format)|private key does not match|parse.*(?:certificate|private key)|x509: malformed/i, "证书或 TLS 私钥格式错误。请检查 PEM 编码、证书与私钥是否匹配，并按下方字段修正配置。"],
    [/x509:.*(?:unknown authority|expired|not yet valid|valid for|cannot validate)|certificate verify failed|certificate verification failed|unknown certificate authority/i, "证书信任或域名验证失败。请根据下方原因检查信任链、证书域名、有效期及系统时间。"],
    [/rejected the configuration|invalid configuration|configuration validation failed/i, "配置未通过内核校验，请根据下方诊断修正配置后重试。"],
    [/deadline exceeded|timed? ?out|timeout/i, "节点操作超时，请检查节点连接和服务状态，确认执行结果后再重试。"],
    [/connection refused/i, "目标服务拒绝连接，请检查服务是否启动及监听地址、端口是否正确。"],
    [/no such host|name resolution/i, "域名解析失败，请检查节点的 DNS 设置和网络连接。"],
    [/permission denied|operation not permitted/i, "节点执行权限不足，请检查运行账号、文件权限和系统限制。"],
    [/no space left on device/i, "节点磁盘空间不足，请清理空间后重试。"],
    [/no such file or directory|executable file not found/i, "节点缺少所需文件或程序，请检查内核安装状态及配置路径。"],
    [/certificate|x509/i, "TLS 或证书相关操作失败。请按下方失败阶段、字段位置和具体原因排查。"],
  ];
  const message = explanations.find(([pattern]) => pattern.test(raw))?.[1]
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
    if (!isCurrent()) throw new DOMException("Account changed", "AbortError");
  };
  const canceled = (cause) => cause?.name !== "TimeoutError"
    && (cause?.name === "AbortError" || options?.signal?.aborted);
  let response;
  // Validators are opt-in and belong to an exact in-memory representation.
  // Never let a bodyless 304 stand in for a value the caller no longer owns.
  const { conditional, onResponse, ...requestOptions } = options || {};
  const validator = conditional?.etag && Object.hasOwn(conditional, "value") ? conditional : null;
  if (validator) requestOptions.headers = { ...requestOptions.headers, "If-None-Match": validator.etag };
  try {
    response = await fetchImpl(url, requestOptions);
  } catch (cause) {
    assertCurrent();
    // Navigation cancellation must remain silent and retain its identity.
    if (canceled(cause)) throw cause;
    const message = cause?.name === "TimeoutError"
      ? "请求超时，请检查网络；若刚提交了操作，请刷新确认结果，避免重复提交。"
      : "无法连接服务器，请检查网络和面板地址；若刚提交了操作，请刷新确认结果，避免重复提交。";
    throw new Error(message, { cause });
  }
  assertCurrent();
  if (response.status === 304 && validator) {
    onResponse?.(response);
    return validator.value;
  }
  if (!response.ok) {
    // Rendering login aborts the route signal. Do not read the response body
    // afterward: that would replace the authentication failure with AbortError.
    if (response.status === 401) {
      const error = new Error(isLogin ? errorMessage("invalid admin token") : statusMessages[401]);
      error.status = response.status;
      if (!isLogin) onUnauthorized(error.message);
      throw error;
    }
    let body;
    try {
      body = await response.json();
    } catch (cause) {
      assertCurrent();
      if (canceled(cause)) throw cause;
      // A proxy may return HTML instead of JSON.
    }
    assertCurrent();
    const message = errorMessage(body?.error, response.status);
    const error = new Error(message);
    error.status = response.status;
    throw error;
  }
  if (response.status === 204) return null;
  try {
    const body = await response.json();
    assertCurrent();
    onResponse?.(response);
    return body;
  } catch (cause) {
    assertCurrent();
    if (canceled(cause)) throw cause;
    const error = new Error("服务器响应不完整或格式无效，请刷新确认操作结果；若持续失败，请联系管理员检查后端和反向代理。", { cause });
    error.status = response.status;
    throw error;
  }
}
