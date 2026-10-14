import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, watch, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const runnerPath = fileURLToPath(new URL("./run-ci-tests.sh", import.meta.url));
const parallelTasks = ["go-1", "go-2", "extra", "checks-1", "checks-2"];
// Reverse lexical order also catches implementations that sort the commands.
const afterTasks = ["after-z", "after-a"];

const stubSource = `
import { appendFileSync, existsSync, readFileSync, watch } from "node:fs";
import { join } from "node:path";

const root = process.env.QCH_RUNNER_FIXTURE;
const config = JSON.parse(readFileSync(join(root, "config.json"), "utf8"));
const eventPath = join(root, "events.jsonl");
const [tool, ...args] = process.argv.slice(2);

function record(event) {
  appendFileSync(eventPath, JSON.stringify(event) + "\\n");
}

function waitForGate(name) {
  const gatePath = join(root, "gates", name);
  if (existsSync(gatePath)) return Promise.resolve();
  return new Promise((resolve) => {
    const watcher = watch(join(root, "gates"), check);
    function check() {
      if (!existsSync(gatePath)) return;
      watcher.close();
      resolve();
    }
    check();
  });
}

async function runTask(name) {
  record({ type: "start", task: name, databaseURL: process.env.QCH_TEST_DATABASE_URL });
  const completed = new Set(readFileSync(eventPath, "utf8").trim().split("\\n")
    .filter(Boolean).map((line) => JSON.parse(line))
    .filter((event) => event.type === "finish").map((event) => event.task));
  const missing = (config.dependencies[name] || []).filter((task) => !completed.has(task));
  if (missing.length) {
    record({ type: "premature", task: name, missing });
    console.error(name + " started before " + missing.join(", "));
    process.exitCode = 99;
    return;
  }
  if (config.gated.includes(name)) await waitForGate(name);
  const status = config.failures[name] || 0;
  console.log("completed " + name);
  if (status) console.error("failure in " + name + ": exit " + status);
  record({ type: "finish", task: name, status });
  process.exitCode = status;
}

if (tool === "go") {
  record({ type: "tool", tool, args });
  if (args[0] === "list" && args.at(-1) === "./...") {
    console.log(config.packages.map((entry) => [entry.name, entry.directory, ...(entry.imports || ["", "", ""])].join("|")).join("\\n"));
  } else if (args[0] === "test") {
    const shard = process.env.QCH_TEST_DATABASE_URL.match(/_shard([0-9]+)(?:\\?|$)/);
    if (!shard) throw new Error("missing shard database URL");
    await runTask("go-" + shard[1]);
  } else {
    throw new Error("unexpected go invocation: " + args.join(" "));
  }
} else if (tool === "psql") {
  record({ type: "tool", tool, args });
  // Report that the fixture databases exist; never contact a PostgreSQL server.
  if (!args.includes("-tAc")) throw new Error("unexpected psql invocation");
  console.log("1");
} else if (tool === "task") {
  await runTask(args[0]);
} else {
  throw new Error("unexpected fixture tool: " + tool);
}
`;

