import assert from "node:assert/strict";

export async function testLiveEditorConverges({ FakeForm, AGENT_ID, ENGINE, KEY, OLD_CONTENT, NEW_CONTENT, makeAgent, emptyWorkspace, buildContext, installForms, drain }) {
  // --- Test F: Live-config editor deploy -> terminal success ->
  // pending cleanup + cache invalidation + guarded re-render.
  {
    const state = {
      route: "live-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, agentContent: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let editorReadCount = 0;
    const editorReadCacheHints = [];

    const liveForm = new FakeForm({
      content: NEW_CONTENT, name: "e", description: "d", version: "1",
    });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": emptyWorkspace,
      "PUT /agents/test-agent/configs/xray": { config: { id: "lc", version: 2 } },
      "POST /tasks": (options) => {
        const body = JSON.parse(options?.body || "{}");
        if (body.action === "deploy") return { id: "lc-deploy-task" };
        editorReadCount += 1;
        editorReadCacheHints.push(body.prefer_cached);
        return { id: `editor-read-${editorReadCount}` };
      },
      "GET /tasks/lc-deploy-task": () => ({
        status: "succeeded", id: "lc-deploy-task",
      }),
      "GET /tasks/editor-read-\\d+": (_o, p) => ({
        status: "succeeded", id: p.split("/").pop(),
      }),
      "GET /tasks/editor-read-\\d+/config-snapshot": (_o, path) => ({
        content: path.includes("editor-read-1/") ? OLD_CONTENT : NEW_CONTENT,
      }),
    });

    const pages = installForms(ctx, { "#live-config-form": liveForm });
    await pages.liveConfig();
    await liveForm.dispatchSubmit({ liveIntent: "deploy" });
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[F] pending cleared after editor deploy succeeded");
    assert.ok(editorReadCount >= 2,
      "[F] forced preflight and post-deploy reads both ran");
    assert.equal(editorReadCacheHints[0], undefined,
      "[F] deploy preflight must bypass the 600-second cache");
    assert.ok(editorReadCacheHints.slice(1).includes(true),
      "[F] ordinary post-deploy page reads may request the cache");
    assert.equal(state.data.liveSources?.[KEY]?.content, NEW_CONTENT,
      "[F] cache contains post-deploy content after convergence");
  }

}

export async function testSourceDeployConverges({ FakeForm, AGENT_ID, ENGINE, KEY, OLD_CONTENT, NEW_CONTENT, makeAgent, workspaceWithConfig, buildContext, installForms, drain }) {
  // --- Test G: Source-config deploy success uses same terminal mechanism.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let srcPollCount = 0;

    const srcForm = new FakeForm({ name: "s", description: "d", content: NEW_CONTENT });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig(),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/log": { present: false, fragment: "" },
      "POST /agents/test-agent/configs/xray/source": { config: { id: "sc", version: 2 }, task: { id: "src-deploy-task" } },
      "GET /tasks/src-deploy-task": () => {
        srcPollCount += 1;
        return { status: "succeeded", id: "src-deploy-task" };
      },
    });

    const pages = installForms(ctx, { "#source-config-form": srcForm });
    await pages.agentConfig();
    await srcForm.dispatchSubmit({ sourceIntent: "deploy" });
    await drain();

    assert.ok(srcPollCount > 0,
      "[G] source-config deploy terminal was checked");
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[G] source-config pending cleared after deploy succeeded");
    assert.equal(state.data.liveSources?.[KEY], undefined,
      "[G] source-config cache invalidated after deploy succeeded");
  }

}
