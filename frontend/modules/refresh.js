const boundEvents = new WeakMap();

const insertedMotionSelector = [
  ".qch-swap-panel",
  ".task-event",
  ".core-log-row",
  ".node-card",
  ".service-card",
  ".client-access-node-card",
  ".access-control-card",
  ".substore-agent-card",
  ".settings-version-card",
  ".template-card",
].join(",");

function markInsertedMotion(node) {
  if (node?.nodeType !== 1 || !node.matches(insertedMotionSelector)) {
    return node;
  }
  node.classList.add("qch-reconcile-enter");
  node.addEventListener(
    "animationend",
    () => node.classList.remove("qch-reconcile-enter"),
    { once: true },
  );
  return node;
}

export function bindEvent(target, type, handler, options) {
  if (!target) return;
  let events = boundEvents.get(target);
  if (!events) {
    events = new Map();
    boundEvents.set(target, events);
  }
  const eventKey = `${type}:${Boolean(
    options === true || options?.capture,
  )}`;
  const existing = events.get(eventKey);
  if (existing) {
    existing.handler = handler;
    return;
  }
  const binding = { handler };
  binding.listener = (event) => binding.handler(event);
  events.set(eventKey, binding);
  target.addEventListener(type, binding.listener, options);
}

function nodeKey(node) {
  if (node?.nodeType !== 1) return "";
  const explicit = node.getAttribute("data-refresh-key");
  if (explicit) return `refresh:${explicit}`;
  if (node.id) return `id:${node.id}`;
  for (const attribute of [
    "data-agent-node",
    "data-context-agent",
    "data-access-agent",
    "data-live-agent",
    "data-live-engine",
    "data-archive-config",
    "data-dashboard-agent",
    "data-dashboard-task",
    "data-task-id",
    "data-task-status-filter",
    "data-core-log-agent",
    "data-context-traffic-agent",
    "data-inbound",
    "data-protocol",
    "data-config-field",
    "data-revision",
    "data-user-form",
  ]) {
    const value = node.getAttribute(attribute);
    if (value != null) return `${attribute}:${value}`;
  }
  if (/^(INPUT|SELECT|TEXTAREA|BUTTON)$/.test(node.tagName) && node.name) {
    const value = node.getAttribute("value");
    const distinguishByValue =
      node.tagName === "BUTTON" ||
      (node.tagName === "INPUT" &&
        ["checkbox", "radio"].includes(node.getAttribute("type")));
    return distinguishByValue && value != null
      ? `${node.tagName}:name:${node.name}:value:${value}`
      : `${node.tagName}:name:${node.name}`;
  }
  if (node.tagName === "OPTION" && node.getAttribute("value") != null)
    return `OPTION:value:${node.getAttribute("value")}`;
  return "";
}

function compatible(current, fresh) {
  return (
    current?.nodeType === fresh?.nodeType &&
    (current.nodeType !== 1 || current.tagName === fresh.tagName)
  );
}

function syncAttributes(current, fresh, metrics) {
  const preserveOpen = current.tagName === "DETAILS" || current.tagName === "DIALOG";
  const preserveInert = current.classList?.contains("desktop-app") && current.inert;
  const freshNames = new Set(
    [...fresh.attributes].map((attribute) => attribute.name),
  );
  [...current.attributes].forEach((attribute) => {
    if (preserveOpen && attribute.name === "open") return;
    if (preserveInert && attribute.name === "inert") return;
    if (!freshNames.has(attribute.name)) {
      current.removeAttribute(attribute.name);
      metrics.updated += 1;
    }
  });
  [...fresh.attributes].forEach((attribute) => {
    if (preserveOpen && attribute.name === "open") return;
    if (preserveInert && attribute.name === "inert") return;
    if (current.getAttribute(attribute.name) !== attribute.value) {
      current.setAttribute(attribute.name, attribute.value);
      metrics.updated += 1;
    }
  });
}

