#!/usr/bin/env node
// Loopback-only HTTP fixture for repeatable WAN/log pressure measurements.
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

const root = resolve(process.env.QCH_PERF_ROOT || resolve(dirname(fileURLToPath(import.meta.url)), "../frontend"));
const record = (name, call) => `const began = performance.now(); try { return ${call}; } finally { window.__phaseMetrics.push({ phase: '${name}', ms: performance.now() - began }); }`;
function instrument(path, source) {
  if (process.env.QCH_PERF_TIMING !== "1") return source;
  const wrappers = path.endsWith("/core-log-model.js") ? ["filterCoreLogEntries", "coreLogFilterCounts"]
    : path.endsWith("/refresh.js") ? ["captureViewState", "reconcileView"] : [];
  for (const name of wrappers) {
    source = source.replace(`export function ${name}(`, `function measured_${name}(`);
    source += `\nexport function ${name}(...args) { ${record(name, `measured_${name}(...args)`)} }\n`;
  }
  if (path.endsWith("/core-log-view.js")) {
    source = source.replace("const rows =", "const rowsBegan = performance.now(); const rows =")
      .replace("const pagination =", "window.__phaseMetrics.push({phase:'rowHTML',ms:performance.now()-rowsBegan}); const pagination =")
      .replace("return renderCoreLogs;", `return (...args) => { ${record("view", "renderCoreLogs(...args)")} };`);
  }
  if (path.endsWith("/core-log-projection.js")) {
    source = source.replace("return (entries, filters) => {", "return (entries, filters) => { const began = performance.now();")
      .replace("return { entries: filtered, counts };", "window.__phaseMetrics.push({phase:'projection',ms:performance.now()-began}); return { entries: filtered, counts };");
  }
  return source;
}
const fixture = { revision: 0, failure: 0, requests: [] };
const latency = Number(process.env.QCH_PERF_LATENCY_MS || 120);
const longRepeat = Number(process.env.QCH_PERF_LONG_REPEAT ?? 80);
const bytesPerSecond = Number(process.env.QCH_PERF_BYTES_PER_SECOND || 4194304);
const server = createServer(async (request, response) => {
  const url = new URL(request.url, "http://127.0.0.1");
  response.setHeader("Cache-Control", "no-store");
  if (url.pathname === "/perf-control" && request.method === "POST") {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const change = JSON.parse(Buffer.concat(chunks).toString() || "{}");
    if (Number.isInteger(change.revision) && change.revision >= 0) fixture.revision = change.revision;
    if ([0, 403, 503].includes(change.failure)) fixture.failure = change.failure;
    if (change.reset) fixture.requests = [];
    response.writeHead(200, { "Content-Type": "application/json" }).end(JSON.stringify({ revision: fixture.revision }));
    return;
  }
  if (url.pathname === "/perf-stats") {
    response.writeHead(200, { "Content-Type": "application/json" }).end(JSON.stringify({
      latency, longRepeat, bytesPerSecond, gzip: process.env.QCH_PERF_GZIP === "1", requests: fixture.requests,
    }));
    return;
  }
  if (url.pathname === "/perf-core-logs") {
    const limit = Number(url.searchParams.get("limit") || 1000);
    if (!Number.isInteger(limit) || limit < 1 || limit > 2000) { response.writeHead(400).end(); return; }
    const agent = url.searchParams.get("agent_id") || "alpha";
    const began = performance.now();
    const revision = fixture.revision, failure = fixture.failure;
    // Synthetic validator models the production HTTP contract, not its SQL path.
    const etag = `"fixture:${agent}:${limit}:${revision}:${longRepeat}"`;
    const metric = { agent, limit, conditional: request.headers["if-none-match"] || null,
      status: 0, encodedBytes: 0, decodedBytes: 0, sentBytes: 0, completed: false };
    fixture.requests.push(metric);
    response.on("finish", () => { metric.completed = true; metric.durationMs = Math.round(performance.now() - began); });
    response.on("close", () => { metric.durationMs = Math.round(performance.now() - began); });
    await new Promise(resolve => setTimeout(resolve, latency));
    if (response.destroyed) return;
    if (failure) {
      metric.status = failure;
      response.writeHead(failure, { "Content-Type": "application/json" }).end('{"error":"temporary fixture failure"}');
      return;
    }
    response.setHeader("ETag", etag);
    if (request.headers["if-none-match"] === etag) {
      metric.status = 304;
      response.writeHead(304).end();
      return;
    }
    let body = Buffer.from(JSON.stringify(["mihomo", "xray", "sing-box", "ss-rust"].flatMap((engine, engineIndex) =>
      Array.from({ length: limit }, (_, offset) => {
        const index = offset + revision;
        return {
          id: engineIndex * 10000 + index + 1, agent_id: agent, engine, level: "warning",
          message: `${agent} ${engine} pressure entry ${index} ` + "长日志 transport ".repeat(longRepeat),
          logged_at: "2026-09-06T00:00:00Z",
        };
      })).sort((a, b) => b.id - a.id)));
    metric.decodedBytes = body.length;
    if (process.env.QCH_PERF_GZIP === "1") { body = gzipSync(body); response.setHeader("Content-Encoding", "gzip"); }
    metric.encodedBytes = body.length;
    metric.status = 200;
    response.writeHead(200, { "Content-Type": "application/json" });
    let offset = 0;
    const chunkBytes = Math.max(1, Math.floor(bytesPerSecond / 32));
    const timer = setInterval(() => {
      if (response.destroyed) { clearInterval(timer); return; }
      const end = Math.min(offset + chunkBytes, body.length);
      response.write(body.subarray(offset, end)); offset = end; metric.sentBytes = offset;
      if (offset === body.length) { clearInterval(timer); response.end(); }
    }, 31.25);
    return;
  }
  if (url.pathname === "/") {
    response.setHeader("Content-Type", "text/html; charset=utf-8");
    response.end('<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css"></head><body><div id="app"></div><script type="module" src="/assets/agents_browser_runtime.mjs"></script></body></html>');
    return;
  }
  if (url.pathname === "/assets/agents_browser_runtime.mjs") {
    response.setHeader("Content-Type", "text/javascript");
    response.end(`import { installAgentFixture } from './browser/agent-fixture.mjs';
const nativeFetch = window.fetch;
const { testAPI } = installAgentFixture('logs');
window.testAPI = testAPI;
const fixtureFetch = window.fetch;
window.__bodyReads = [];
window.__httpResponses = [];
window.fetch = async (input, options) => { const url = new URL(input, location.origin);
  if (url.pathname.startsWith('/perf-')) return nativeFetch(input, options);
  if (url.pathname !== '/api/v1/core-logs') return fixtureFetch(input, options);
  const response = await nativeFetch('/perf-core-logs' + url.search, options);
  window.__httpResponses.push({ status: response.status, limit: url.searchParams.get('limit') });
  const json = response.json.bind(response);
  response.json = async () => {
    const began = performance.now();
    try { return await json(); }
    finally { window.__bodyReads.push({ limit: url.searchParams.get('limit'), status: response.status, bodyAndJSONMs: performance.now() - began }); }
  };
  return response;
};
window.__longTasks = [];
window.__phaseMetrics = [];
new PerformanceObserver(list => window.__longTasks.push(...list.getEntries().map(entry => ({ at: entry.startTime, ms: entry.duration })))).observe({ type: 'longtask', buffered: true });
await import('./app.js');`);
    return;
  }
  const path = resolve(root, url.pathname.replace(/^\/assets\//, ""));
  if (!url.pathname.startsWith("/assets/") || !path.startsWith(root + "/")) { response.writeHead(404).end(); return; }
  try {
    response.setHeader("Content-Type", path.endsWith(".css") ? "text/css" : "text/javascript");
    response.end(instrument(path, await readFile(path, "utf8")));
  } catch { response.writeHead(404).end(); }
});
server.listen(Number(process.env.QCH_PERF_PORT || 0), "127.0.0.1", () =>
  console.log(`http://127.0.0.1:${server.address().port}/?mode=logs#core-logs`));
