import { coreLogFilterCounts } from "./core-log-model.js";
import { reuseCoreLogEntries } from "./core-log-projection.js";

// Weak keys add no retained windows beyond the account's active view/LRU.
// A related or partial snapshot is never a validator for a different URL.
const validators = new WeakMap();
async function readSnapshot(api, params, signal, previous) {
  const path = `/core-logs?${params}`;
  const known = previous && validators.get(previous)?.get(path);
  let etag = "";
  const entries = await api(path, {
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

// Start both stages in the same turn. A fast full response wins immediately;
// a slow full response never holds up the preview. Quiet previews are complete.
export async function readCoreLogWindow(api, params, { signal, preview, engines, onPreview, previous }) {
  if (!preview) return readSnapshot(api, params, signal, previous);
  const previewController = new AbortController(), fullController = new AbortController();
  const abort = () => { previewController.abort(); fullController.abort(); };
  signal.addEventListener("abort", abort, { once: true });
  if (signal.aborted) abort();
  const previewParams = new URLSearchParams(params);
  previewParams.set("limit", "200");
  const first = readSnapshot(api, previewParams, previewController.signal)
    .then(entries => ({ stage: "preview", entries }), error => ({ stage: "preview-failed", error }));
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
    let failed = null;
    if (result.stage === "full-failed") {
      failed = result.error;
      result = await first;
      checkAccess(result);
    }
    if (result.stage === "preview" && !signal.aborted) {
      const counts = coreLogFilterCounts(result.entries, engines);
      if (Object.values(counts.engine).every(count => count < 200)) return result.entries;
      onPreview(result.entries);
    }
    const complete = failed ? { stage: "full-failed", error: failed } : await full;
    if (complete.stage === "full-failed") throw complete.error;
    return complete.entries;
  } finally {
    abort();
    signal.removeEventListener("abort", abort);
  }
}
