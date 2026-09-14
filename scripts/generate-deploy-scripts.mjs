#!/usr/bin/env node
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const scriptPath = fileURLToPath(import.meta.url);
export const repositoryRoot = resolve(dirname(scriptPath), "..");

// The order is part of each public, remotely consumed shell script. Keep the
// first fragment responsible for its shebang and keep all fragments byte exact.
export const deployManifest = [
  {
    output: "deploy/quick-start.sh",
    modules: [
      "deploy/modules/quick-start/00-bootstrap-options.sh",
      "deploy/modules/quick-start/10-secrets-state.sh",
      "deploy/modules/quick-start/20-compose.sh",
      "deploy/modules/quick-start/30-service-readiness.sh",
      "deploy/modules/quick-start/40-update-rollback.sh",
      "deploy/modules/quick-start/50-environment.sh",
      "deploy/modules/quick-start/60-menu.sh",
      "deploy/modules/quick-start/70-dispatch.sh",
    ],
  },
  {
    output: "deploy/existing-core-mapping.sh",
    modules: [
      "deploy/modules/core-mapping/00-defaults.sh",
      "deploy/modules/core-mapping/10-openrc-process.sh",
      "deploy/modules/core-mapping/20-file-security.sh",
      "deploy/modules/core-mapping/30-argv-paths.sh",
      "deploy/modules/core-mapping/40-ownership.sh",
      "deploy/modules/core-mapping/50-discovery.sh",
    ],
  },
  {
    output: "deploy/remote/install-agent.sh",
    modules: [
      "deploy/modules/install-agent/00-arguments-platform.sh",
      "deploy/modules/install-agent/10-dependencies.sh",
      "deploy/modules/install-agent/20-uninstall-input.sh",
      "deploy/modules/install-agent/30-download-discovery.sh",
      "deploy/modules/install-agent/40-assets.sh",
      "deploy/modules/install-agent/50-state-env.sh",
      "deploy/modules/install-agent/60-service-lifecycle.sh",
      "deploy/modules/install-agent/70-enrollment.sh",
    ],
  },
];

function repositoryPath(pathname) {
  const resolved = resolve(repositoryRoot, pathname);
  const pathFromRoot = relative(repositoryRoot, resolved);
  if (
    pathname.length === 0 ||
    pathFromRoot === "" ||
    pathFromRoot === ".." ||
    pathFromRoot.startsWith(`..${sep}`)
  ) {
    throw new Error(`deploy manifest path must stay inside the repository: ${pathname}`);
  }
  return resolved;
}

export function renderDeployScript(entry) {
  if (!entry.output || !Array.isArray(entry.modules) || entry.modules.length === 0) {
    throw new Error("each deploy manifest entry needs an output and at least one module");
  }
  return Buffer.concat(entry.modules.map((modulePath) => readFileSync(repositoryPath(modulePath))));
}

function sourceModuleFiles(directory = repositoryPath("deploy/modules")) {
  const modules = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const entryPath = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      modules.push(...sourceModuleFiles(entryPath));
      continue;
    }
    if (!entry.isFile()) {
      throw new Error(`deploy module must be a regular file: ${entryPath}`);
    }
    modules.push(relative(repositoryRoot, entryPath).split(sep).join("/"));
  }
  return modules.sort();
}

export function validateDeployManifest(manifest = deployManifest, moduleFiles = sourceModuleFiles()) {
  const listedModules = new Set();
  const outputs = new Set();
  for (const entry of manifest) {
    if (!entry.output || !Array.isArray(entry.modules) || entry.modules.length === 0) {
      throw new Error("each deploy manifest entry needs an output and at least one module");
    }
    if (outputs.has(entry.output)) {
      throw new Error(`deploy manifest lists output more than once: ${entry.output}`);
    }
    outputs.add(entry.output);
    for (const modulePath of entry.modules) {
      if (!modulePath.startsWith("deploy/modules/")) {
        throw new Error(`deploy manifest module must be under deploy/modules: ${modulePath}`);
      }
      if (listedModules.has(modulePath)) {
        throw new Error(`deploy manifest lists module more than once: ${modulePath}`);
      }
      listedModules.add(modulePath);
    }
  }
  const sourceModules = new Set(moduleFiles);
  const unlisted = moduleFiles.filter((modulePath) => !listedModules.has(modulePath));
  const missing = [...listedModules].filter((modulePath) => !sourceModules.has(modulePath));
  if (unlisted.length > 0 || missing.length > 0) {
    throw new Error(
      `deploy manifest and source modules differ: unlisted=${unlisted.join(",") || "none"}; missing=${missing.join(",") || "none"}`,
    );
  }
}

export function checkDeployScripts() {
  validateDeployManifest();
  const staleOutputs = [];
  for (const entry of deployManifest) {
    const outputPath = repositoryPath(entry.output);
    if (!renderDeployScript(entry).equals(readFileSync(outputPath))) {
      staleOutputs.push(entry.output);
    }
  }
  if (staleOutputs.length > 0) {
    throw new Error(
      `generated deploy scripts differ: ${staleOutputs.join(", ")}; run node scripts/generate-deploy-scripts.mjs --write`,
    );
  }
}

export function writeDeployScripts() {
  validateDeployManifest();
  for (const entry of deployManifest) {
    writeFileSync(repositoryPath(entry.output), renderDeployScript(entry));
  }
}

function main(argumentsList) {
  if (argumentsList.length !== 1 || !["--check", "--write"].includes(argumentsList[0])) {
    throw new Error("usage: node scripts/generate-deploy-scripts.mjs --check|--write");
  }
  if (argumentsList[0] === "--check") {
    checkDeployScripts();
    return;
  }
  writeDeployScripts();
}

if (process.argv[1] && resolve(process.argv[1]) === scriptPath) {
  try {
    main(process.argv.slice(2));
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
