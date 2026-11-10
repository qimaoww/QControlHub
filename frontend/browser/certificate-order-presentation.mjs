import { startAnalytics } from '../analytics/app.js';
import { formatDate } from '../analytics/appearance.js';
import { assert, waitFor } from './assertions.mjs';

// Render the production native page rather than checking retired HTML templates.
export async function testCertificateOrderPresentation() {
  window.dispatchEvent(new Event('pagehide'));
  const originalFetch = window.fetch;
  const session = { role: 'admin', user_id: 'certificate-order-presentation', username: 'certificate-review' };
  const scheduled = '2026-11-02T04:05:06Z';
  const certificate = { id: 'cert-presentation', name: 'TLS <img src=x onerror=alert(1)>', domains: ['example.test'],
    key_type: 'ecdsa256', source: 'acme', method: 'dns-api', version: 1234, owned: true, status: 'issued', saved_at: scheduled };
  const detail = { certificate, orders: [{ id: 'order', status: 'queued', stage: 'retry', error: 'not found', attempt: 2,
    next_attempt: scheduled, created_at: scheduled, challenges: [] }], bindings: [{ version: 5678, saved_at: scheduled }],
    grants: [], versions: [{ version: 1234, saved_at: scheduled }] };
  const reply = value => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
  window.fetch = (input, options) => {
    const url = new URL(input instanceof Request ? input.url : input, location.href);
    if (!url.pathname.startsWith('/api/v1/')) return originalFetch(input, options);
    const path = url.pathname.slice('/api/v1'.length);
    return Promise.resolve(reply(path === '/auth/session' ? session : path === '/certificates' ? { items: [certificate], encryption_ready: true }
      : path === `/certificates/${certificate.id}` ? detail : path === '/settings' ? { time_zone: 'UTC' } : []));
  };
  try {
    document.querySelectorAll('dialog[open]').forEach(box => box.close());
    const app = Object.assign(document.createElement('div'), { id: 'certificate-presentation-test' });
    document.body.replaceChildren(app);
    history.replaceState(null, '', '#certificates');
    startAnalytics({ app, session });
    await waitFor(() => document.querySelector('[data-certificate-detail]'), 'native certificate list did not load');
    assert.equal(document.querySelector('.a-main img'), null, 'certificate name became executable markup');
    document.querySelector('[data-certificate-detail]').click();
    const box = await waitFor(() => document.querySelector('dialog[open][data-certificate-id]'), 'native certificate detail did not open');
    await waitFor(() => !box.querySelector('[aria-busy=true]') && box.textContent.includes(formatDate(scheduled)), 'native certificate detail data did not complete');
    const text = box.textContent;
    assert.ok(!/v1234|1234|5678|第 0 次|版本历史/.test(text), 'internal revisions or invented attempts leaked into native detail');
    assert.ok(text.includes(formatDate(scheduled)), 'certificate record lost its actual saved/scheduled date');
    assert.ok(text.includes('等待自动重试'), 'queued retry was presented as a terminal failure');
    assert.ok(text.includes('暂无法确认具体资源或原因'), 'not found was attributed to an unconfirmed missing resource');
    assert.ok(text.includes('已执行 2 次') && text.includes('后台将自动继续申请'), 'retry attempt and continuation information disappeared');
    assert.equal(box.querySelector('img'), null, 'certificate name became executable markup in detail');
    box.close();
  } finally {
    window.dispatchEvent(new Event('pagehide'));
    window.fetch = originalFetch;
  }
}
