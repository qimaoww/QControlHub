import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import {
  checkStyles, renderStyles, repositoryRoot, styleManifest,
  validateStyleManifest, validateStyleSlice,
} from "../../scripts/generate-styles.mjs";

test("the distributed stylesheet is the byte-exact ordered concatenation", () => {
  assert.deepEqual(renderStyles(), readFileSync(resolve(repositoryRoot, "frontend/app.css")));
  assert.doesNotThrow(() => checkStyles());
  const result = spawnSync(process.execPath, ["scripts/generate-styles.mjs", "--check"], {
    cwd: repositoryRoot, encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout, "");
});

test("every style source is listed once, and no listed source can disappear", () => {
  assert.throws(() => validateStyleManifest([...styleManifest, styleManifest[0]]), /duplicate/);
  assert.throws(() => validateStyleManifest(styleManifest, [...styleManifest, "frontend/styles/unlisted.css"]), /unlisted=.*unlisted\.css/);
  assert.throws(() => validateStyleManifest(styleManifest, styleManifest.slice(1)), /missing=frontend\/styles\//);
  for (const invalid of [[], null, ["frontend/styles/../outside.css"], ["/tmp/styles.css"]])
    assert.throws(() => validateStyleManifest(invalid));
});

test("source slices cannot cut a rule, conditional group, string, or comment", () => {
  assert.doesNotThrow(() => validateStyleSlice('@media (width > 1px){a[data-x="}"]{content:"/*";color:red}}/* done */'));
  for (const text of ["a{color:red", "}", "/* open", 'a{content:"open}', "@media (width > 1px){a{color:red}", "a[foo{color:red}"])
    assert.throws(() => validateStyleSlice(text), /style source/);
});

test("changing cascade order or a source byte makes the generated check fail", (t) => {
  const root = mkdtempSync(join(tmpdir(), "qch-style-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(join(root, "frontend/styles"), { recursive: true });
  const manifest = ["frontend/styles/a.css", "frontend/styles/b.css"];
  writeFileSync(join(root, manifest[0]), "a{color:red}\n");
  writeFileSync(join(root, manifest[1]), "a{color:blue}\n");
  writeFileSync(join(root, "frontend/app.css"), "a{color:red}\na{color:blue}\n");
  assert.doesNotThrow(() => checkStyles(root, manifest));
  assert.throws(() => checkStyles(root, [...manifest].reverse()), /differs/);
  writeFileSync(join(root, manifest[0]), "a{color:green}\n");
  assert.throws(() => checkStyles(root, manifest), /differs/);
});
