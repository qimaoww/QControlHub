import { createInterface } from "node:readline";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
const name = process.argv[2];
const started = performance.now();
for await (const line of createInterface({ input: process.stdin })) console.log(`[${name}] ${line}`);
if (process.env.QCH_CI_REPORT_DIR) {
  mkdirSync(process.env.QCH_CI_REPORT_DIR, { recursive: true });
  writeFileSync(join(process.env.QCH_CI_REPORT_DIR, `${name}-time.json`),
    JSON.stringify({ elapsedSeconds: (performance.now() - started) / 1000 }) + "\n");
}
