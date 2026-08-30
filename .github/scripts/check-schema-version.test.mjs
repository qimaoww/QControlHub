import assert from "node:assert/strict";
import test from "node:test";

import { readSchemaContract, validateSchemaVersionChange } from "./check-schema-version.mjs";

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