function shellQuote(value) {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

function createFixture(t, { failures = {}, gated = [] } = {}) {
  const root = mkdtempSync(join(tmpdir(), "qch-ci-runner-"));
  const binPath = join(root, "bin");
  const eventPath = join(root, "events.jsonl");
  const stubPath = join(root, "stub.mjs");
  mkdirSync(binPath);
  mkdirSync(join(root, "gates"));
  writeFileSync(eventPath, "");
  writeFileSync(stubPath, stubSource);

  const packages = ["database", "unit"].map((name) => {
    const directory = join(root, name);
    mkdirSync(directory);
    writeFileSync(join(directory, "fixture_test.go"),
      'package fixture\nimport "testing"\nfunc TestFixture(t *testing.T) {}\n');
    if (name === "database") {
      writeFileSync(join(directory, "fixture.go"),
        'package fixture\nconst databaseEnv = "QCH_TEST_DATABASE_URL"\n');
    }
    return { name: `example.invalid/${name}`, directory };
  });
  const dependencies = Object.fromEntries(afterTasks.map((task, index) =>
    [task, [...parallelTasks, ...afterTasks.slice(0, index)]]));
  writeFileSync(join(root, "config.json"), JSON.stringify({ failures, gated, packages, dependencies }));
  for (const tool of ["go", "psql"]) {
    writeFileSync(join(binPath, tool),
      `#!/usr/bin/env bash\nexec ${shellQuote(process.execPath)} ${shellQuote(stubPath)} ${tool} "$@"\n`,
      { mode: 0o755 });
  }

  let execution;
  t.after(async () => {
    if (execution && execution.child.exitCode === null && execution.child.signalCode === null) {
      try {
        // Stop the runner and its gated descendants when an assertion fails.
        process.kill(-execution.child.pid, "SIGKILL");
      } catch (error) {
        if (error.code !== "ESRCH") throw error;
      }
    }
    if (execution) await execution.result.catch(() => {});
    rmSync(root, { recursive: true, force: true });
  });

  const taskCommand = (name) =>
    `${shellQuote(process.execPath)} ${shellQuote(stubPath)} task ${shellQuote(name)}`;
  const events = () => readFileSync(eventPath, "utf8").split("\n")
    .filter(Boolean).map((line) => JSON.parse(line));

  function start({ dryRun = false, extraIndependent = true } = {}) {
    assert.equal(execution, undefined, "one runner per fixture");
    // Interleave options to ensure after-checks are deferred regardless of
    // where their options appear, while their own CLI order stays intact.
    const args = [runnerPath,
      "--checks", taskCommand("checks-1"),
      "--after-checks", taskCommand(afterTasks[0]),
      "--extra", taskCommand("extra"),
      "--checks", taskCommand("checks-2"),
      "--after-checks", taskCommand(afterTasks[1]),
    ];
    if (dryRun) args.push("--dry-run");
    if (extraIndependent) args.push("--extra-independent");
    const child = spawn("bash", args, {
      cwd: root,
      detached: true,
      env: {
        ...process.env,
        PATH: `${binPath}:${process.env.PATH}`,
        QCH_RUNNER_FIXTURE: root,
        // Fixture planners/summary writers must not overwrite real CI evidence.
        QCH_CI_REPORT_DIR: join(root, "reports"),
        QCH_TEST_DATABASE_URL: "postgresql://fixture.invalid/ci_runner_base?sslmode=disable",
        QCH_TEST_SHARDS: "2",
      },
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    child.stdout.setEncoding("utf8").on("data", (data) => { stdout += data; });
    child.stderr.setEncoding("utf8").on("data", (data) => { stderr += data; });
    const result = new Promise((resolve, reject) => {
      child.once("error", reject);
      child.once("close", (code, signal) => resolve({ code, signal, stdout, stderr }));
    });
    execution = { child, result };
    return execution;
  }

  function waitForEvents(predicate) {
    return new Promise((resolve, reject) => {
      let settled = false;
      const watcher = watch(eventPath, check);
      const deadline = setTimeout(() => finish(new Error(
        `timed out waiting for task events: ${JSON.stringify(events())}`)), 8000);
      function finish(error, value) {
        if (settled) return;
        settled = true;
        watcher.close();
        clearTimeout(deadline);
        if (error) reject(error);
        else resolve(value);
      }
      function check() {
        if (settled) return;
        const current = events();
        if (predicate(current)) finish(undefined, current);
      }
      execution.result.then((result) => {
        if (settled) return;
        const current = events();
        if (predicate(current)) finish(undefined, current);
        else finish(new Error(`runner exited before the expected events: ${JSON.stringify(result)}`));
      }, (error) => finish(error));
      check();
    });
  }

  return {
    start, events, taskCommand, waitForEvents,
    addPackage(name, imports = ["", "", ""]) {
      const directory = join(root, "new-package");
      mkdirSync(directory);
      writeFileSync(join(directory, "new_test.go"), 'package fixture\nimport "testing"\nfunc TestNew(t *testing.T) {}\n');
      const path = join(root, "config.json");
      const config = JSON.parse(readFileSync(path, "utf8"));
      config.packages.push({ name, directory, imports });
      writeFileSync(path, JSON.stringify(config));
    },
    release(name) { writeFileSync(join(root, "gates", name), "released\n"); },
  };
}

function assertStageOrder(events) {
  assert.deepEqual(events.filter((event) => event.type === "premature"), []);
  assert.deepEqual(events.filter((event) => event.type === "start" && afterTasks.includes(event.task))
    .map((event) => event.task), afterTasks);
  for (const [index, task] of afterTasks.entries()) {
    const startedAt = events.findIndex((event) => event.type === "start" && event.task === task);
    for (const predecessor of [...parallelTasks, ...afterTasks.slice(0, index)]) {
      const finishedAt = events.findIndex((event) => event.type === "finish" && event.task === predecessor);
      assert.ok(finishedAt >= 0 && finishedAt < startedAt,
        `${predecessor} must finish before ${task} starts`);
    }
    assert.ok(events.some((event) => event.type === "finish" && event.task === task), `${task} must finish`);
  }
}

test("waits for every parallel task, then runs repeatable after-checks sequentially in CLI order", async (t) => {
  const fixture = createFixture(t, { gated: [...parallelTasks, afterTasks[0]] });
  const { result } = fixture.start();
  await fixture.waitForEvents((events) => parallelTasks.every((task) =>
    events.some((event) => event.type === "start" && event.task === task)));

  // Release tasks in a different order from the runner's wait list. The final
  // checks task stays blocked until both Go shards and the extra task finish.
  for (const task of ["go-2", "extra", "checks-1", "go-1", "checks-2"]) {
    fixture.release(task);
    await fixture.waitForEvents((events) => events.some((event) => event.type === "finish" && event.task === task));
  }
  await fixture.waitForEvents((events) => events.some((event) => event.type === "start" && event.task === afterTasks[0]));
  fixture.release(afterTasks[0]);
  const completed = await result;
  assert.equal(completed.code, 0, completed.stdout + completed.stderr);
  assertStageOrder(fixture.events());
  assert.match(completed.stdout, /\[after-checks-1\] completed after-z/);
  assert.match(completed.stdout, /\[after-checks-2\] completed after-a/);
});

test("still runs after-checks and retains failures from each kind of parallel task", async (t) => {
  for (const failedTask of ["go-1", "extra", "checks-1"]) {
    await t.test(failedTask, async (t) => {
      const fixture = createFixture(t, { failures: { [failedTask]: 7 }, gated: parallelTasks });
      const { result } = fixture.start();
      await fixture.waitForEvents((events) => parallelTasks.every((task) =>
        events.some((event) => event.type === "start" && event.task === task)));
      for (const task of parallelTasks) fixture.release(task);
      const completed = await result;
      assert.notEqual(completed.code, 0, "parallel failure must survive successful after-checks");
      assert.equal(completed.signal, null);
      assertStageOrder(fixture.events());
      assert.match(completed.stdout, new RegExp(`failure in ${failedTask}: exit 7`));
    });
  }
});

test("reports a failing after-check and continues with later after-checks", async (t) => {
  const fixture = createFixture(t, { failures: { [afterTasks[0]]: 9 }, gated: parallelTasks });
  const { result } = fixture.start();
  await fixture.waitForEvents((events) => parallelTasks.every((task) =>
    events.some((event) => event.type === "start" && event.task === task)));
  for (const task of parallelTasks) fixture.release(task);
  const completed = await result;
  assert.notEqual(completed.code, 0, "after-check pipeline failure must fail the runner");
  assert.equal(completed.signal, null);
  assertStageOrder(fixture.events());
  assert.match(completed.stdout, /\[after-checks-1\] failure in after-z: exit 9/);
  assert.match(completed.stdout, /\[after-checks-2\] completed after-a/);
});

test("dry-run prints after-check commands in CLI order without executing any tasks", async (t) => {
  const fixture = createFixture(t);
  const completed = await fixture.start({ dryRun: true }).result;
  assert.equal(completed.code, 0, completed.stdout + completed.stderr);
  assert.deepEqual(completed.stdout.split("\n").filter((line) => line.startsWith("after-checks: ")),
    afterTasks.map((task) => `after-checks: ${fixture.taskCommand(task)}`));
  assert.ok(fixture.events().length > 0, "dry-run still inspects the fixture packages");
  assert.ok(fixture.events().every((event) =>
    event.type === "tool" && event.tool === "go" && event.args[0] === "list"),
  "dry-run must not execute Go tests, psql, checks, extra tasks, or after-checks");
});


test("uses one metadata load, auto-includes a new helper-based database package exactly once", async t => {
  const fixture = createFixture(t);
  fixture.addPackage("example.invalid/new", ["", "example.invalid/database", ""]);
  const completed = await fixture.start({ dryRun: true }).result;
  assert.equal(completed.code, 0, completed.stdout + completed.stderr);
  assert.equal(fixture.events().filter(event => event.tool === "go" && event.args[0] === "list").length, 1);
  const shards = completed.stdout.split("\n").filter(line => line.startsWith("shard "));
  assert.match(shards[0], /example.invalid\/database.*example.invalid\/new/);
  assert.equal(shards.join(" ").match(/example.invalid\/new/g).length, 1);
});

test("unknown extra tasks wait for the cluster-wide database transaction horizon", async t => {
  const fixture = createFixture(t, { gated: ["go-1"] });
  const { result } = fixture.start({ extraIndependent: false });
  await fixture.waitForEvents(events => events.some(event => event.type === "start" && event.task === "go-1"));
  assert.equal(fixture.events().some(event => event.type === "start" && event.task === "extra"), false);
  fixture.release("go-1");
  const completed = await result;
  assert.equal(completed.code, 0, completed.stdout + completed.stderr);
  const events = fixture.events();
  assert.ok(events.findIndex(event => event.type === "finish" && event.task === "go-1") <
    events.findIndex(event => event.type === "start" && event.task === "extra"));
});

test("cancellation stops gated subprocesses and never starts after-checks", async t => {
  const fixture = createFixture(t, { gated: parallelTasks });
  const { child, result } = fixture.start();
  await fixture.waitForEvents(events => parallelTasks.every(task => events.some(event => event.type === "start" && event.task === task)));
  child.kill("SIGTERM");
  const completed = await result;
  assert.equal(completed.code, 143);
  assert.equal(fixture.events().some(event => event.type === "start" && afterTasks.includes(event.task)), false);
});

test("format gate excludes downloaded templates and checks new source directories", t => {
  const root = mkdtempSync(join(tmpdir(), "qch-format-gate-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(join(root, ".ci-cache", "modules"), { recursive: true });
  writeFileSync(join(root, ".ci-cache", "modules", "template.go"), "package {{template}}\n");
  mkdirSync(join(root, "new package"));
  const source = join(root, "new package", "new.go");
  writeFileSync(source, "package fixture\n\nfunc New() {}\n");
  const check = () => spawnSync("make", ["-f", fileURLToPath(new URL("../../Makefile", import.meta.url)), "fmt-check"], { cwd: root, encoding: "utf8" });
  const clean = check();
  assert.equal(clean.status, 0, clean.stdout + clean.stderr);
  writeFileSync(source, "package fixture\nfunc New( ) { }\n");
  const unformatted = check();
  assert.notEqual(unformatted.status, 0);
  assert.match(unformatted.stdout, /new package\/new.go/);
  assert.doesNotMatch(unformatted.stdout + unformatted.stderr, /template.go/);
});
