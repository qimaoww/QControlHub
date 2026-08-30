import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const storePath = "internal/store/store.go";

export function readSchemaContract(source) {
  const versionMatch = source.match(/const\s+currentSchemaVersion\s*=\s*(\d+)/);
  const schemaMatch = source.match(/const\s+schemaSQL\s*=\s*`([\s\S]*?)`/);
  if (!versionMatch || !schemaMatch) {
    throw new Error(`could not read currentSchemaVersion and schemaSQL from ${storePath}`);
  }
  return { version: Number(versionMatch[1]), schemaSQL: schemaMatch[1] };
}

export function validateSchemaVersionChange(baseSource, currentSource) {
  const base = readSchemaContract(baseSource);
  const current = readSchemaContract(currentSource);
  if (current.version < base.version) {
    throw new Error(`currentSchemaVersion decreased from ${base.version} to ${current.version}`);
  }
  if (current.schemaSQL !== base.schemaSQL && current.version <= base.version) {
    throw new Error(
      `schemaSQL changed but currentSchemaVersion is still ${current.version}; increment it above ${base.version} so existing databases run the migration`,
    );
  }
  return { baseVersion: base.version, currentVersion: current.version, schemaChanged: current.schemaSQL !== base.schemaSQL };
}

function main() {
  const baseRef = process.env.QCH_SCHEMA_BASE_REF?.trim();
  if (!baseRef || /^0+$/.test(baseRef)) {
    throw new Error("QCH_SCHEMA_BASE_REF must name the pull request base or previous push commit");
  }
  const baseSource = execFileSync("git", ["show", `${baseRef}:${storePath}`], { encoding: "utf8" });
  const currentSource = readFileSync(storePath, "utf8");
  const result = validateSchemaVersionChange(baseSource, currentSource);
  process.stdout.write(
    `schema contract valid: v${result.baseVersion} -> v${result.currentVersion}, schemaSQL changed=${result.schemaChanged}\n`,
  );
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  try {
    main();
  } catch (error) {
    process.stderr.write(`schema version policy failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}
