import { installUsers } from "../modules/users.js";
import { assert, esc, waitFor, input } from "./users-helpers.mjs";

export async function testInvitationRuntime() {
  const state = { route: "my-quota", data: {}, session: { role: "user", user_id: "recipient" } };
  let access = { isolated: true, revision: 4, shares: [
    { id: "shr_pending", agent_id: "shared", agent_name: "共享 Agent", owner_username: "<img src=x onerror=alert(1)>", enabled: true, status: "pending", invitation_revision: 2, engines: ["mihomo", "xray"], ports: Array.from({ length: 101 }, (_, index) => 21000 + index), limit_bytes: 1024 ** 3, used_bytes: 64 },
    { id: "shr_revoked", agent_id: "revoked", agent_name: "已撤销 Agent", enabled: false, status: "pending", invitation_revision: 8, engines: [], ports: [], limit_bytes: 0, used_bytes: 0 },
  ] };
  const writes = [], notices = [];
  let hold = false, release, conflict = false, confirm = true, holdRead = false, releaseRead, failRead = false;
  const pages = installUsers({
    state, esc, notify: message => notices.push(message), confirmAction: async () => confirm,
    shell: markup => { document.body.innerHTML = `<main>${markup}</main>`; },
    api: async (path, options = {}) => {
      if (!options.method) {
        assert(path === "/agent-access", "quota page loaded host/user administration");
        if (failRead) throw new Error("邀请读取失败");
        const result = structuredClone(access);
        if (holdRead) await new Promise(resolve => { releaseRead = resolve; });
        return result;
      }
      assert(path === "/agent-access/shr_pending/response" && options.method === "POST", "response addressed an unintended share");
      const input = JSON.parse(options.body), target = access;
      writes.push(input);
      if (hold) await new Promise(resolve => { release = resolve; });
      if (conflict || input.revision !== target.shares[0].invitation_revision) throw new Error("共享邀请已变更，请刷新后重试。");
      target.shares[0].status = input.decision === "accept" ? "accepted" : "rejected";
      target.shares[0].invitation_revision++;
      target.revision++;
      return structuredClone(target);
    },
  });
  const card = () => document.querySelector('[data-quota-share="shr_pending"]');
  const dialog = () => document.querySelector(".agent-invitation-dialog");
  const open = () => card().querySelector("[data-share-open]").click();
  const accept = () => dialog()?.querySelector('[data-share-decision="accept"]');
  const reject = () => dialog()?.querySelector('[data-share-decision="reject"]') || card()?.querySelector('[data-share-decision="reject"]');
  await pages.myQuota();
  assert(card().textContent.includes("待接受") && card().querySelector("[data-share-open]"), "pending invitation lacks a dedicated entry");
  assert(card().textContent.includes("端口 21000-21100"), "quota card did not compact the allocated port range");
  assert(!card().querySelector("[data-share-decision]"), "accept/reject actions still appear inline on the quota page");
  assert(!document.querySelector(".user-quota-card img"), "inviter identity was not escaped");
  assert(!document.querySelector('[data-quota-share="shr_revoked"] :is([data-share-decision],[data-share-open])'), "withdrawn invitation can be accepted");
  assert(card().scrollWidth <= card().clientWidth + 1, "invitation card overflows mobile viewport");
  open();
  assert(dialog()?.open && accept() && reject(), "dedicated dialog lacks consent actions");
  assert(dialog().textContent.includes("共享 Agent") && dialog().textContent.includes("21000-21100") && !dialog().querySelector("img"), "dialog lost its port range or failed to escape the owner");
  assert(dialog().textContent.includes("Mihomo / Xray") && !dialog().textContent.includes("sing-box"), "consent terms did not show the exact engine allocation");
  // Measure the final layout, not the shared dialog entrance transform.
  await Promise.all(dialog().getAnimations().map(animation => animation.finished));
  const bounds = dialog().getBoundingClientRect(), footer = dialog().querySelector("footer").getBoundingClientRect();
  assert(dialog().scrollWidth <= dialog().clientWidth + 1 && footer.bottom <= innerHeight + 1, "invitation dialog clips controls or overflows");
  if (innerWidth <= 600) assert(Math.abs(bounds.width - innerWidth) <= 1 && Math.abs(bounds.height - innerHeight) <= 1, "mobile invitation is not a full-page overlay");
  dialog().querySelector("[data-invitation-close]").click();
  assert(!dialog() && writes.length === 0 && card().textContent.includes("待接受"), "closing an invitation rejected it");
  open();
  dialog().dispatchEvent(new Event("cancel", { cancelable: true }));
  assert(!dialog() && writes.length === 0, "Escape changed the invitation decision");
  for (const [ports, label] of [[[0], "无限制"], [[], "未分配"]]) {
    access.shares[0].ports = ports;
    access.shares[0].invitation_revision++;
    await pages.myQuota();
    assert(card().textContent.includes(`端口 ${label}`), "quota card lost the port permission mode");
    open();
    assert(dialog().querySelector(".agent-invitation-terms").textContent.includes(`端口${label}`),
      "invitation displayed a sentinel or silently widened empty port authority");
    dialog().querySelector("[data-invitation-close]").click();
  }
  open();
  hold = true;
  accept().click();
  reject().click();
  assert(writes.length === 1 && accept().disabled && reject().disabled && pages.hasUnsavedChanges(), "duplicate response or unlocked in-flight action");
  await pages.myQuota();
  assert(accept().disabled && document.querySelector("[data-quota-refresh]").disabled, "refresh bypassed in-flight response lock");
  // A read begun before acceptance must not restore the old invitation.
  holdRead = true;
  const oldRead = pages.myQuota();
  await waitFor(() => Boolean(releaseRead), "stale read did not start");
  hold = false;
  release();
  await waitFor(() => card().textContent.includes("已接受"), "acceptance did not update the card");
  releaseRead();
  holdRead = false;
  await oldRead;
  assert(!accept() && card().textContent.includes("已接受") && !pages.hasUnsavedChanges(), "stale read reversed acceptance");
  confirm = false;
  reject().click();
  await Promise.resolve();
  assert(writes.length === 1, "canceling leave changed consent");
  confirm = true;
  reject().click();
  await waitFor(() => card().textContent.includes("已拒绝"), "recipient cannot leave an accepted share");
  assert(!accept() && !reject(), "rejected invitation is still actionable");

  access.shares[0].status = "pending";
  access.shares[0].invitation_revision++;
  await pages.myQuota();
  open();
  conflict = true;
  accept().click();
  await waitFor(() => !dialog().querySelector("[data-invitation-error]").hidden, "stale invitation did not report conflict inside its dialog");
  assert(!accept().disabled && card().textContent.includes("待接受"), "failed response granted access or stranded controls");
  conflict = false;
  access.shares[0].ports = [21002];
  access.shares[0].invitation_revision++;
  holdRead = true;
  releaseRead = null;
  const beforeReload = writes.length;
  dialog().querySelector("[data-invitation-reload]").click();
  await waitFor(() => Boolean(releaseRead), "invitation refresh did not start");
  accept().click();
  reject().click();
  assert(accept().disabled && reject().disabled && writes.length === beforeReload, "refresh allowed a decision on stale invitation terms");
  holdRead = false;
  releaseRead();
  await waitFor(() => dialog()?.textContent.includes("21002"), "invitation refresh did not load changed terms");
  reject().click();
  await waitFor(() => card().textContent.includes("已拒绝"), "pending invitation cannot be rejected");
  assert(!dialog(), "successful response left the invitation dialog open");

  access.shares[0].status = "pending";
  access.shares[0].engines = [];
  access.shares[0].invitation_revision++;
  await pages.myQuota();
  open();
  assert(accept().disabled && !reject().disabled, "未分配内核的旧邀请可以被接受");
  failRead = true;
  dialog().querySelector("[data-invitation-reload]").click();
  await waitFor(() => dialog()?.querySelector("[data-invitation-error]").textContent.includes("读取失败"), "刷新失败没有保留错误");
  assert(accept().disabled && !reject().disabled, "刷新失败解除了接受按钮的安全限制");
  failRead = false;
  pages.closeInvitation();
  access.shares[0].engines = ["mihomo"];
  access.shares[0].status = "pending";
  access.shares[0].invitation_revision++;
  await pages.myQuota();
  open();
  pages.closeInvitation();
  state.route = "tasks";
  assert(!dialog(), "navigation left a stale invitation overlay");
  state.route = "my-quota";
  await pages.myQuota();
  open();
  hold = true;
  release = null;
  accept().click();
  await waitFor(() => Boolean(release), "logout-race response did not start");
  const noticeCount = notices.length;
  state.data = {};
  state.session = { role: "user", user_id: "another-account" };
  access = { isolated: true, revision: 1, shares: [] };
  await pages.myQuota();
  release();
  await new Promise(resolve => setTimeout(resolve, 30));
  assert(!dialog() && !card() && state.data.agentAccess.shares.length === 0 && notices.length === noticeCount, "old response contaminated a different account");
}
