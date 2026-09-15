#!/usr/bin/env node
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const scriptPath = fileURLToPath(import.meta.url);
export const repositoryRoot = resolve(dirname(scriptPath), "..");
export const styleManifest = JSON.parse(
  readFileSync(resolve(repositoryRoot, "frontend/styles/manifest.json"), "utf8"),
);

function sourceFiles(root, directory = resolve(root, "frontend/styles")) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) return sourceFiles(root, path);
    const name = relative(root, path).split(sep).join("/");
    if (!entry.isFile()) throw new Error(`style source must be a regular file: ${name}`);
    if (name === "frontend/styles/manifest.json") return [];
    if (!name.endsWith(".css")) throw new Error(`unexpected style source: ${name}`);
    return [name];
  }).sort();
}

export function validateStyleManifest(manifest = styleManifest, files = sourceFiles(repositoryRoot)) {
  if (!Array.isArray(manifest) || !manifest.length) throw new Error("style manifest must be a nonempty ordered array");
  const listed = new Set();
  for (const path of manifest) {
    if (typeof path !== "string" || !/^frontend\/styles\/[a-z0-9/_-]+\.css$/.test(path))
      throw new Error(`style module must stay under frontend/styles: ${path}`);
    if (listed.has(path)) throw new Error(`duplicate style module: ${path}`);
    listed.add(path);
  }
  const found = new Set(files);
  const missing = manifest.filter(path => !found.has(path));
  const unlisted = files.filter(path => !listed.has(path));
  if (missing.length || unlisted.length)
    throw new Error(`style manifest and sources differ: missing=${missing.join(",") || "none"}; unlisted=${unlisted.join(",") || "none"}`);
}

// Source boundaries may only occur between complete CSS rules. This lexical
// check deliberately does not reformat CSS or change the historic cascade.
export function validateStyleSlice(text, name = "style source") {
  const stack = [];
  let quote = "", comment = false;
  for (let index = 0; index < text.length; index++) {
    const char = text[index], next = text[index + 1];
    if (comment) {
      if (char === "*" && next === "/") { comment = false; index++; }
      continue;
    }
    if (quote) {
      if (char === "\\") index++;
      else if (char === quote) quote = "";
      continue;
    }
    if (char === "/" && next === "*") { comment = true; index++; continue; }
    if (char === '"' || char === "'") { quote = char; continue; }
    if (char === "\\") { index++; continue; }
    if ("{([".includes(char)) stack.push(char);
    else if ("})]".includes(char)) {
      if (stack.pop() !== ({ "}": "{", ")": "(", "]": "[" })[char])
        throw new Error(`unbalanced style source: ${name}`);
    }
  }
  if (stack.length || quote || comment) throw new Error(`incomplete style source: ${name}`);
}

export function renderStyles(root = repositoryRoot, manifest = styleManifest) {
  validateStyleManifest(manifest, sourceFiles(root));
  return Buffer.concat(manifest.map(path => {
    const bytes = readFileSync(resolve(root, path));
    validateStyleSlice(bytes.toString("utf8"), path);
    return bytes;
  }));
}

export function checkStyles(root = repositoryRoot, manifest = styleManifest) {
  if (!renderStyles(root, manifest).equals(readFileSync(resolve(root, "frontend/app.css"))))
    throw new Error("generated frontend/app.css differs; run make generate-styles");
}

export function writeStyles() {
  writeFileSync(resolve(repositoryRoot, "frontend/app.css"), renderStyles());
}

if (process.argv[1] && resolve(process.argv[1]) === scriptPath) {
  try {
    if (process.argv.length !== 3 || !["--check", "--write"].includes(process.argv[2]))
      throw new Error("usage: node scripts/generate-styles.mjs --check|--write");
    if (process.argv[2] === "--check") checkStyles();
    else writeStyles();
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
