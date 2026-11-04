// Isolated local evidence server. Both revisions use these exact fixtures.
import {createServer} from "node:http";
import {readFile} from "node:fs/promises";
import {spawnSync} from "node:child_process";
import {resolve, dirname, extname, join} from "node:path";
import {fileURLToPath} from "node:url";
const root = resolve(process.argv[2] || "."), capture = dirname(fileURLToPath(import.meta.url));
let catalog;
const server = createServer(async (request, response) => {
  try {
    const path = new URL(request.url, "http://localhost").pathname;
    response.setHeader("Cache-Control", "no-store");
    if (path === "/favicon.ico") { response.writeHead(204).end(); return; }
    if (path === "/") {
      response.setHeader("Content-Type", "text/html; charset=utf-8");
      response.end('<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css"><div id="app"></div><script type="module" src="/capture-runtime.mjs"></script></html>'); return;
    }
    if (path === "/assets/preset-plans.json") {
      catalog ||= spawnSync("go", ["run", "frontend/testdata/preset_plans.go"], {cwd:root, encoding:"utf8", timeout:30000});
      if (catalog.status !== 0) throw Error("Unable to generate the local fixture catalog");
      response.setHeader("Content-Type", "application/json"); response.end(catalog.stdout); return;
    }
    const file = path === "/capture-runtime.mjs" ? join(capture, "runtime.mjs") : resolve(root, "frontend", path.replace(/^\/assets\//, ""));
    if (file !== join(capture, "runtime.mjs") && !file.startsWith(join(root, "frontend") + "/")) { response.writeHead(404).end(); return; }
    response.setHeader("Content-Type", {".css":"text/css", ".js":"text/javascript", ".mjs":"text/javascript"}[extname(file)] || "text/plain");
    response.end(await readFile(file));
  } catch (error) { response.writeHead(500).end(String(error)); }
});
server.listen(Number(process.argv[3]) || 0, "127.0.0.1", () => process.stdout.write(`http://127.0.0.1:${server.address().port}\n`));
process.on("SIGTERM", () => server.close());
