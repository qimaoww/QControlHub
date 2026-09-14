import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, rmSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { readSchemaContract, validateSchemaVersionChange } from "./check-schema-version.mjs";

const scriptPath = fileURLToPath(new URL("./check-schema-version.mjs", import.meta.url));

function storeSource(version, schemaSQL) {
  return `package store\n\nconst currentSchemaVersion = ${version}\n\nconst schemaSQL = \`${schemaSQL}\`\n`;
}

test("reads the migration version and schema body", () => {
  assert.deepEqual(readSchemaContract(storeSource(36, "CREATE TABLE example ();")), {
    version: 36,
    schemaSQL: "CREATE TABLE example ();",
  });
});

test("rejects a schema change without a version increment", () => {
  assert.throws(
    () => validateSchemaVersionChange(
      storeSource(35, "CREATE TABLE example (id text);"),
      storeSource(35, "CREATE TABLE example (id text, name text);"),
    ),
    /schemaSQL changed but currentSchemaVersion is still 35/,
  );
});

test("accepts a schema change with a version increment", () => {
  assert.deepEqual(
    validateSchemaVersionChange(
      storeSource(35, "CREATE TABLE example (id text);"),
      storeSource(36, "CREATE TABLE example (id text, name text);"),
    ),
    { baseVersion: 35, currentVersion: 36, schemaChanged: true },
  );
});

test("accepts a hotfix version increment without another schema edit", () => {
  assert.deepEqual(
    validateSchemaVersionChange(
      storeSource(35, "CREATE TABLE example (id text, name text);"),
      storeSource(36, "CREATE TABLE example (id text, name text);"),
    ),
    { baseVersion: 35, currentVersion: 36, schemaChanged: false },
  );
});

test("rejects a schema version downgrade", () => {
  assert.throws(
    () => validateSchemaVersionChange(storeSource(36, "SELECT 1;"), storeSource(35, "SELECT 1;")),
    /currentSchemaVersion decreased from 36 to 35/,
  );
});

function git(directory, ...args) {
  return execFileSync("git", args, { cwd: directory, encoding: "utf8" });
}

function commit(directory, message) {
  git(directory, "add", ".");
  git(directory, "commit", "-qm", message);
  return git(directory, "rev-parse", "HEAD").trim();
}

function runPolicy(directory, baseRef) {
  return spawnSync(process.execPath, [scriptPath], {
    cwd: directory,
    encoding: "utf8",
    env: { ...process.env, QCH_SCHEMA_BASE_REF: baseRef },
  });
}

test("checks legacy and schema-file bases in a real temporary Git repository", (t) => {
  const directory = mkdtempSync(join(tmpdir(), "qch-schema-policy-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  git(directory, "init", "-q");
  git(directory, "config", "user.email", "tests@example.invalid");
  git(directory, "config", "user.name", "Schema policy tests");
  mkdirSync(join(directory, "internal", "store"), { recursive: true });

  const schemaSQL = "CREATE TABLE relocated ();";
  writeFileSync(join(directory, "internal", "store", "store.go"), storeSource(61, schemaSQL));
  const legacyBase = commit(directory, "legacy schema location");

  writeFileSync(join(directory, "internal", "store", "schema.go"), storeSource(61, schemaSQL));
  unlinkSync(join(directory, "internal", "store", "store.go"));
  let result = runPolicy(directory, legacyBase);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /internal\/store\/store\.go -> internal\/store\/schema\.go/);
  assert.match(result.stdout, /v61 -> v61, schemaSQL changed=false/);

  const schemaBase = commit(directory, "move schema declaration without changing the contract");
  writeFileSync(join(directory, "internal", "store", "schema.go"), storeSource(62, "CREATE TABLE current ();"));
  result = runPolicy(directory, schemaBase);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /internal\/store\/schema\.go -> internal\/store\/schema\.go/);
});

test("reports an invalid base reference instead of falling back to store.go", (t) => {
  const directory = mkdtempSync(join(tmpdir(), "qch-schema-policy-invalid-ref-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  git(directory, "init", "-q");
  git(directory, "config", "user.email", "tests@example.invalid");
  git(directory, "config", "user.name", "Schema policy tests");
  mkdirSync(join(directory, "internal", "store"), { recursive: true });
  writeFileSync(join(directory, "internal", "store", "store.go"), storeSource(60, "CREATE TABLE legacy ();"));
  writeFileSync(join(directory, "internal", "store", "schema.go"), storeSource(61, "CREATE TABLE current ();"));
  commit(directory, "schema fixture");

  const result = runPolicy(directory, "not-a-commit");
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /could not resolve QCH_SCHEMA_BASE_REF "not-a-commit" as a commit/);
  assert.doesNotMatch(result.stderr, /internal\/store\/store\.go/);
});

test("fails explicitly when the base contains neither schema location", (t) => {
  const directory = mkdtempSync(join(tmpdir(), "qch-schema-policy-missing-base-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  git(directory, "init", "-q");
  git(directory, "config", "user.email", "tests@example.invalid");
  git(directory, "config", "user.name", "Schema policy tests");
  writeFileSync(join(directory, "README.md"), "fixture\n");
  const base = commit(directory, "base without schema source");
  mkdirSync(join(directory, "internal", "store"), { recursive: true });
  writeFileSync(join(directory, "internal", "store", "schema.go"), storeSource(61, "CREATE TABLE current ();"));

  const result = runPolicy(directory, base);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /contains neither internal\/store\/schema\.go nor internal\/store\/store\.go/);
});
