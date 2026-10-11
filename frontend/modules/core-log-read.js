import { coreLogFilterCounts } from "./core-log-model.js";

// Start both stages in the same turn. A fast full response wins immediately;
// a slow full response never holds up the preview. Quiet previews are complete.
export async function readCoreLogWindow(api, params, { signal, preview, engines, onPreview }) {
  if (!preview) return api(`/core-logs?${params}`, { signal });
  const previewController = new AbortController(), fullController = new AbortController();
  const abort = () => { previewController.abort(); fullController.abort(); };
  signal.addEventListener("abort", abort, { once: true });
  if (signal.aborted) abort();
  const previewParams = new URLSearchParams(params);
  previewParams.set("limit", "200");
  const first = api(`/core-logs?${previewParams}`, { signal: previewController.signal })
    .then(entries => ({ stage: "preview", entries }), () => ({ stage: "preview-failed" }));
  const full = api(`/core-logs?${params}`, { signal: fullController.signal })
    .then(entries => ({ stage: "full", entries }), error => ({ stage: "full-failed", error }));
  try {
    let result = await Promise.race([first, full]);
    if (result.stage === "full") return result.entries;
    let failed = null;
    if (result.stage === "full-failed") {
      failed = result.error;
      // Access failures take precedence over a possibly older authorized read.
      if ([403, 404].includes(failed.status)) throw failed;
      result = await first;
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
