import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync, mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const started = performance.now();

// One package load, including build constraints and test imports. Never keep a
// package allowlist: a newly added package must participate on the first run.
const format = '{{.ImportPath}}|{{.Dir}}|{{join .Imports ","}}|{{join .TestImports ","}}|{{join .XTestImports ","}}';
const metadata = execFileSync("go", ["list", "-f", format, "./..."], { encoding: "utf8" });
const metadataSeconds = (performance.now() - started) / 1000;
const count = Number(process.argv[2]);
const databaseCount = Number(process.argv[4] || 1);
if (!Number.isInteger(count) || !Number.isInteger(databaseCount) || databaseCount < 1 || databaseCount > count) {
  throw new Error("database cluster count must fit within the test shards");
}
const exclude = process.argv[3] ? new RegExp(process.argv[3]) : null;
const history = JSON.parse(readFileSync(new URL("../ci-package-times.json", import.meta.url), "utf8"));
const platform = process.env.QCH_CI_PLATFORM || "debian";
const packages = metadata.trim().split("\n").filter(Boolean).map(line => {
  const [name, directory, ...imports] = line.split("|");
  const files = readdirSync(directory).filter(file => file.endsWith(".go"));
  const sources = files.map(file => readFileSync(`${directory}/${file}`, "utf8"));
  const tests = sources.reduce((sum, source) => sum + (source.match(/^func (Test|Fuzz|Benchmark)/gm)?.length || 0), 0);
  return { name, imports: imports.flatMap(value => value.split(",")),
    database: name.endsWith("/internal/testdb") || sources.some(source => source.includes("QCH_TEST_DATABASE_URL")),
    seconds: history[platform]?.[name.replace(/^github.com\/qimaoww\/qcontrolhub\/?/, "")] ?? Math.max(1, tests * 0.25) };
});
// Conservatively follow helper imports too. A new integration package may use
// testdb without spelling out the environment variable itself.
const database = new Set(packages.filter(pkg => pkg.database).map(pkg => pkg.name));
let changed;
do {
  changed = false;
  for (const pkg of packages) if (!database.has(pkg.name) && pkg.imports.some(name => database.has(name))) {
    database.add(pkg.name);
    changed = true;
  }
} while (changed);
const selected = packages.filter(pkg => !exclude?.test(pkg.name));
if (!selected.length) throw new Error("no Go packages selected for the test shards");
const shards = Array.from({ length: count }, () => ({ seconds: 0, packages: [] }));
function assign(shard, pkg) { shard.seconds += pkg.seconds; shard.packages.push(pkg.name); }
// A database shard owns a separate PostgreSQL cluster, not just a database.
// Keep packages sequential within it so migration locks and xmin cannot affect
// another package's HOT/VACUUM measurements. One cluster remains the default.
const lightest = candidates => candidates.reduce((best, shard) => shard.seconds < best.seconds ? shard : best);
const databaseShards = shards.slice(0, databaseCount);
const databasePackages = selected.filter(pkg => database.has(pkg.name));
if (databaseCount > 1) databasePackages.sort((a, b) => b.seconds - a.seconds || a.name.localeCompare(b.name));
for (const pkg of databasePackages) assign(lightest(databaseShards), pkg);
for (const pkg of selected.filter(pkg => !database.has(pkg.name))
  .sort((a, b) => b.seconds - a.seconds || a.name.localeCompare(b.name))) {
  assign(lightest(shards), pkg);
}
const assigned = shards.flatMap(shard => shard.packages).sort();
if (new Set(assigned).size !== assigned.length || JSON.stringify(assigned) !== JSON.stringify(selected.map(pkg => pkg.name).sort())) {
  throw new Error("shard assignment did not cover every selected package exactly once");
}
const plannerSeconds = (performance.now() - started) / 1000;
console.error(`package plan: ${selected.length}/${packages.length} packages, metadata=${metadataSeconds.toFixed(3)}s, total=${plannerSeconds.toFixed(3)}s`);
if (process.env.QCH_CI_REPORT_DIR) {
  mkdirSync(process.env.QCH_CI_REPORT_DIR, { recursive: true });
  writeFileSync(join(process.env.QCH_CI_REPORT_DIR, "shard-plan.json"), JSON.stringify({
    metadataSeconds, plannerSeconds, packageCount: packages.length,
    selectedCount: selected.length, databaseClusterCount: databaseCount,
    excluded: packages.filter(pkg => !selected.includes(pkg)).map(pkg => pkg.name), shards,
  }, null, 2) + "\n");
}
for (const shard of shards) process.stdout.write(`${shard.seconds.toFixed(2)}\t${shard.packages.join(" ")}\n`);
