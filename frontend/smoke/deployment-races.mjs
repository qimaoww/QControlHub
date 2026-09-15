import assert from "node:assert/strict";

export async function testDeploymentABA({ FakeForm, deferred, AGENT_ID, ENGINE, KEY, OLD_CONTENT, NEW_CONTENT, makeAgent, workspaceWithConfig, buildContext, installForms, drain }) {
  // --- Test E (ABA): Old deploy A succeeds -> cache invalidated.
  // Then B overwrites pending; B fails later -> only B's pending cleared.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };

    const gateA = deferred();
    const gateB = deferred();
    let editorReadCountE = 0;

    const planForm = new FakeForm({
      operation: "modify", tag: "t", listen: "0.0.0.0", port: "443",
      username: "", credential: "", secondary_credential: "",
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
        config: { id: "cfg-a", version: 2 },
        task: { id: "dep-A", status: "pending" },
      }),
      "GET /tasks/dep-A": () => gateA.promise,
      "GET /tasks/dep-B": (() => {
        let bCalls = 0;
        return async () => {
          bCalls += 1;
          if (bCalls <= 1) return { status: "running", id: "dep-B" };
          return await gateB.promise;
        };
      })(),
      "POST /tasks": (options) => {
        const body = JSON.parse(options?.body || "{}");
        if (body.action === "read-managed-config") {
          editorReadCountE += 1;
          return { id: "read-e1" };
        }
        return { id: "other-e" };
      },
      "GET /tasks/read-e1": { status: "succeeded", id: "read-e1" },
      "GET /tasks/read-e1/config-snapshot": { content: NEW_CONTENT },
    });

    const pages = installForms(ctx, { "#server-plan-form": planForm });
    await pages.agentConfig();

    // Submit deploy A via form handler (starts monitor A).
    await planForm.dispatchSubmit({ planIntent: "deploy" });
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY]?.taskId, "dep-A",
      "[E] dep-A recorded as pending");

    // Simulate deploy B overwriting A in the pending map
    // (happens when a second mutation fires before A completes).
    state.data.pendingDeployTasks[KEY] = { taskId: "dep-B" };

    // Release gate A -> old dep-A succeeds.
    gateA.resolve({ status: "succeeded", id: "dep-A" });
    await drain();

    // Cache IS invalidated because A succeeded and changed the node file.
    assert.equal(state.data.liveSources?.[KEY], undefined,
      "[E] cache invalidated by old deploy A's success");

    // Pending still holds dep-B (A did NOT clear it via CAS).
    assert.equal(state.data.pendingDeployTasks?.[KEY]?.taskId, "dep-B",
      "[E] newer dep-B pending NOT cleared by old dep-A completion");

    // Step 4: Start B's recovery poller by entering live-config.
    // reconcilePendingDeploy sees dep-B running -> starts recovery poller.
    state.route = "live-config";
    await pages.liveConfig();
    await drain();

    // Step 5: Release gate B -> dep-B fails.
    gateB.resolve({ status: "failed", id: "dep-B", error: "conflict" });
    await drain();

    // Assert: B's failure cleared its own pending record.
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[E] dep-B pending cleared after B's own terminal failure");

    // Strict: post-A fresh read completed and cached NEW_CONTENT.
    assert.ok(editorReadCountE >= 1,
      "[E] post-A fresh managed-config read fired");
    assert.equal(state.data.liveSources?.[KEY]?.content, NEW_CONTENT,
      "[E] cache contains post-deploy content (not OLD_CONTENT) after B failed");
  }

}
