# 管理员 API Key

## 实施基准

- 基准提交：`8834b33d2fad0539f935b14976e2892ddbd38a71`
- 范围：管理员 API Key 管理、现有诊断接口的受限访问、逐 Key IP 白名单、凭据脱敏和审计。
- 复用现有管理员身份、工作区、权限和资源可见规则。
- 沿用现有 GitHub CI，不新增工作流、任务、检查项或门禁。

## 交付

- [数据结构、接口、鉴权、权限与 IP 规则](implementation.md)
- [可交给 AI 的接入说明](AI接入.md)
- [可运行的只读排查示例](diagnose.sh)（Python 3 标准库）
- [实际验证结果、界面对比及限制](validation.md)
- [持久清理任务登记](evidence/cleanup.json)

设置页的「API Key」支持名称、有效期、读取授权、IP 白名单、停用和撤销。默认「只读排查」预设复用现有 14 项读取权限；完整 Key 仅创建时显示，数据库只存验证摘要。

本地集成测试使用 `QCH_TEST_DATABASE_URL` 指向专用测试数据库。实际 HTTP 验证脚本为 `scripts/verify-api-keys.go`，仅允许本机名为 `qch_api_keys` 的独立数据库；它会创建测试节点、配置、任务、日志和 Key，请勿指向业务库。
