import { assert, waitFor } from './assertions.mjs';

// Exercise the real node settings dialog with independently timed upstream
// responses. The fixture only handles the two check endpoints during this test.
export async function testProxyReports(dialog, agent, refresh) {
  const section = dialog.querySelector('.a-node-proxies');
  const check = name => section.querySelector(`[aria-label="检查 ${name} 更新"]`);
  assert.match(section.textContent, /需升级 QAgent/);
  assert.ok(check('Nginx').disabled);
  agent.features = [...agent.features, 'managed-proxies-v1'];
  agent.managed_proxies = {
    nginx: { managed: true, state: 'pending', installed: '1.31.9-1~bookworm', running: '1.22.1-9+deb12u10',
      latest: '1.31.9-1~bookworm', checked_at: '2026-11-18T08:00:00Z', listeners: 2, shared_instances: 1 },
    caddy: { state: 'active', managed: true, installed: 'v2.11.7', running: 'v2.11.7', latest: 'v2.11.9', update_state: 'blocked', update_error: '当前程序与同版本官方 Linux 发布包的摘要不同' },
  };
  await refresh();
  assert.match(section.textContent, /待切换/);
  assert.ok(section.textContent.includes(agent.managed_proxies.nginx.running));
  assert.match(section.textContent, /更新受阻/);
  assert.match(section.textContent, /有新版/);
  const previous = window.fetch;
  const requests = [];
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  try {
    window.fetch = async (url, options) => {
      const path = new URL(url, location.origin).pathname;
      if (!path.includes('/proxies/')) return previous(url, options);
      requests.push(path);
      if (path.endsWith('/nginx/check')) await gate;
      const mode = path.endsWith('/caddy/check') ? 'caddy' : 'nginx';
      window.__agentsBrowserTestAPI.proxyTasks = { ...window.__agentsBrowserTestAPI.proxyTasks, ['check-' + mode]: { status: 'running' } };
      return Response.json({ id: 'fixture', status: 'running' });
    };
    check('Nginx').click();
    check('Nginx').click();
    check('Caddy').click();
    await waitFor(() => section.querySelector('[aria-label=Caddy]').textContent.includes('检查中'), 'Caddy 检查被 Nginx 阻塞');
    assert.ok(check('Nginx').disabled);
    assert.equal(requests.length, 2);
    release();
    await waitFor(() => section.querySelector('[aria-label=Nginx]').textContent.includes('检查中'), 'Nginx 检查无反馈');
    window.__agentsBrowserTestAPI.proxyTasks = { 'check-caddy': {status:'canceled',finished_at:new Date().toISOString()} };
    await waitFor(() => section.textContent.includes('检查任务已取消') && !check('Nginx').disabled, '终态未解锁重试');
    assert.match(section.textContent, /更新受阻/);
    agent.managed_proxies.nginx.outcome = '检查失败';
    agent.status = 'offline';
    await refresh();
    assert.match(section.textContent, /已过期/);
    assert.match(section.textContent, /离线/);
    assert.ok(check('Caddy').disabled);
    assert.ok(section.textContent.includes(agent.managed_proxies.nginx.latest));
  } finally {
    release();
    delete window.__agentsBrowserTestAPI.proxyTasks;
    window.fetch = previous;
    agent.status = 'online';
    await refresh();
  }
}

export async function testReopenedProxyTasks(agent, refresh) {
  const fixture = window.__agentsBrowserTestAPI;
  agent.can_manage = true;
  agent.status = 'online';
  agent.features = [...agent.features, 'managed-proxies-v1'];
  await refresh();
  const open = async () => {
    const control = [...document.querySelectorAll('article[data-agent-id=alpha] button')].find(button => button.textContent === '查看设置');
    control.click();
    return waitFor(() => document.querySelector('dialog[open] .a-node-proxies'), '节点设置未重新打开');
  };
  const previous = window.fetch;
  let waiting = false, release;
  const gate = new Promise(resolve => { release = resolve; });
  try {
    window.fetch = async (url, options) => {
      if (String(url).endsWith('/alpha/proxies') && !waiting) {
        waiting = true;
        await gate;
        return Response.json({agent_id: 'alpha', tasks: {'check-nginx': {status:'running'}}});
      }
      return previous(url, options);
    };
    const old = await open();
    await waitFor(() => waiting, '未发起状态读取');
    old.closest('dialog').close();
    fixture.proxyTasks = {'update-caddy': {status: 'pending'}};
    const fresh = await open();
    await waitFor(() => fresh.textContent.includes('更新排队中'), '未恢复排队任务');
    assert.ok(fresh.querySelector('[aria-label="更新 Caddy"]').disabled);
    release();
    fixture.proxyTasks = {'update-caddy': {status: 'failed',created_at:new Date().toISOString(),finished_at:new Date().toISOString()}};
    await waitFor(() => fresh.textContent.includes('更新任务失败'), '无节点回报的失败任务未解锁');
    assert.ok(!fresh.textContent.includes('检查中') && !fresh.querySelector('[aria-label="更新 Caddy"]').disabled);
    fresh.closest('dialog').close();
  } finally {
    release();
    delete fixture.proxyTasks;
    window.fetch = previous;
  }
}
