// Reads are scoped to an immutable workspace revision. Weak keys release
// credential-bearing fragments when that workspace is no longer used.
export function createPresetReads() {
  const workspaces = new WeakMap();
  return (workspace, path, read) => {
    let cache = workspaces.get(workspace);
    if (!cache) workspaces.set(workspace, cache = new Map());
    if (cache.has(path)) return cache.get(path);
    const entry = {};
    cache.set(path, entry);
    entry.promise = Promise.resolve().then(read).then(value => {
      entry.value = value;
      return value;
    }).catch(error => {
      if (cache.get(path) === entry) cache.delete(path);
      throw error;
    });
    // A superseded render may never attach its field consumer.
    void entry.promise.catch(() => {});
    return entry;
  };
}

// Render protocol-specific controls before reconciliation, so they take part
// in focus/draft preservation instead of being removed and appended again.
export function renderPresetIdentity(markup, options, forwarding = "") {
  const start = markup.indexOf('<section class="builder-section" id="identity">');
  const end = markup.indexOf("</section>", start);
  if (start < 0 || end < 0) return markup;
  const section = forwarding || markup.slice(start, end).replace(/<\/div>$/, () => `${options}</div>`) + "</section>";
  markup = markup.slice(0, start) + section + markup.slice(end + "</section>".length);
  if (forwarding) markup = markup.replace('href="#identity" data-builder-step="identity"><b>02</b><strong>认证</strong>',
    'href="#target" data-builder-step="target"><b>02</b><strong>目标</strong>');
  return markup;
}
