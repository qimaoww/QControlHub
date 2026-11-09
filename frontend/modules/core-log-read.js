import { coreLogFilterCounts } from "./core-log-model.js";
import { reuseCoreLogEntries } from "./core-log-projection.js";

// Weak keys add no retained windows beyond the account's active view/LRU.
// A related or partial snapshot is never a validator for a different URL.
const validators = new WeakMap();

// Bound the entire operation, including response.json(), and release callers
// even when a transport ignores abort. Abort still cancels real fetch/SQL work.
export async function requestCoreLogJSON(api, path, { signal, timeout = 15000, ...options } = {}) {
  const controller = new AbortController();
  let timer, cancel;
  const stopped = new Promise((_, reject) => { cancel = reject; });
  const abort = () => {
    const reason = signal?.reason || new DOMException("Read canceled", "AbortError");
    controller.abort(reason); cancel(reason);
  };
  signal?.addEventListener("abort", abort, { once: true });
  if (signal?.aborted) abort();
  timer = setTimeout(() => {
    const error = new Error("读取超时，请重试。");
    controller.abort(new DOMException("Read timed out", "TimeoutError"));
    cancel(error);
  }, timeout);
  try {
    return await Promise.race([stopped, Promise.resolve().then(() => {
      if (controller.signal.aborted) throw controller.signal.reason;
      return api(path, { ...options, signal: controller.signal });
    })]);
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
    controller.abort();
  }
}

async function readSnapshot(api, params, signal, previous) {
  const path = `/core-logs?${params}`;
  const known = previous && validators.get(previous)?.get(path);
  let etag = "";
  const entries = await requestCoreLogJSON(api, path, {
    signal,
    ...(known ? { conditional: { etag: known, value: previous } } : {}),
    onResponse: response => { etag = response.headers?.get("ETag") || ""; },
  });
  const snapshot = reuseCoreLogEntries(previous, entries);
  const tags = validators.get(snapshot) || new Map();
  tags.delete(path);
  if (etag) tags.set(path, etag);
  // A sparse window can be identical at several limits. Keep each validator
  // tied to its exact URL without retaining an unbounded list of query strings.
  while (tags.size > 3) tags.delete(tags.keys().next().value);
  if (tags.size) validators.set(snapshot, tags);
  else validators.delete(snapshot);
  return snapshot;
}

// Start the full read immediately. A retained window can supply the preview
// after fresh grants; otherwise start the preview alongside it. A fast full
// response wins immediately. Quiet previews are complete.
export async function readCoreLogWindow(api, params, { signal, preview, previewReady, engines, onPreview, previous }) {
  if (!preview && !previewReady) return readSnapshot(api, params, signal, previous);
  const previewController = new AbortController(), fullController = new AbortController();
  const abort = () => { previewController.abort(); fullController.abort(); };
  signal.addEventListener("abort", abort, { once: true });
  if (signal.aborted) abort();
  const previewParams = new URLSearchParams(params);
  previewParams.set("limit", "200");
  // Briefly let a fast grant check supply a retained preview. Slow metadata
  // must not withhold a fresh, independently authorized preview indefinitely.
  let previewTimer;
  const decision = previewReady ? Promise.race([previewReady, new Promise(resolve => {
    previewTimer = setTimeout(() => resolve(true), 100);
  })]) : null;
  const first = (decision
    ? requestCoreLogJSON(() => decision, "", { signal: previewController.signal })
    : Promise.resolve(true)).then(needed => needed
      ? readSnapshot(api, previewParams, previewController.signal).then(entries => ({ stage: "preview", entries }))
      : { stage: "preview-skipped" })
    .catch(error => ({ stage: "preview-failed", error }));
  const full = readSnapshot(api, params, fullController.signal)
    .then(entries => ({ stage: "full", entries }), error => ({ stage: "full-failed", error }));
  try {
    let result = await Promise.race([first, full]);
    if (result.stage === "full") return result.entries;
    const checkAccess = result => {
      // A failed authorization in either stage invalidates any older preview.
      if ([401, 403, 404].includes(result.error?.status)) throw result.error;
    };
    checkAccess(result);
    // A failed full window is terminal. Waiting for an outstanding preview
    // delays recovery and can turn a late, sparse preview into false success.
    if (result.stage === "full-failed") throw result.error;
    if (result.stage === "preview" && !signal.aborted) {
      const counts = coreLogFilterCounts(result.entries, engines);
      if (Object.values(counts.engine).every(count => count < 200)) return result.entries;
      onPreview(result.entries);
    }
    const complete = await full;
    if (complete.stage === "full-failed") throw complete.error;
    return complete.entries;
  } finally {
    clearTimeout(previewTimer);
    abort();
    signal.removeEventListener("abort", abort);
  }
}
