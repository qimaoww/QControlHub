import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const titlePattern =
  /^(build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test)(\([a-z0-9][a-z0-9._/-]*\))?!?: \S.+$/u;
const asciiTextPattern = /^[\x09\x0a\x0d\x20-\x7e]*$/u;
const requiredSections = ["Summary", "Validation", "Risk and rollback"];

function sectionContents(body) {
  const sections = new Map();
  let current;
  for (const line of body.split(/\r?\n/u)) {
    const heading = line.match(/^##[ \t]+(.+?)[ \t]*$/u);
    if (heading) {
      current = heading[1];
      if (!sections.has(current)) sections.set(current, []);
      continue;
    }
    if (current) sections.get(current).push(line);
  }
  return sections;
}

function meaningfulContent(lines = []) {
  return lines
    .join("\n")
    .replace(/<!--[\s\S]*?-->/gu, "")
    .replace(/^[ \t]*-[ \t]*\[[ \t]\].*$/gmu, "")
    .trim();
}

export function validatePullRequest({ title = "", body = "" } = {}) {
  const errors = [];
  const normalizedTitle = typeof title === "string" ? title.trim() : "";
  const normalizedBody = typeof body === "string" ? body : "";
  if (!titlePattern.test(normalizedTitle)) {
    errors.push(
      "The PR title must follow Conventional Commit format, for example `feat(agent): add capability` or `fix: handle startup failure`.",
    );
  }
  if ([...normalizedTitle].length > 100) {
    errors.push("The PR title must not exceed 100 characters.");
  }
  if (!asciiTextPattern.test(normalizedTitle)) {
    errors.push("The PR title must be written in English using ASCII characters.");
  }
  if (!asciiTextPattern.test(normalizedBody)) {
    errors.push("The PR description must be written in English using ASCII characters.");
  }

  const sections = sectionContents(normalizedBody);
  for (const section of requiredSections) {
    if (!sections.has(section)) {
      errors.push(`The PR description is missing the \`## ${section}\` section.`);
      continue;
    }
    if (!meaningfulContent(sections.get(section))) {
      errors.push(`The \`## ${section}\` section must contain meaningful content.`);
    }
  }
  return errors;
}

function escapeWorkflowCommand(value) {
  return value.replaceAll("%", "%25").replaceAll("\r", "%0D").replaceAll("\n", "%0A");
}

function main() {
  const eventPath = process.env.GITHUB_EVENT_PATH;
  if (!eventPath) throw new Error("GITHUB_EVENT_PATH is not set");
  const event = JSON.parse(readFileSync(eventPath, "utf8"));
  if (!event.pull_request) throw new Error("event payload does not contain pull_request");

  const errors = validatePullRequest(event.pull_request);
  if (errors.length === 0) {
    process.stdout.write("pull request metadata passed policy checks\n");
    return;
  }
  for (const error of errors) {
    process.stderr.write(`::error title=PR policy::${escapeWorkflowCommand(error)}\n`);
  }
  process.exitCode = 1;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
