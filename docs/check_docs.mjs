import { readFileSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const docDir = resolve(root, "docs");
const files = ["README.md", "docs/api.md", "docs/production.md"]
  .map((path) => resolve(root, path))
  .filter(existsSync);

const forbidden = [
  /开发版只使用官方/,
  /版本安装只使用/,
  /只使用官方 prerelease/,
  /development 没有官方 prerelease 时任务会失败/,
];

const required = {
  "docs/api.md": ["core_source", "vernesong/mihomo", "mihomo-development-source-v1"],
  "docs/production.md": ["vernesong/mihomo", "mihomo-development-source-v1", "checksums.txt"],
};

let failures = 0;
const fail = (message) => {
  failures += 1;
  console.error(`docs/check_docs.mjs: ${message}`);
};

const text = (path) => readFileSync(path, "utf8");

for (const path of files) {
  const content = text(path);
  const relative = resolve(root, path) === path ? path.slice(root.length + 1) : path;
  for (const pattern of forbidden) {
    if (pattern.test(content)) {
      fail(`${relative} still contains stale wording matching ${pattern}`);
    }
  }
  const keys = Object.keys(required).find((key) => relative.replace(/^\.\//, "") === key);
  if (keys) {
    for (const term of required[keys]) {
      if (!content.includes(term)) {
        fail(`${relative} must mention "${term}"`);
      }
    }
  }
  for (const match of content.matchAll(/\]\(([^)]+)\)/g)) {
    const target = match[1].trim();
    if (/^(https?:\/\/|mailto:|#)/.test(target)) continue;
    const [pathPart] = target.split("#");
    if (!pathPart) continue;
    const resolved = resolve(dirname(path), pathPart);
    const fromRoot = resolve(root, pathPart);
    if (!existsSync(resolved) && !existsSync(fromRoot)) {
      fail(`${relative} links to a missing path "${target}"`);
    }
  }
}

if (failures > 0) {
  process.exitCode = 1;
}
console.log("docs/check_docs.mjs: docs contract and link check passed");
