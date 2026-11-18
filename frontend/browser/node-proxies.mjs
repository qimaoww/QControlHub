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
    caddy: { state: 'missing', managed: false },
  };
  await refresh();
  assert.match(section.textContent, /待切换/);
  assert.ok(section.textContent.includes(agent.managed_proxies.nginx.running));
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
      return Response.json({ id: 'fixture', status: 'succeeded', output: path.endsWith('/caddy/check') ? 'Caddy 已检查' : 'Nginx 已检查' });
    };
    check('Nginx').click();
    check('Nginx').click();
    check('Caddy').click();
    await waitFor(() => section.textContent.includes('Caddy 已检查'), 'Caddy 检查被 Nginx 阻塞');
    assert.ok(check('Nginx').disabled);
    assert.equal(requests.length, 2);
    release();
    await waitFor(() => section.textContent.includes('Nginx 已检查'), 'Nginx 检查无反馈');
    agent.managed_proxies.nginx.outcome = '检查失败';
    agent.status = 'offline';
    await refresh();
    assert.match(section.textContent, /已过期/);
    assert.match(section.textContent, /离线/);
    assert.ok(check('Caddy').disabled);
    assert.ok(section.textContent.includes(agent.managed_proxies.nginx.latest));
  } finally {
    release();
    window.fetch = previous;
    agent.status = 'online';
    await refresh();
  }
}
