import assert from "node:assert/strict";
import { enrollmentInstallCommand } from "./modules/enrollment-command.js";

const previousLocation = Object.getOwnPropertyDescriptor(globalThis, "location");
try {
  Object.defineProperty(globalThis, "location", { configurable: true, value: { origin: "https://panel.example.invalid" } });
  assert.throws(() => enrollmentInstallCommand({}), /没有可恢复的部署命令/);
  const command = enrollmentInstallCommand({ token: "quoted'token", name: "edge'node" });
  assert.ok(command.includes("'quoted'\\''token' 'edge'\\''node'"));
  assert.ok(command.includes("X-QControlHub-Enrollment: quoted'\\''token"));
  assert.ok(command.includes("https://panel.example.invalid/install-agent.sh"));
} finally {
  if (previousLocation) Object.defineProperty(globalThis, "location", previousLocation);
  else delete globalThis.location;
}

console.log("Native enrollment command quoting smoke passed");
