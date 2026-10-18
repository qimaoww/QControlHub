// Local evidence server: production assets plus the existing browser fixtures.
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { resolve, join, extname } from 'node:path';
const root = resolve(process.argv[2]);
const capture = resolve('docs/ui-review-20261018/capture');
const server = createServer(async (req, res) => {
  try {
    const path = new URL(req.url, 'http://localhost').pathname;
    res.setHeader('Cache-Control', 'no-store');
    if (path === '/favicon.ico') {
      res.writeHead(204);res.end();
    } else if (path === '/') {
      res.setHeader('Content-Type', 'text/html; charset=utf-8');
      res.end('<!doctype html><html lang="zh-CN" data-review-version="' + (/\/round\d+\/before/.test(root) ? 'before' : 'after') + '"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css"><div id="app"></div><script type="module" src="/review-runtime.mjs"></script></html>');
    } else if (path === '/substore-fixture.mjs') {
      const code = await readFile(join(root, 'frontend/config_scope_browser_runtime.mjs'), 'utf8');
      res.setHeader('Content-Type', 'text/javascript');
      res.end(code.replaceAll('"./modules/', '"/assets/modules/').replaceAll('"./browser/', '"/assets/browser/') + '\nexport { subStoreFixture };');
    } else if (path === '/preset-fixture.mjs') {
      const code = await readFile(join(root, 'frontend/presets_browser_runtime.mjs'), 'utf8');
      res.setHeader('Content-Type', 'text/javascript');
      res.end(code.slice(0, code.indexOf('async function testEditedRetryDialog')).replaceAll('"./modules/', '"/assets/modules/') + '\nexport { fixture };');
    } else {
      const file = path === '/review-runtime.mjs' ? join(capture, 'runtime.mjs') : path === '/assets/preset-plans.json' ? resolve('output/playwright/ui-review-20261018/preset-plans.json') : resolve(root, 'frontend', path.replace(/^\/assets\//, ''));
      if (!file.startsWith(root + '/') && !file.startsWith(capture + '/') && path !== '/assets/preset-plans.json') throw Error('outside evidence roots');
      res.setHeader('Content-Type', {'.css':'text/css', '.js':'text/javascript', '.mjs':'text/javascript', '.json':'application/json'}[extname(file)] || 'text/plain');
      res.end(await readFile(file));
    }
  } catch (error) { res.writeHead(500); res.end(String(error)); }
});
server.listen(Number(process.argv[3]) || 0, '127.0.0.1', () => console.log(`http://127.0.0.1:${server.address().port}`));
process.on('SIGTERM', () => server.close());