function controlState(element) {
  if (!/^(INPUT|SELECT|TEXTAREA)$/.test(element.tagName)) return null;
  const editable = !element.readOnly && !element.disabled;
  const active = element.ownerDocument?.activeElement === element;
  const dirty =
    element.tagName === "SELECT"
      ? [...element.options].some(
          (option) => option.selected !== option.defaultSelected,
        )
      : element.type === "checkbox" || element.type === "radio"
        ? element.checked !== element.defaultChecked
        : element.value !== element.defaultValue;
  const preserve = editable && (active || dirty);
  if (!preserve) return null;
  return {
    value: element.value,
    checked: element.checked,
    selectionStart: element.selectionStart,
    selectionEnd: element.selectionEnd,
    selectionDirection: element.selectionDirection,
  };
}

function restoreControlState(element, state) {
  if (!state) return;
  element.value = state.value;
  if (typeof state.checked === "boolean") element.checked = state.checked;
  if (
    state.selectionStart != null &&
    typeof element.setSelectionRange === "function"
  ) {
    element.setSelectionRange(
      state.selectionStart,
      state.selectionEnd,
      state.selectionDirection || "none",
    );
  }
}

function reconcileChildren(current, fresh, metrics) {
  const existing = [...current.childNodes];
  const used = new Set();
  const keyed = new Map(
    existing
      .map((child) => [nodeKey(child), child])
      .filter(([key]) => key),
  );
  const desired = [...fresh.childNodes].map((freshChild, index) => {
    const key = nodeKey(freshChild);
    let candidate = key ? keyed.get(key) : null;
    if (used.has(candidate)) candidate = null;
    if (!candidate) {
      const positional = existing[index];
      if (!used.has(positional) && !nodeKey(positional) && compatible(positional, freshChild))
        candidate = positional;
    }
    if (!candidate) {
      candidate = existing.find(
        (child) =>
          !used.has(child) &&
          !nodeKey(child) &&
          compatible(child, freshChild),
      );
    }
    if (!candidate) {
      metrics.inserted += 1;
      return markInsertedMotion(freshChild.cloneNode(true));
    }
    used.add(candidate);
    return reconcileNode(candidate, freshChild, metrics);
  });

  desired.forEach((child, index) => {
    const currentAtIndex = current.childNodes[index] || null;
    if (currentAtIndex !== child) current.insertBefore(child, currentAtIndex);
  });
  [...current.childNodes].forEach((child) => {
    if (!desired.includes(child)) {
      child.remove();
      metrics.removed += 1;
    }
  });
}

function reconcileNode(current, fresh, metrics) {
  if (!compatible(current, fresh)) {
    const replacement = fresh.cloneNode(true);
    current.replaceWith(replacement);
    metrics.replaced += 1;
    return replacement;
  }
  if (current.nodeType === 3) {
    if (current.data !== fresh.data) {
      current.data = fresh.data;
      metrics.updated += 1;
    }
    return current;
  }
  if (current.tagName === "DIALOG" && current.open) return current;
  const state = controlState(current);
  const detailsOpen = current.tagName === "DETAILS" ? current.open : null;
  const scrollTop = current.scrollTop;
  const scrollLeft = current.scrollLeft;
  syncAttributes(current, fresh, metrics);
  reconcileChildren(current, fresh, metrics);
  restoreControlState(current, state);
  if (detailsOpen != null) current.open = detailsOpen;
  if (scrollTop) current.scrollTop = scrollTop;
  if (scrollLeft) current.scrollLeft = scrollLeft;
  return current;
}

export function captureViewState(root, documentObject = document, windowObject = window) {
  const active = root.contains(documentObject.activeElement)
    ? documentObject.activeElement
    : null;
  const scrollers = [
    root.matches?.(".workspace-main,[data-refresh-scroll]") ? root : null,
    ...root.querySelectorAll(".workspace-main,[data-refresh-scroll]"),
  ]
    .filter(Boolean)
    .map((element) => ({
      element,
      top: element.scrollTop,
      left: element.scrollLeft,
    }));
  return {
    active,
    selectionStart: active?.selectionStart,
    selectionEnd: active?.selectionEnd,
    selectionDirection: active?.selectionDirection,
    scrollers,
    windowX: windowObject.scrollX,
    windowY: windowObject.scrollY,
  };
}

