// Enrollment commands and node-directory dialogs own their modal lifecycle.
export function createAgentEnrollment({ api, esc, engineName, notify, refreshAgentPage }) {
function bindModalLifecycle(wrap, onClose) {
  const previousFocus = document.activeElement;
  const restoreEnrollmentEntry = previousFocus?.matches?.(
    "[data-open-enrollment]",
  );
  const inertRoots = new Map();
  const lockBackground = () => {
    document.querySelectorAll(".desktop-app").forEach((root) => {
      if (!inertRoots.has(root)) inertRoots.set(root, root.inert || false);
      root.inert = true;
    });
  };
  const observer = new MutationObserver(lockBackground);
  const previousOverflow = document.body.style.overflow;
  let closed = false;
  lockBackground();
  observer.observe(document.body, { childList: true, subtree: true });
  document.body.style.overflow = "hidden";
  const focusable = () => [
    ...wrap.querySelectorAll(
      'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [href], [tabindex]:not([tabindex="-1"])',
    ),
  ].filter((element) =>
    !element.hidden &&
    element.getClientRects().length > 0 &&
    // Closed details can retain layout boxes, but their content cannot be focused.
    !element.closest("details:not([open]) > :not(summary)"),
  );
  const close = () => {
    if (closed) return;
    closed = true;
    observer.disconnect();
    document.removeEventListener("keydown", onKeydown);
    inertRoots.forEach((inert, root) => {
      if (root.isConnected) root.inert = inert;
    });
    document.body.style.overflow = previousOverflow;
    wrap.remove();
    const restoreTarget = previousFocus instanceof HTMLElement && previousFocus.isConnected
      ? previousFocus
      : restoreEnrollmentEntry
        ? document.querySelector("[data-open-enrollment]")
        : null;
    restoreTarget?.focus();
    onClose?.();
  };
  const onKeydown = (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
      return;
    }
    if (event.key !== "Tab") return;
    const items = focusable();
    if (!items.length) {
      event.preventDefault();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };
  document.addEventListener("keydown", onKeydown);
  wrap.querySelectorAll("[data-close]").forEach((button) => {
    button.onclick = close;
  });
  wrap.onclick = (event) => {
    if (event.target === wrap) close();
  };
  return close;
}

function bindEnrollmentRecordButtons(root, closeParent) {
  root.querySelectorAll("[data-view-enrollment-record]").forEach((button) => {
    if (button.dataset.bound === "1") return;
    button.dataset.bound = "1";
    button.onclick = async () => {
      if (button.dataset.loading === "1") return;
      button.dataset.loading = "1";
      button.disabled = true;
      try {
        const created = await api(
          `/enrollment-tokens/${encodeURIComponent(button.dataset.viewEnrollmentRecord)}/command`,
          { method: "POST" },
        );
        closeParent?.();
        showCommand(
          enrollmentInstallCommand(created),
          () => document.querySelector("[data-open-enrollment]")?.focus(),
          "复制 Agent 安装命令",
        );
      } catch (error) {
        notify(error.message, "error");
      } finally {
        button.dataset.loading = "";
        button.disabled = false;
      }
    };
  });
}

async function showAgentDirectoryDialog() {
  let entries;
  try {
    entries = await api("/agent-directory");
  } catch (error) {
    notify(error.message, "error");
    return;
  }
  const rows = (entries || [])
    .map((entry) => {
      const ports = (entry.ports || []).join("、") || "—";
      const engines = (entry.capabilities || []).map((engine) => engineName(engine)).join("、") || "无内核";
      const hidden = entry.admin_hidden ? " · 已对管理员隐藏" : "";
      return `<article data-directory-node="${esc(entry.id)}"><div><strong>${esc(entry.name)}</strong><small>${esc(entry.owner_username || "未分配账号")} · ${entry.status === "online" ? "在线" : "离线"} · ${esc(engines)} · 端口 ${esc(ports)}${hidden}</small></div><button class="button small danger-button" type="button" data-delete-directory-node="${esc(entry.id)}" data-node-name="${esc(entry.name)}">删除节点</button></article>`;
    })
    .join("");
  const wrap = document.createElement("div");
  wrap.className = "modal-backdrop";
  wrap.innerHTML = `<section class="deploy-command-modal enrollment-dialog" role="dialog" aria-modal="true" aria-labelledby="agent-directory-title" aria-describedby="agent-directory-description"><header class="deploy-command-head"><span class="deploy-command-icon" aria-hidden="true">☰</span><div><p class="eyebrow">管理员视图</p><h2 id="agent-directory-title">其他用户节点</h2><p id="agent-directory-description">列出其他账号节点，含对管理员隐藏的节点。仅可删除，不能读取配置、日志、指标或代为部署。</p></div><button class="deploy-command-close" type="button" data-close aria-label="关闭其他用户节点弹窗">×</button></header><div class="deploy-command-body enrollment-dialog-body"><section class="enrollment-history"><header><div><b>节点清单</b><small>删除会断开 Agent 并清理关联配置，不会远程卸载 QAgent。</small></div><span>${(entries || []).length}</span></header><div data-agent-directory-list>${rows || '<p class="enrollment-history-empty">没有其他账号的节点</p>'}</div></section></div></section>`;
  document.body.append(wrap);
  bindModalLifecycle(wrap);
  wrap.querySelectorAll("[data-delete-directory-node]").forEach((button) => {
    button.onclick = async () => {
      if (button.dataset.confirmDelete !== "1") {
        button.dataset.confirmDelete = "1";
        button.textContent = "再次点击确认删除";
        window.setTimeout(() => {
          if (!button.isConnected || button.disabled) return;
          button.dataset.confirmDelete = "";
          button.textContent = "删除节点";
        }, 5000);
        return;
      }
      button.disabled = true;
      try {
        await api(`/agents/${encodeURIComponent(button.dataset.deleteDirectoryNode)}`, { method: "DELETE" });
        const name = button.dataset.nodeName || "";
        button.closest("article")?.remove();
        notify(name ? `节点 ${name} 已删除` : "节点已删除");
        try {
          await refreshAgentPage();
        } catch (error) {
          notify(`节点列表刷新失败：${error.message}`, "error");
        }
      } catch (error) {
        button.disabled = false;
        notify(error.message, "error");
      }
    };
  });
}

function showEnrollmentDialog({ tokenRows, tokenCount, onDelete, onSubmit }) {
  const wrap = document.createElement("div");
  wrap.className = "modal-backdrop enrollment-backdrop";
  wrap.innerHTML = `<section class="deploy-command-modal enrollment-dialog enrollment-create-dialog" role="dialog" aria-modal="true" aria-labelledby="enrollment-dialog-title" aria-describedby="enrollment-dialog-description">
    <header class="deploy-command-head">
      <span class="deploy-command-icon" aria-hidden="true">＋</span>
      <div><h2 id="enrollment-dialog-title">添加节点</h2><p id="enrollment-dialog-description">命令仅供复制，不会自动执行。</p></div>
      <button class="deploy-command-close" type="button" data-close aria-label="关闭添加节点弹窗">×</button>
    </header>
    <div class="deploy-command-body enrollment-dialog-body">
      <form class="enrollment-dialog-form">
        <label class="enrollment-name">节点名称<input name="name" maxlength="100" required autocomplete="off" placeholder="例如 shanghai-edge-01"></label>
        <label class="enrollment-visibility">
          <input type="checkbox" name="admin_hidden" aria-labelledby="enrollment-visibility-title" aria-describedby="enrollment-visibility-description">
          <span><strong id="enrollment-visibility-title">对管理员隐藏</strong><small id="enrollment-visibility-description">管理员仍可在目录查看基本信息、删除节点。</small></span>
        </label>
        <footer class="enrollment-form-actions"><button class="button" type="button" data-close>取消</button><button class="button primary" type="submit">生成部署命令</button></footer>
      </form>
      <details class="enrollment-history enrollment-history-disclosure" aria-labelledby="enrollment-history-title" ${tokenCount ? "open" : ""}>
        <summary tabindex="0"><span><b id="enrollment-history-title">添加记录</b><span data-enrollment-history-count>${tokenCount || 0}</span></span><svg class="enrollment-history-chevron" viewBox="0 0 24 24" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg></summary>
        <p class="enrollment-history-description">删除仅撤销凭据，不影响已注册节点。</p>
        <div data-enrollment-history-list>${tokenRows || '<p class="enrollment-history-empty">暂无添加记录</p>'}</div>
      </details>
    </div>
  </section>`;
  document.body.append(wrap);
  const close = bindModalLifecycle(wrap);
  bindEnrollmentRecordButtons(wrap, close);
  wrap.querySelectorAll("[data-delete-enrollment]").forEach((button) => {
    button.onclick = async () => {
      if (button.dataset.confirmDelete !== "1") {
        button.dataset.confirmDelete = "1";
        button.dataset.defaultAriaLabel = button.getAttribute("aria-label") || "";
        button.textContent = "再次点击确认删除";
        button.setAttribute(
          "aria-label",
          "再次点击确认删除；凭据会失效，已注册节点和 Agent 不受影响",
        );
        window.setTimeout(() => {
          if (!button.isConnected || button.disabled) return;
          button.dataset.confirmDelete = "";
          button.textContent = "删除";
          if (button.dataset.defaultAriaLabel)
            button.setAttribute("aria-label", button.dataset.defaultAriaLabel);
          else button.removeAttribute("aria-label");
        }, 5000);
        return;
      }
      button.disabled = true;
      try {
        await onDelete(button.dataset.deleteEnrollment);
        button.closest("article")?.remove();
        const list = wrap.querySelector("[data-enrollment-history-list]");
        const count = list?.querySelectorAll("article").length || 0;
        const countLabel = wrap.querySelector("[data-enrollment-history-count]");
        if (countLabel) countLabel.textContent = String(count);
        if (list && count === 0)
          list.innerHTML = '<p class="enrollment-history-empty">暂无添加记录</p>';
        notify("添加节点凭据已删除");
      } catch (error) {
        button.disabled = false;
        notify(error.message, "error");
      }
    };
  });
  wrap.querySelector("form").onsubmit = async (event) => {
    event.preventDefault();
    const submit = event.currentTarget.querySelector("button[type=submit]");
    submit.disabled = true;
    try {
      const form = new FormData(event.currentTarget);
      await onSubmit(String(form.get("name") || "").trim(), form.get("admin_hidden") === "on", close);
    }
    catch (error) { submit.disabled = false; notify(error.message, "error"); }
  };
  wrap.querySelector("input").focus();
}

function enrollmentInstallCommand(created) {
  const token = String(created?.token || "");
  const name = String(created?.name || "");
  if (!token || !name) throw new Error("当前节点没有可恢复的部署命令");
  const escapedToken = token.replaceAll("'", "'\\''");
  const escapedName = name.replaceAll("'", "'\\''");
  return `curl -fsSL -H 'X-QControlHub-Enrollment: ${escapedToken}' ${location.origin}/install-agent.sh | sudo sh -s -- ${location.origin} '${escapedToken}' '${escapedName}'`;
}

function showCommand(command, onClose, heading = "复制 QAgent 部署命令") {
  const wrap = document.createElement("div");
  wrap.className = "modal-backdrop";
  wrap.innerHTML = `<section class="deploy-command-modal" role="dialog" aria-modal="true" aria-labelledby="deploy-command-title" aria-describedby="deploy-command-description"><header class="deploy-command-head"><span class="deploy-command-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="m7 8 4 4-4 4M13 16h4"/></svg></span><div><h2 id="deploy-command-title">${esc(heading)}</h2><p id="deploy-command-description">命令仅供复制，不会自动执行。</p></div><button class="deploy-command-close" type="button" data-close aria-label="关闭部署命令弹窗">×</button></header><div class="deploy-command-body"><div class="deploy-command-notice"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3 5 6v5c0 4.6 2.8 8.1 7 10 4.2-1.9 7-5.4 7-10V6l-7-3Z"/><path d="m9.5 12 1.7 1.7 3.5-3.7"/></svg><span><b>命令可重复查看</b><small>凭据受保护保存；删除添加记录、撤销或到期后失效。</small></span></div><section class="deploy-command-shell" aria-label="Agent 安装命令"><header><span><i></i>Terminal</span></header><div><span class="deploy-command-prompt" aria-hidden="true">$</span><textarea class="deploy-command-input" rows="5" readonly spellcheck="false" aria-label="Agent 安装命令" data-command>${esc(command)}</textarea></div></section></div><footer class="deploy-command-actions"><span>请在目标 Linux 节点执行</span><div><button class="button" type="button" data-close>关闭</button><button class="button primary deploy-command-copy" type="button" data-copy-command><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="11" height="11" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2v8a2 2 0 0 0 2 2h2"/></svg><span data-copy-label>复制部署命令</span></button></div></footer></section>`;
  document.body.append(wrap);
  const copyButton = wrap.querySelector("[data-copy-command]");
  const commandInput = wrap.querySelector("[data-command]");
  let resetCopyLabel;
  bindModalLifecycle(wrap, () => {
    window.clearTimeout(resetCopyLabel);
    onClose?.();
  });
  copyButton.onclick = async () => {
    try {
      await navigator.clipboard.writeText(command);
    } catch {
      commandInput.select();
      document.execCommand("copy");
      commandInput.setSelectionRange(0, 0);
    }
    const copyLabel = copyButton.querySelector("[data-copy-label]");
    copyButton.classList.add("copied");
    copyLabel.textContent = "已复制";
    window.clearTimeout(resetCopyLabel);
    resetCopyLabel = window.setTimeout(() => {
      copyButton.classList.remove("copied");
      copyLabel.textContent = "复制部署命令";
    }, 1800);
  };
  copyButton.focus();
}
  function bindEnrollmentPage(enrollmentHistory = {}) {
  document.querySelectorAll("[data-open-enrollment]").forEach((button) => {
    button.onclick = () =>
      showEnrollmentDialog({
        tokenRows: enrollmentHistory.tokenRows || "",
        tokenCount: enrollmentHistory.tokenCount || 0,
        onDelete: async (id) => {
          await api(`/enrollment-tokens/${encodeURIComponent(id)}`, {
            method: "DELETE",
          });
          try {
            await refreshAgentPage();
          } catch (error) {
            notify(`添加记录刷新失败：${error.message}`, "error");
          }
        },
        onSubmit: async (name, adminHidden, close) => {
          const created = await api("/enrollment-tokens", {
            method: "POST",
            body: JSON.stringify({ name, admin_hidden: adminHidden }),
          });
          const command = enrollmentInstallCommand(created);
          close();
          showCommand(command, async () => {
            try {
              await refreshAgentPage();
            } catch (error) {
              notify(`添加记录刷新失败，部署命令未受影响：${error.message}`, "error");
            }
          });
        },
      });
  });
  bindEnrollmentRecordButtons(document);
  document.querySelectorAll("[data-open-agent-directory]").forEach((button) => {
    button.onclick = () => showAgentDirectoryDialog();
  });
  document.querySelectorAll("[data-view-enrollment-command]").forEach((button) => {
    button.onclick = async () => {
      try {
        const created = await api(
          `/agents/${encodeURIComponent(button.dataset.viewEnrollmentCommand)}/enrollment-command`,
          { method: "POST" },
        );
        showCommand(enrollmentInstallCommand(created), null, "复制 Agent 安装命令");
      } catch (error) {
        notify(error.message, "error");
      }
    };
  });

  }
  return { bindEnrollmentPage, bindEnrollmentRecordButtons, showAgentDirectoryDialog, showEnrollmentDialog, enrollmentInstallCommand, showCommand };
}
