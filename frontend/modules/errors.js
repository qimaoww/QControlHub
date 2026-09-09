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
  const message = typeof value === "string" ? value.trim() : "";
  if (Object.hasOwn(legacyMessages, message)) return legacyMessages[message];
  if (/\p{Script=Han}/u.test(message)) return message;
  return statusMessages[status] || "操作失败，请稍后重试；若持续失败，请查看任务详情或联系管理员。";
}

// Diagnostic output stays available verbatim, with a Chinese explanation above
// it. Do not rewrite task.error itself: retry diagnostics inspect the raw value.
export function diagnosticError(value) {
  if (!value) return "";
  const raw = String(value);
  if (/\p{Script=Han}/u.test(raw)) return raw;
  const explanations = [
    [/rolled back/i, "变更失败，已自动回滚，请确认服务状态并检查配置后重试。"],
    [/rejected the configuration|invalid configuration|configuration validation failed/i, "配置未通过内核校验，请根据下方诊断修正配置后重试。"],
    [/deadline exceeded|timed? ?out|timeout/i, "节点操作超时，请检查节点连接和服务状态，确认执行结果后再重试。"],
    [/connection refused/i, "目标服务拒绝连接，请检查服务是否启动及监听地址、端口是否正确。"],
    [/no such host|name resolution/i, "域名解析失败，请检查节点的 DNS 设置和网络连接。"],
    [/permission denied|operation not permitted/i, "节点执行权限不足，请检查运行账号、文件权限和系统限制。"],
    [/no space left on device/i, "节点磁盘空间不足，请清理空间后重试。"],
    [/no such file or directory|executable file not found/i, "节点缺少所需文件或程序，请检查内核安装状态及配置路径。"],
    [/certificate|x509/i, "证书校验失败，请检查证书有效期、域名、系统时间和信任链。"],
  ];
  const message = explanations.find(([pattern]) => pattern.test(raw))?.[1]
    || "节点操作失败，请根据下方原始错误检查节点、配置和服务状态。";
  return message === raw ? raw : `${message}\n原始错误（供排查）：${raw}`;
}

export async function requestJSON(url, options, {
  fetchImpl = globalThis.fetch,
  isLogin = false,
  onUnauthorized = () => {},
} = {}) {
  const canceled = (cause) => cause?.name !== "TimeoutError"
    && (cause?.name === "AbortError" || options?.signal?.aborted);
  let response;
  try {
    response = await fetchImpl(url, options);
  } catch (cause) {
    // Navigation cancellation must remain silent and retain its identity.
    if (canceled(cause)) throw cause;
    const message = cause?.name === "TimeoutError"
      ? "请求超时，请检查网络；若刚提交了操作，请刷新确认结果，避免重复提交。"
      : "无法连接服务器，请检查网络和面板地址；若刚提交了操作，请刷新确认结果，避免重复提交。";
    throw new Error(message, { cause });
  }
  if (!response.ok) {
    if (response.status === 401 && !isLogin) onUnauthorized();
    let body;
    try {
      body = await response.json();
    } catch (cause) {
      if (canceled(cause)) throw cause;
      // A proxy may return HTML instead of JSON.
    }
    const message = response.status === 401
      ? isLogin ? errorMessage("invalid admin token") : statusMessages[401]
      : errorMessage(body?.error, response.status);
    const error = new Error(message);
    error.status = response.status;
    throw error;
  }
  if (response.status === 204) return null;
  try {
    return await response.json();
  } catch (cause) {
    if (canceled(cause)) throw cause;
    const error = new Error("服务器响应不完整或格式无效，请刷新确认操作结果；若持续失败，请联系管理员检查后端和反向代理。", { cause });
    error.status = response.status;
    throw error;
  }
}