export function restoreViewState(state, windowObject = window) {
  state.scrollers.forEach(({ element, top, left }) => {
    if (!element.isConnected) return;
    element.scrollTop = top;
    element.scrollLeft = left;
  });
  if (state.active?.isConnected) {
    state.active.focus({ preventScroll: true });
    if (
      state.selectionStart != null &&
      typeof state.active.setSelectionRange === "function"
    ) {
      state.active.setSelectionRange(
        state.selectionStart,
        state.selectionEnd,
        state.selectionDirection || "none",
      );
    }
  }
  if (
    windowObject.scrollX !== state.windowX ||
    windowObject.scrollY !== state.windowY
  )
    windowObject.scrollTo(state.windowX, state.windowY);
}

export function reconcileView(current, fresh, runtime = {}) {
  const documentObject = runtime.document || document;
  const windowObject = runtime.window || window;
  const metrics = runtime.metrics || {
    inserted: 0,
    removed: 0,
    replaced: 0,
    updated: 0,
  };
  const state = captureViewState(current, documentObject, windowObject);
  const result = reconcileNode(current, fresh, metrics);
  restoreViewState(state, windowObject);
  return result;
}

export function createRefreshChannel({
  isCurrent = () => true,
  getScope = () => null,
} = {}) {
  let sequence = 0;
  let controller = null;
  return {
    async run(load, apply) {
      const request = ++sequence;
      const scope = getScope();
      controller?.abort();
      controller = new AbortController();
      try {
        const value = await load(controller.signal);
        if (
          request !== sequence ||
          scope !== getScope() ||
          !isCurrent()
        )
          return false;
        await apply(value);
        return true;
      } catch (error) {
        if (
          error?.name === "AbortError" ||
          request !== sequence ||
          scope !== getScope() ||
          !isCurrent()
        )
          return false;
        throw error;
      }
    },
    invalidate() {
      sequence += 1;
      controller?.abort();
      controller = null;
    },
  };
}

export function createPoller({
  run,
  isActive,
  delay,
  setTimer = (callback, timeout) => setTimeout(callback, timeout),
  clearTimer = (timer) => clearTimeout(timer),
}) {
  let timer = null;
  let running = false;
  let queued = false;
  const schedule = () => {
    clearTimer(timer);
    timer = null;
    if (!isActive()) return;
    timer = setTimer(trigger, delay());
  };
  const trigger = async () => {
    clearTimer(timer);
    timer = null;
    if (!isActive()) return;
    if (running) {
      queued = true;
      return;
    }
    running = true;
    try {
      do {
        queued = false;
        await run();
      } while (queued && isActive());
    } finally {
      running = false;
      schedule();
    }
  };
  return {
    start: schedule,
    trigger,
    stop() {
      clearTimer(timer);
      timer = null;
      queued = false;
    },
    timerCount: () => (timer == null ? 0 : 1),
  };
}

export function createInteractionGate() {
  let active = 0;
  const pending = new Map();
  const flush = () => {
    if (active || !pending.size) return;
    const callbacks = [...pending.values()];
    pending.clear();
    callbacks.forEach((callback) => callback());
  };
  return {
    begin() {
      active += 1;
      let released = false;
      return () => {
        if (released) return;
        released = true;
        active = Math.max(0, active - 1);
        flush();
      };
    },
    defer(callback, key = "default") {
      pending.set(key, callback);
      return flush();
    },
    cancel() {
      active = 0;
      pending.clear();
    },
    activeCount: () => active,
    pendingCount: () => pending.size,
  };
}

export function createLatestRenderScheduler(renderOnce, { cancelActive } = {}) {
  let running = false;
  let pending = false;
  let active = null;

  return function scheduleRender() {
    pending = true;
    if (running) {
      cancelActive?.();
      return active;
    }

    running = true;
    active = (async () => {
      try {
        while (pending) {
          pending = false;
          await renderOnce();
        }
      } finally {
        running = false;
        active = null;
      }
    })();
    return active;
  };
}
