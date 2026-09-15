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
    let emptyDetail = "尚未收到符合当前筛选条件的运行记录。";
    let sourceNoticeTitle = "";
    let sourceNoticeDetail = "";
    if (filters.agent_id && !can("agents.read")) {
      emptyTitle = "无法核验采集状态";
      emptyDetail = "当前账号无权读取节点状态；历史日志仍可按现有权限查看。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (filters.agent_id && !selectedAgent) {
      emptyTitle = "节点状态不可用";
      emptyDetail = "无法读取当前所选节点的运行状态。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (selectedAgent?.status === "offline") {
      emptyTitle = "节点离线";
      emptyDetail = "当前日志采集已暂停；已有内容为保留期内的历史记录。";
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
      emptyDetail = "请升级 Agent 后再查看内核运行日志。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (
      selectedAgent &&
      !(selectedAgent.features || []).includes("core-log-status-v1")
    ) {
      emptyTitle = "日志状态能力不可用";
      emptyDetail = "此 Agent 可以上传日志，但无法报告当前采集来源是否正常。";
      sourceNoticeTitle = emptyTitle;
      sourceNoticeDetail = emptyDetail;
    } else if (filters.engine && selectedRuntime?.installed === false) {
      emptyTitle = "内核尚未安装";
      emptyDetail = "当前节点尚未安装所选内核，因此没有可采集的运行日志。";
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
          "Agent 已拒绝或无法读取该日志来源，请检查节点上的 Agent 诊断日志。";
        sourceNoticeTitle = emptyTitle;
        sourceNoticeDetail = emptyDetail;
      } else if (status === "waiting") {
        emptyTitle = "等待日志来源";
        emptyDetail = "日志文件尚未创建；服务写入后会自动开始采集。";
        sourceNoticeTitle = emptyTitle;
        sourceNoticeDetail = emptyDetail;
      } else if (filters.engine && !status) {
        emptyTitle = "日志状态尚未上报";
        emptyDetail = "Agent 尚未报告当前内核的采集来源状态。";
        sourceNoticeTitle = emptyTitle;
        sourceNoticeDetail = emptyDetail;
      } else if (filters.engine && status === "active") {
        emptyDetail = "当前来源工作正常，尚未收到符合筛选条件的新运行记录。";
      }
    }

  return { emptyTitle, emptyDetail, sourceNoticeTitle, sourceNoticeDetail };
}
