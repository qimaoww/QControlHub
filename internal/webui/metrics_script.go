package webui

const agentMetricsScript = `
(() => {
  const roots = Array.from(document.querySelectorAll('[data-agent-metrics]'));
  if (roots.length === 0) return;
  const text = (root, name, value) => {
    const element = root.querySelector('[data-metric-text="' + name + '"]');
    if (element) element.textContent = value;
  };
  const progress = (root, name, available, value) => {
    const element = root.querySelector('[data-metric-progress="' + name + '"]');
    if (!element) return;
    element.value = available ? value : 0;
    element.dataset.available = available ? '1' : '0';
  };
  const runtimeStatus = (value) => {
    const labels = {active: '运行中', inactive: '已停止', failed: '失败', activating: '启动中', deactivating: '停止中', 'dry-run': '演练模式', unknown: '未知'};
    const classes = {active: 'ok', inactive: 'bad', failed: 'bad', activating: 'warn', deactivating: 'warn', 'dry-run': 'warn', unknown: 'muted'};
    value = value || 'unknown';
    return {label: labels[value] || value, className: classes[value] || 'muted'};
  };
	const serviceActionDisabled = (action, online, serviceStatus) => {
	  if (!online) return true;
	  if (action === 'start') return serviceStatus === 'active' || serviceStatus === 'activating';
	  if (action === 'stop') return serviceStatus === 'inactive' || serviceStatus === 'deactivating';
	  if (action === 'restart') return serviceStatus === 'inactive' || serviceStatus === 'activating' || serviceStatus === 'deactivating';
	  return false;
	};
  const updateChrome = (items) => {
    const online = items.filter((item) => item.status === 'online').length;
    const count = document.querySelector('[data-online-count]');
    if (count) { count.textContent = String(online); count.hidden = online === 0; }
    const live = document.querySelector('[data-context-live]');
    if (live) live.classList.toggle('inactive', online === 0);
    const liveLabel = document.querySelector('[data-context-live-label]');
    if (liveLabel) liveLabel.textContent = online ? online + ' 个在线' : '无在线节点';
    const footerDot = document.querySelector('[data-context-live-dot]');
    if (footerDot) footerDot.classList.toggle('inactive', online === 0);
    const footer = document.querySelector('[data-context-live-footer]');
    if (footer) footer.textContent = online ? 'WSS 通道已连接' : '等待节点连接';
    const sync = document.querySelector('[data-sync-state]');
    if (sync) sync.classList.toggle('inactive', online === 0);
    const syncLabel = document.querySelector('[data-sync-label]');
    if (syncLabel) syncLabel.textContent = online ? online + ' 个节点在线' : '等待节点连接';
    items.forEach((item) => {
      const link = document.querySelector('[data-context-agent="' + item.id + '"]');
      if (!link) return;
      const onlineAgent = item.status === 'online';
      const dot = link.querySelector('[data-context-agent-dot]');
      if (dot) dot.className = 'status-dot ' + (onlineAgent ? 'ok' : 'bad');
      const label = link.querySelector('[data-context-agent-label]');
      if (label) label.textContent = onlineAgent ? '在线' : '离线';
    });
  };
  const update = (item) => {
    const root = roots.find((candidate) => candidate.dataset.agentMetrics === item.id);
    if (!root) return;
	const node = root.closest('[data-agent-node]');
	const online = item.status === 'online';
	if (node) {
	  const statusDot = node.querySelector('[data-agent-status-dot]');
	  if (statusDot) statusDot.className = 'status-dot ' + (online ? 'ok' : 'bad');
	  const statusLabel = node.querySelector('[data-agent-status-label]');
	  if (statusLabel) {
		statusLabel.className = 'status-label ' + (online ? 'ok' : 'bad');
		statusLabel.textContent = online ? 'WSS 在线' : '离线';
	  }
	  const heartbeat = node.querySelector('[data-agent-heartbeat]');
	  if (heartbeat) heartbeat.textContent = item.last_seen_label || '尚未心跳';
	  node.querySelectorAll('[data-agent-version]').forEach((element) => { element.textContent = item.version || '未知'; });
	  node.querySelectorAll('.core-version-form button[type="submit"]').forEach((button) => { button.disabled = !online; });
	  if (!online) node.querySelectorAll('[data-service-action]').forEach((button) => { button.disabled = true; });
	  Object.entries(item.runtime || {}).forEach(([engine, runtime]) => {
		const version = node.querySelector('[data-core-version="' + engine + '"]');
		if (version) version.textContent = runtime.installed ? (runtime.version || '版本未知') : '未检测到二进制';
		const service = node.querySelector('[data-core-service="' + engine + '"]');
		if (service) {
		  const status = runtimeStatus(runtime.service_status);
		  service.textContent = status.label;
		  const state = service.closest('.engine-state');
		  if (state) state.className = 'engine-state ' + status.className;
		  const card = service.closest('.service-card');
		  card?.querySelectorAll('[data-service-action]').forEach((button) => {
			button.disabled = serviceActionDisabled(button.dataset.serviceAction, online, runtime.service_status || 'unknown');
		  });
		}
	  });
	}
    root.dataset.available = item.available ? '1' : '0';
    text(root, 'stamp', item.available ? '采集于 ' + item.collected_ago : '等待 Agent 上报');
    text(root, 'cpu', item.cpu_available ? item.cpu_text : '等待采集');
    progress(root, 'cpu', item.cpu_available, item.cpu_percent);
    text(root, 'memory', item.memory_available ? item.memory_text : '等待采集');
    progress(root, 'memory', item.memory_available, item.memory_percent);
    text(root, 'disk', item.disk_available ? item.disk_text : '等待采集');
    progress(root, 'disk', item.disk_available, item.disk_percent);
    text(root, 'download-rate', item.network_available ? item.download_rate : '等待采集');
    text(root, 'upload-rate', item.network_available ? item.upload_rate : '等待采集');
    text(root, 'download-total', item.network_available ? item.download_total : '—');
    text(root, 'upload-total', item.network_available ? item.upload_total : '—');
  };
  const pollState = (message, failed) => {
	roots.forEach((root) => {
	  root.dataset.pollError = failed ? '1' : '0';
	  const element = root.querySelector('[data-metric-poll]');
	  if (element) element.textContent = message;
	});
  };
  let polling = false;
	let sessionExpired = false;
  const poll = async () => {
	if (document.hidden || polling || sessionExpired) return;
	polling = true;
	pollState('正在刷新…', false);
    try {
      const response = await fetch('/ui/agents/metrics', {credentials: 'same-origin', cache: 'no-store', headers: {'Accept': 'application/json'}});
	  if (response.status === 401) {
		sessionExpired = true;
		pollState('会话已过期，正在返回登录页…', true);
		window.setTimeout(() => window.location.assign('/login?error=' + encodeURIComponent('会话已过期，请重新登录')), 300);
		return;
	  }
	  if (!response.ok) throw new Error('HTTP ' + response.status);
      const payload = await response.json();
	  if (!payload || !Array.isArray(payload.agents)) throw new Error('invalid metrics payload');
	  payload.agents.forEach(update);
	  updateChrome(payload.agents);
	  pollState('刚刚更新', false);
    } catch (_) {
	  pollState('刷新失败，保留上次数据', true);
	} finally {
	  polling = false;
    }
  };
	roots.forEach((root) => root.querySelector('[data-agent-refresh]')?.addEventListener('click', poll));
  window.setTimeout(poll, 2000);
  window.setInterval(poll, 5000);
})();
`
