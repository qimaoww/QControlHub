import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const storePath = "internal/store/schema.go";
const legacyStorePath = "internal/store/store.go";

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

function git(args) {
  return execFileSync("git", ["-c", `safe.directory=${process.cwd()}`, ...args], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
}

function basePathExists(baseRef, path) {
  // Resolve the commit before this lookup. `ls-tree` returns success for a
  // missing path, while an invalid revision remains a distinct, actionable
  // failure instead of being mistaken for the pre-schema layout.
  const entries = git(["ls-tree", "-z", "--name-only", baseRef, "--", path]).split("\0");
  return entries.includes(path);
}

export function readBaseSchemaContractSource(baseRef) {
  try {
    git(["rev-parse", "--verify", `${baseRef}^{commit}`]);
  } catch (error) {
    throw new Error(`could not resolve QCH_SCHEMA_BASE_REF ${JSON.stringify(baseRef)} as a commit: ${error.message}`);
  }
  const basePath = basePathExists(baseRef, storePath) ? storePath : legacyStorePath;
  if (basePath === legacyStorePath && !basePathExists(baseRef, legacyStorePath)) {
    throw new Error(`base commit ${baseRef} contains neither ${storePath} nor ${legacyStorePath}`);
  }
  try {
    return { basePath, source: git(["show", `${baseRef}:${basePath}`]) };
  } catch (error) {
    throw new Error(`could not read ${basePath} from base commit ${baseRef}: ${error.message}`);
  }
}

export function checkSchemaVersion(baseRef) {
  const { basePath, source: baseSource } = readBaseSchemaContractSource(baseRef);
  const currentSource = readFileSync(storePath, "utf8");
  const result = validateSchemaVersionChange(baseSource, currentSource);
  return { basePath, ...result };
}

function main() {
  const baseRef = process.env.QCH_SCHEMA_BASE_REF?.trim();
  if (!baseRef || /^0+$/.test(baseRef)) {
    throw new Error("QCH_SCHEMA_BASE_REF must name the pull request base or previous push commit");
  }
  const result = checkSchemaVersion(baseRef);
  process.stdout.write(
    `schema contract valid (${result.basePath} -> ${storePath}): v${result.baseVersion} -> v${result.currentVersion}, schemaSQL changed=${result.schemaChanged}\n`,
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
