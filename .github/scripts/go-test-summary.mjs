import { createInterface } from "node:readline";
import { mkdirSync, appendFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const name = process.argv[2];
const directory = process.env.QCH_CI_REPORT_DIR;
if (directory) mkdirSync(directory, { recursive: true });
const started = Date.now();
const packages = new Map();
for await (const line of createInterface({ input: process.stdin })) {
  if (directory) appendFileSync(join(directory, `${name}.ndjson`), line + "\n");
  let event;
  try { event = JSON.parse(line); } catch { console.log(`[${name}] ${line}`); continue; }
  if (!event.Package) { if (event.Output) process.stdout.write(event.Output); continue; }
  const pkg = packages.get(event.Package) || { name: event.Package, seconds: 0, output: [] };
  packages.set(event.Package, pkg);
  if (event.Action === "start") pkg.startedAt = event.Time;
  if (event.Action === "output") pkg.output.push(event.Output);
  if (!event.Test && ["pass", "fail", "skip"].includes(event.Action)) {
    pkg.seconds = event.Elapsed || 0;
    pkg.result = event.Action;
    pkg.completedAt = event.Time;
    if (event.Action === "fail") process.stdout.write(pkg.output.join(""));
    console.log(`[${name}] ${event.Action} ${event.Package} ${pkg.seconds.toFixed(3)}s`);
    pkg.output = [];
  }
}
if (directory) {
  const results = [...packages.values()].map(({ output, ...pkg }) => pkg);
  writeFileSync(join(directory, `${name}.json`), JSON.stringify({
    elapsedSeconds: (Date.now() - started) / 1000, packages: results,
  }, null, 2) + "\n");
}
