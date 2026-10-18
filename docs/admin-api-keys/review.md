# 合并前审查

审查对象：`6c21eb6b`。同步 `main` 的 `64336089`（多用户权限与资源归属修复），自动合并后核对 `validateSession`，保留浏览器读取请求的账号/CSRF 绑定检查。

## 已修正的问题

1. **原生协议凭据遗漏**：真实配置生成矩阵在修复前复现 13 个泄漏场景，涉及 Snell `psk`、Sudoku `key`、Mihomo Hy2/AnyTLS/Mieru 用户密码字典、TUIC UUID 字典键及密码、Mihomo/Xray VLESS `decryption` 私钥。补齐这些形态，以及 WireGuard `pre_shared_key` 和 Cookie 字段；YAML 敏感节点去除锚点。保留配置字段标识和 `authentication-timeout` 等非凭据参数。
2. **原始响应校验器**：既有内核日志处理器在脱敏前计算 ETag。API Key 响应不发送该 ETag，也忽略针对原始表示的 `If-None-Match`，避免未脱敏内容影响条件响应；新增实际 Handler 回归覆盖。
3. **编辑到期精度**：编辑窗口展示到分钟，保存未改动的到期时间时保留服务器原始秒数与精度，避免编辑其他字段时意外改变到期时间，尤其是已过期 Key。
4. **PR 元数据检查**：按照既有 PR Policy 将标题、说明改为英文 ASCII，并补齐 `Summary`、`Validation`、`Risk and rollback`；未修改任何 CI 文件或门禁。

权限审查确认：Key 不接受管理员隐式全权限；显式接口清单默认拒绝未审阅接口；身份/工作区继承创建者；创建者停用、降级、删除后永久撤销；IP 白名单和授权每次实时读取；管理操作与审计事务提交；Key 无法用于浏览器登录或管理其他凭据。

## 本轮验证

- [修复前协议矩阵复现](evidence/review-redaction-before.log)：13 个子场景失败；日志不包含生成的完整凭据。
- [core/authn/serverconfig 全包测试](evidence/review-core-protocols.log)：通过，含真实配置生成协议矩阵；端点与端口转发协议未纳入该矩阵。
- [认证、会话、身份、角色和权限回归](evidence/review-auth.log)：通过，50.365 秒。
- [最终 API Key 数据库集成回归](evidence/review-api-key-final.log)：通过，3.265 秒，包含条件日志响应的新断言。
- [浏览器场景](evidence/review-browser.log)：`config-scope`、`capabilities-settings`、`capabilities-settings-readonly` 均通过；原先基准存在的 `config-scope` 失败随同步的主分支修复消除。
- [前端模块](evidence/review-modules.log)、[设置/布局/样式契约](evidence/review-frontend-contract.log)、`go vet`、样式生成检查及 `git diff --check`：通过。

前一轮实际 HTTP、控制面重启、示例及截图仍见 [原验证报告](validation.md)。本轮没有新增生产 Agent/真实内核运行验证。GitHub 完整 CI 使用现有工作流，在本轮提交后执行，以 PR 最新检查结果为准。

测试数据库已再次停止，测试端口已释放；清理计时按本轮验证结束重新登记，具体时间、时区和范围见 [持久任务](evidence/cleanup.json)。
