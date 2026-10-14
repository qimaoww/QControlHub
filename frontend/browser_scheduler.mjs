import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { spawn } from "node:child_process";
import { availableParallelism, totalmem } from "node:os";

// Local complete-suite timing on the same source as the baseline CI run.
// CI artifacts provide replacement weights when timings materially change.
const seconds = JSON.parse(readFileSync(new URL("./browser-scene-times.json", import.meta.url), "utf8"));

export function browserShards(modes, workers) {
  assert.ok(Number.isInteger(workers) && workers >= 1 && workers <= 2, "browser workers must be 1 or 2");
  assert.equal(new Set(modes).size, modes.length, "duplicate browser mode");
  const shards = Array.from({ length: Math.min(workers, modes.length) }, () => ({ load: 0, modes: [] }));
  // Longest-processing-time packing of measured scene runtime. Newly added
  // modes automatically enter the plan with a conservative fallback weight.
  const weight = mode => seconds[mode] || 5;
  for (const mode of [...modes].sort((a, b) => weight(b) - weight(a))) {
    const shard = shards.reduce((best, next) => next.load < best.load ? next : best);
    shard.modes.push(mode);
    shard.load += weight(mode);
  }
  assert.deepEqual(shards.flatMap(shard => shard.modes).sort(), [...modes].sort());
  return shards.map(shard => shard.modes);
}

export async function runBrowserWorkers(script, modes) {
  const requested = Number(process.env.QCH_BROWSER_WORKERS || 2);
  assert.ok(Number.isInteger(requested) && requested >= 1 && requested <= 2, "QCH_BROWSER_WORKERS must be 1 or 2");
  const workers = Math.min(requested, availableParallelism(), totalmem() >= 3 * 1024 ** 3 ? 2 : 1);
  const children = new Set();
  let cancelled = false;
  let escalation;
  const signal = name => {
    cancelled = true;
    for (const child of children) {
      try { process.kill(-child.pid, name); } catch (error) { if (error.code !== "ESRCH") throw error; }
    }
  };
  const cancel = () => {
    signal("SIGTERM");
    escalation = setTimeout(() => signal("SIGKILL"), 6000).unref();
  };
  process.once("SIGINT", cancel);
  process.once("SIGTERM", cancel);
  const started = performance.now();
  process.stdout.write(`browser workers=${workers}, CPUs=${availableParallelism()}, memory=${Math.round(totalmem() / 1024 ** 2)}MiB, modes=${modes.length}\n`);
  try {
    const results = await Promise.all(browserShards(modes, workers).map(async shard => {
      const child = spawn(process.execPath, [script], {
        detached: true, stdio: ["ignore", "inherit", "inherit"],
        env: { ...process.env, QCH_BROWSER_SMOKE_WORKER: "1", QCH_BROWSER_SMOKE_MODES: shard.join(",") },
      });
      children.add(child);
      // Every worker keeps its own server, fixtures, profiles, ports and CDP.
      // Continue the other worker on a test failure to retain diagnostics.
      try {
        return await new Promise((resolve, reject) => {
          child.once("error", reject);
          child.once("exit", (code, signal) => resolve({ code, signal }));
        });
      } finally { children.delete(child); }
    }));
    assert.ok(!cancelled && results.every(result => result.code === 0), `browser workers failed: ${JSON.stringify(results)}`);
  } finally {
    signal("SIGTERM");
    clearTimeout(escalation);
    process.removeListener("SIGINT", cancel);
    process.removeListener("SIGTERM", cancel);
    process.stdout.write(`browser suite elapsed=${((performance.now() - started) / 1000).toFixed(3)}s\n`);
  }
}
