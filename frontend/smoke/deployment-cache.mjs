import assert from "node:assert/strict";

import { installConfigPages } from "../modules/configs.js";

export async function testAbortedMonitorRecovery({ FakeForm, AGENT_ID, ENGINE, KEY, OLD_CONTENT, NEW_CONTENT, makeAgent, workspaceWithConfig, buildContext, installForms, drain }) {
  // --- Test A: Route abort on monitor -> reconcile sees running ->
  // recovery poller detects succeeded -> cache invalidated -> fresh read.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, agentContent: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let freshReadCount = 0;

    const planForm = new FakeForm({
      operation: "delete", tag: "old-inbound", listen: "0.0.0.0",
      port: "443", username: "", credential: "", secondary_credential: "",
      method: "", transport: "raw", transport_path: "",
      tls_enabled: "0", certificate_path: "", private_key_path: "",
      reality_enabled: "0", reality_private_key: "", reality_public_key: "",
      reality_short_id: "", reality_server_name: "",
    });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig(),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/.*": { present: false, fragment: "" },
      "POST /agents/test-agent/configs/xray/server-inbounds": () => ({
        config: { id: "cfg-new", version: 2 },
        task: { id: "deploy-t1", status: "pending" },
      }),
      "GET /tasks/deploy-t1": (() => {
        let calls = 0;
        return () => {
          calls += 1;
          if (calls <= 1) throw new DOMException("route abort", "AbortError");
          return { status: "succeeded", id: "deploy-t1" };
        };
      })(),
      "POST /tasks": (options) => {
        const body = JSON.parse(options?.body || "{}");
        if (body.action === "read-managed-config") {
          assert.equal(body.prefer_cached, true,
            "automatic live-config reads should request the 600-second server snapshot cache");
          freshReadCount += 1;
          return { id: `read-${freshReadCount}` };
        }
        return { id: "other" };
      },
      "GET /tasks/read-\\d+": (_o, p) => ({ status: "succeeded", id: p.split("/").pop() }),
      "GET /tasks/read-\\d+/config-snapshot": { content: NEW_CONTENT },
    });

    const pages = installForms(ctx, { "#server-plan-form": planForm });
    state.data.inboundTag = "old-inbound";
    await pages.agentConfig();
    await planForm.dispatchSubmit({ planIntent: "deploy" });
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[A] route-aborted status reads recover automatically without revisiting the page");

    state.route = "live-config";
    await pages.liveConfig();
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[A] pending cleared after recovery poller saw succeeded");
    assert.ok(freshReadCount >= 1,
      "[A] fresh read fired after convergence");
    if (state.data.liveSources?.[KEY]?.content)
      assert.notEqual(state.data.liveSources[KEY].content, OLD_CONTENT,
        "[A] old content not persisted after convergence");
  }

}

export async function testFailedDeployPreservesCache({ FakeForm, AGENT_ID, ENGINE, KEY, OLD_CONTENT, makeAgent, workspaceWithConfig, buildContext, installForms, drain }) {
  // --- Test B: Field deploy failure does NOT invalidate (real handler).
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let failPollCount = 0;
    let fieldPostCalled = false;

    const fieldForm = new FakeForm({ mutation: "modify", fragment: '{"level":"info"}' });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig({ version: 3, name: "existing-cfg" }),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/log": { present: true, fragment: "{}" },
      "POST /agents/test-agent/configs/xray/fields/log": () => {
        fieldPostCalled = true;
        return { config: { version: 4 }, task: { id: "field-deploy-fail", status: "pending" } };
      },
      "GET /tasks/field-deploy-fail": () => {
        failPollCount += 1;
        return failPollCount <= 2
          ? { status: "running", id: "field-deploy-fail" }
          : { status: "failed", id: "field-deploy-fail", error: "node down" };
      },
    });

    const pages = installForms(ctx, { "#field-form": fieldForm });
    await pages.agentConfig();
    await fieldForm.dispatchSubmit({ fieldIntent: "deploy" });
    await drain();

    assert.ok(fieldPostCalled, "[B] field POST was called");
    assert.ok(failPollCount > 0, "[B] deploy terminal was polled");
    assert.equal(state.data.liveSources?.[KEY]?.content, OLD_CONTENT,
      "[B] cache NOT invalidated after deploy failed");
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[B] pending entry cleared after failed terminal");
  }

}

export async function testValidationPreservesCache({ FakeForm, AGENT_ID, ENGINE, KEY, OLD_CONTENT, makeAgent, workspaceWithConfig, buildContext, installForms, drain }) {
  // --- Test C: Validate-only preserves cache and records no pending.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };

    const planForm = new FakeForm({
      operation: "modify", tag: "t", listen: "127.0.0.1", port: "8443",
      username: "u", credential: "c", secondary_credential: "",
      method: "", transport: "raw", transport_path: "",
      tls_enabled: "0", certificate_path: "", private_key_path: "",
      reality_enabled: "0", reality_private_key: "", reality_public_key: "",
      reality_short_id: "", reality_server_name: "",
    });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig(),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/.*": { present: false, fragment: "" },
      "POST /agents/test-agent/configs/xray/server-inbounds": {
        config: { version: 2 }, task: { id: "v-only-task" } },
    });

    installForms(ctx, { "#server-plan-form": planForm });
    const p = installConfigPages(ctx);
    await p.agentConfig();
    await planForm.dispatchSubmit({ planIntent: "validate" });
    await drain();

    assert.equal(state.data.liveSources?.[KEY]?.content, OLD_CONTENT,
      "[C] validate-only preserves cached content");
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[C] no pending deploy recorded for validate-only");
  }

}

export async function testIndependentServiceCaches({ AGENT_ID, ENGINE, KEY, OLD_CONTENT, NEW_CONTENT, makeAgent, emptyWorkspace, buildContext, installForms, drain }) {
  // --- Test D: Different agent|engine keys do not cross-contaminate.
  {
    const OTHER_AGENT = "other-agent";
    const OTHER_KEY = `${OTHER_AGENT}|${ENGINE}`;
    const state = {
      route: "live-config",
      data: {
        agents: [makeAgent(), makeAgent(OTHER_AGENT)],
        agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: {
          [KEY]: { content: OLD_CONTENT, reading: false },
          [OTHER_KEY]: { content: '{"other":true}', reading: false },
        },
        pendingDeployTasks: { [KEY]: { taskId: "dep-a" } },
      },
      session: { role: "admin" },
    };

    const { ctx } = buildContext(state, {
      "GET /agents": () => state.data.agents,
      "GET /agents/test-agent/configs/xray/workspace": emptyWorkspace,
      "GET /tasks/dep-a": { status: "succeeded", id: "dep-a" },
      "POST /tasks": { id: "r-x" },
      "GET /tasks/r-x": { status: "succeeded", id: "r-x" },
      "GET /tasks/r-x/config-snapshot": { content: NEW_CONTENT },
    });

    installForms(ctx, {});
    const pages = installConfigPages(ctx);
    await pages.liveConfig();
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[D] pending deploy cleared for target agent");
    assert.notEqual(state.data.liveSources?.[KEY]?.content, OLD_CONTENT,
      "[D] target agent's old content replaced after reconciliation");
    assert.equal(state.data.liveSources?.[OTHER_KEY]?.content, '{"other":true}',
      "[D] other agent's cache NOT affected");
    assert.equal(state.data.pendingDeployTasks?.[OTHER_KEY], undefined,
      "[D] other agent has no pending to clear (was never set)");
  }

}
