import { assert, waitFor, delay } from './assertions.mjs';

// Exercise the shipped shell and transport with isolated HTTP responses.
export async function testSessionErrors(preview = '') {
  const app = document.querySelector('#app');
  let session = null, failure = null, loginFailure = null, holds = [], failures = 0, loginCalls = 0;
  const identity = id => ({ user_id: id, username: id, role: 'user', csrf_token: `fixture-${id}`, permissions: ['tasks.read', 'settings.read'] });
  const expired = { error: '登录已过期，请重新登录。', detail: 'session expired' };
  const reply = (body, status = 200) => Response.json(body, { status });
  window.fetch = async (url, options = {}) => {
    const path = new URL(url, location.href).pathname.replace(/^\/api\/v1/, '');
    if (path === '/auth/login') { loginCalls++; if (loginFailure) return reply(loginFailure.body, loginFailure.status); session = identity('second'); return reply(session); }
    if (path === '/auth/logout') { session = null; return new Response(null, { status: 204 }); }
    if (path === '/auth/session') return session ? reply(session) : reply(expired, 401);
    if (path === '/tasks/failures') { failures++; return reply({ id: 'tsk_unexpected' }); }
    if (path === '/site-branding') return reply({});
    if (failure && ['/tasks', '/settings', '/preferences/ui'].includes(path)) {
      if (failure === 'hold') return new Promise(resolve => holds.push({ resolve, signal: options.signal }));
      return reply(failure.body, failure.status);
    }
    if (path === '/tasks') return reply({ items: [], total: 0 });
    return reply({});
  };
  const { startAnalytics } = await import('../analytics/app.js');
  const start = authenticated => {
    session = authenticated;
    history.replaceState(null, '', `${location.pathname}${location.search}#tasks`);
    startAnalytics({ app, session });
  };
  const login = () => app.querySelector('.a-login form');
  const submit = async () => {
    const form = login();
    form.elements.username.value = 'fixture'; form.elements.token.value = 'fixture-password';
    form.requestSubmit();
    await waitFor(() => !form.matches('[aria-busy=true]'), 'login did not settle');
  };
  if (preview) {
    if (preview === 'expired') { failure = { body: expired, status: 401 }; start(identity('first')); }
    else { start(null); if (preview !== 'initial') { loginFailure = { body: { error: preview === 'forbidden' ? '当前账号没有执行此操作的权限，请联系管理员。' : '用户名、密码或管理员令牌不正确，请检查后重试。', detail: preview === 'forbidden' ? 'token role does not permit this operation' : 'invalid admin token' }, status: preview === 'forbidden' ? 403 : 401 }; await submit(); } }
    await waitFor(login, 'preview login missing');
    return;
  }
  start(null);
  assert.equal(app.querySelector('.a-login').textContent.includes('过期'), false, 'initial visit claims expiration');
  for (const [status, body, expected] of [
    [401, { error: 'invalid admin token' }, '用户名、密码或管理员令牌不正确'],
    [401, { error: '用户名、密码或管理员令牌不正确，请检查后重试。', detail: 'invalid admin token' }, '用户名、密码或管理员令牌不正确'],
    [403, { error: 'missing or invalid CSRF token' }, '页面安全凭据已失效'],
    [403, { error: '当前账号没有执行此操作的权限，请联系管理员。', detail: 'token role does not permit this operation' }, '当前账号没有执行此操作的权限'],
    [429, { error: 'too many authentication failures' }, '登录失败次数过多'],
    [503, { error: '服务暂不可用，请稍后重试。' }, '服务暂不可用'],
  ]) {
    loginFailure = { status, body }; await submit();
    assert.ok(login().textContent.includes(expected), `wrong login feedback for ${status}`);
    assert.ok(!app.textContent.includes('登录已过期') && !app.textContent.includes('未能保存到任务') && !app.textContent.includes('原始错误'), 'login error contains unrelated diagnostics');
    assert.ok(!login().querySelector('button[type=submit]').disabled, 'failed login cannot retry');
  }
  loginFailure = null; await submit();
  await waitFor(() => app.querySelector('.a-main'), 'login recovery did not show shell');
  assert.equal(loginCalls, 7, 'login submits were duplicated');
  assert.ok(!app.textContent.includes('登录失败次数过多'), 'successful login retained an old error');
  app.querySelector('[title="退出登录"]').click();
  await waitFor(login, 'logout did not show login');
  assert.ok(!app.textContent.includes('过期'), 'logout claims expiration');

  for (const body of [expired, { error: 'session expired' }]) {
    failure = 'hold'; holds = []; start(identity('first'));
    await waitFor(() => holds.length >= 2, 'concurrent shell reads were not started');
    const requests = holds.splice(0);
    requests[0].resolve(reply(body, 401));
    await waitFor(login, '401 did not show login');
    assert.ok(requests.every(request => request.signal.aborted), 'expired session left requests running');
    const form = login(); form.elements.username.value = 'draft-user'; form.elements.token.value = 'draft-password';
    requests[1].resolve(reply(body, 401)); await delay(80);
    assert.equal(login(), form, 'concurrent 401 rebuilt the login form');
    assert.equal(form.elements.username.value, 'draft-user', 'concurrent 401 lost input');
    assert.equal(app.querySelector('.a-login-card > .a-notice')?.textContent, '登录已过期，请重新登录。', 'expiration was duplicated');
    assert.equal(document.querySelectorAll('.a-toast').length, 0, '401 also opened a notification');
    location.hash = '#settings'; await delay(80);
    assert.equal(login(), form, 'anonymous navigation rebuilt the form');
    failure = null; await submit();
    await waitFor(() => app.querySelector('.a-main'), 'new account did not recover');
    for (const request of requests.slice(2)) request.resolve(reply(body, 401));
    await delay(100);
    assert.ok(!login() && app.textContent.includes('second') && !app.textContent.includes('登录已过期'), 'late 401 expired the replacement account');
  }
  assert.equal(failures, 0, 'authentication failures created failed tasks');
  failure = { body: { error: '权限不足', detail: '仅节点所有者可以修改。', request_id: 'req_permission_fixture' }, status: 403 };
  start(identity('reader'));
  await waitFor(() => app.textContent.includes('权限不足'), 'permission failure was not visible');
  assert.ok(!login(), '403 expired a valid session');
  assert.ok(app.textContent.includes('仅节点所有者可以修改。') && app.textContent.includes('req_permission_fixture'), 'useful diagnostic information was removed');
  failure = 'hold'; holds = []; start(identity('closing'));
  await waitFor(() => holds.length >= 3, 'page-lifetime reads were not started');
  window.dispatchEvent(new Event('pagehide'));
  for (const request of holds) request.resolve(reply(expired, 401));
  await delay(100);
  assert.ok(!login() && !app.textContent.includes('登录已过期'), 'destroyed page accepted a late 401');
}
