// Old Agents combine source discovery and target safety; require an upgrade
// rather than guessing scope from localized text.
export function managedCoreBlockReason(agent, engine) {
 const runtime = agent.runtime?.[engine] || {};
 if (runtime.managed_operation_blocked_reason) return runtime.managed_operation_blocked_reason;
 if (!agent.shared_allocation?.enabled && runtime.managed_host_blocked_reason) return runtime.managed_host_blocked_reason;
 if (runtime.existing_config_unsupported_reason && !agent.features?.includes("import-isolation-v1"))
   return `请先升级 Agent，以区分外部导入限制和托管目标安全状态：${runtime.existing_config_unsupported_reason}`;
 return "";
}
