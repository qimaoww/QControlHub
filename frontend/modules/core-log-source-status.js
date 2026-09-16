// Report collection availability independently of log paging and transport.
export function coreLogSourceStatus(selectedAgent, filters, can) {
    const selectedRuntime = filters.engine
      ? selectedAgent?.runtime?.[filters.engine]
      : null;
    const sourceStates = selectedAgent
      ? Object.values(selectedAgent.runtime || {})
          .map((runtime) => runtime?.core_log_status)
          .filter(Boolean)
      : [];
    let emptyTitle = "暂无日志";
    let emptyDetail = "";
    let sourceNoticeTitle = "";
    let sourceNoticeDetail = "";
    if (filters.agent_id && !can("agents.read")) {
      emptyTitle = "无法核验采集状态";
      emptyDetail = "无权读取节点状态；可按现有权限查看历史日志。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (filters.agent_id && !selectedAgent) {
      emptyTitle = "节点状态不可用";
      emptyDetail = "无法读取所选节点状态；历史日志仍可查看。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (selectedAgent?.status === "offline") {
      emptyTitle = "节点离线";
      emptyDetail = "采集已暂停；仅可查看保留期内的历史记录。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (selectedAgent && selectedAgent.status !== "online") {
      emptyTitle = "节点状态不可用";
      emptyDetail = "无法确认当前节点是否仍在采集日志。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (
      selectedAgent &&
      !(selectedAgent.features || []).includes("core-logs-v1")
    ) {
      emptyTitle = "此节点不支持集中日志";
      emptyDetail = "请升级 Agent 以采集日志。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (
      selectedAgent &&
      !(selectedAgent.features || []).includes("core-log-status-v1")
    ) {
      emptyTitle = "日志状态能力不可用";
      emptyDetail = "可上传日志，但无法报告采集状态；请升级 Agent。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (filters.engine && selectedRuntime?.installed === false) {
      emptyTitle = "内核尚未安装";
      emptyDetail = "安装所选内核后可采集日志。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (
      selectedAgent &&
      (selectedAgent.features || []).includes("core-log-status-v1")
    ) {
      const status =
        filters.engine
          ? selectedRuntime?.core_log_status || ""
          : sourceStates.includes("failed")
            ? "failed"
            : sourceStates.length &&
                sourceStates.every((value) => value === "waiting")
              ? "waiting"
              : "";
      if (status === "failed") {
        emptyTitle = "日志采集失败";
        emptyDetail =
          "Agent 无法读取或已拒绝此来源；请检查节点上的 Agent 诊断日志。";
        sourceNoticeTitle = emptyTitle;
        sourceNoticeDetail = emptyDetail;
      } else if (status === "waiting") {
        emptyTitle = "等待日志来源";
        emptyDetail = "服务创建日志文件后自动采集。";
        sourceNoticeTitle = emptyTitle;
        sourceNoticeDetail = emptyDetail;
      } else if (filters.engine && !status) {
        emptyTitle = "日志状态尚未上报";
        emptyDetail = "等待 Agent 报告采集状态。";
        sourceNoticeTitle = emptyTitle;
        sourceNoticeDetail = emptyDetail;
      } else if (filters.engine && status === "active") {
        emptyDetail = "采集正常，暂无匹配记录。";
      }
    }

  return { emptyTitle, emptyDetail, sourceNoticeTitle, sourceNoticeDetail };
}
