import { configJSONMembers, splitConfigFiles } from "./config-json.js";

const objectText = entries => `{\n${[...entries].map(([key, value]) => `  ${JSON.stringify(key)}: ${value}`).join(",\n")}\n}\n`;
const generated = tag => /^qch-(?:trf-|stat-)/.test(tag || "");

// Ranges retain whitespace and numeric tokens. Updating one section must not
// stringify the rest of the native document, or change default exit priority.
function ranges(text, array) {
  configJSONMembers(text, array);
  const open = text.indexOf(array ? "[" : "{");
  const result = [];
  let start = open + 1, depth = 0, quoted = false, escaped = false, close;
  for (let i = start; i < text.length; i++) {
    const char = text[i];
    if (quoted) {
      if (escaped) escaped = false;
      else if (char === "\\") escaped = true;
      else if (char === '"') quoted = false;
    } else if (char === '"') quoted = true;
    else if (char === "{" || char === "[") depth++;
    else if (char === "}" || char === "]") {
      if (!depth) { close = i; break; }
      depth--;
    } else if (char === "," && !depth) {
      result.push({start, end:i}); start = i + 1;
    }
  }
  if (text.slice(start, close).trim()) result.push({start, end:close});
  return {open, close, parts:result.map(({start, end}) => {
    const raw = text.slice(start, end);
    if (array) return {raw, value:raw.trim()};
    const match = raw.match(/^(\s*("(?:[^"\\]|\\.)*")\s*:\s*)([\s\S]*?)(\s*)$/);
    return {raw, key:JSON.parse(match[2]), prefix:match[1], value:match[3], suffix:match[4]};
  })};
}

export function patchConfigObject(original, values) {
  const {open, close, parts} = ranges(original, false);
  const remaining = new Map(values);
  const next = [];
  for (const part of parts) {
    if (!remaining.has(part.key)) continue;
    const value = remaining.get(part.key);
    next.push(value === part.value ? part.raw : part.prefix + value + part.suffix);
    remaining.delete(part.key);
  }
  for (const [key, value] of remaining) next.push(`\n  ${JSON.stringify(key)}: ${value}\n`);
  return original.slice(0, open + 1) + next.join(",") + original.slice(close);
}

function patchArray(original, entries) {
  if (!original) return `[\n${entries.join(",\n")}\n]`;
  const {open, close, parts} = ranges(original, true);
  if (parts.length === entries.length && parts.every((part, i) => part.value === entries[i])) return original;
  const next = entries.map((value, i) => {
    const same = parts.find(part => part.value === value);
    if (same) return same.raw;
    const prior = parts[i];
    return prior ? prior.raw.slice(0, prior.raw.indexOf(prior.value)) + value + prior.raw.slice(prior.raw.indexOf(prior.value) + prior.value.length) : `\n${value}\n`;
  });
  return original.slice(0, open + 1) + next.join(",") + original.slice(close);
}

function renameReferences(content, engine, renames) {
  const mappings = new Map(["inbound", "outbound"].map(kind => [kind,
    new Map(renames.filter(item => item.kind === kind).map(item => [item.previous, item.next]))]));
  const reference = (path, kind) => {
    const names = path.filter(part => typeof part === "string"), key = names.at(-1);
    if (kind === "inbound") return engine === "xray" ? names[0] === "routing" && key === "inboundTag"
      : names[0] === "route" && key === "inbound" || names.join("/") === "experimental/v2ray_api/stats/inbounds";
    if (engine === "xray") return names[0] === "routing" && ["outboundTag", "selector"].includes(key) ||
      names.join("/") === "outbounds/proxySettings/tag" ||
      ["inbounds", "outbounds"].includes(names[0]) && names.slice(1).join("/") === "streamSettings/sockopt/dialerProxy";
    return names[0] === "route" && ["outbound", "final"].includes(key) ||
      ["inbounds", "outbounds", "endpoints"].includes(names[0]) && names.slice(1).join("/") === "detour" ||
      names.join("/") === "outbounds/outbounds" || names.join("/") === "experimental/v2ray_api/stats/outbounds";
  };
  const visit = (text, path) => {
    const raw = text.trim();
    if (raw.startsWith("{")) return patchConfigObject(text, new Map([...configJSONMembers(text)].map(([key, value]) => [key, visit(value, [...path, key])])));
    if (raw.startsWith("[")) return patchArray(text, configJSONMembers(text, true).map((value, index) => visit(value, [...path, index])));
    const value = JSON.parse(text);
    for (const [kind, mapping] of mappings) if (reference(path, kind) && mapping.has(value)) return JSON.stringify(mapping.get(value));
    return text;
  };
  return visit(content, []);
}

// Complete source and section views share one document. Invalid input stays
// editable in its original view; leaving that view never substitutes a template.
export function createConfigDocument(engine, content, options = {}) {
  const baseline = String(content ?? "");
  let source = baseline, files = [], serial = 0, selected = options.selection || "common", lastSelection = "common", parseError;
  const listeners = new Set(), originals = new Map();
  const segmented = ["xray", "sing-box"].includes(engine);
  let nativeInbounds = options.inbounds || [];
  const notify = () => listeners.forEach(callback => callback());
  const entryOf = file => {
    const key = file.path.split("/")[0];
    try { return JSON.parse(file.content)[key]?.[0] || {}; } catch { return {}; }
  };
  const load = (text, ids) => {
    let next;
    try {
      next = segmented ? splitConfigFiles(engine, text) : [{path:engine === "mihomo" ? "config.yaml" : "config.json", content:text}];
      if (segmented) {
        const common = configJSONMembers(next[0].content);
        const exits = configJSONMembers(common.get("outbounds") || "[]", true);
        for (const key of ["inbounds", "endpoints", "outbounds"]) common.delete(key);
        next[0].content = objectText(common);
        exits.forEach((entry, index) => next.push({path:`outbounds/${index}.json`, content:objectText(new Map([["outbounds", `[${entry}]`]]))}));
      }
    } catch (error) { parseError = error; return false; }
    const used = new Set();
    const previous = files;
    const kindOf = (file, index) => !index ? "common" : file.path.startsWith("outbounds/") ? "outbound" : "inbound";
    const nextTags = new Set(next.map((file, index) => `${kindOf(file, index)}:${entryOf(file).tag || ""}`));
    const existingIds = new Set([...previous, ...(ids || [])].map(file => file.id));
    files = next.map((file, index) => {
      const kind = kindOf(file, index);
      const entry = entryOf(file);
      const prior = previous.find(item => !used.has(item.id) && item.kind === kind &&
        (kind === "common" || entry.tag && entryOf(item).tag === entry.tag)) || previous.find(item =>
        !used.has(item.id) && item.kind === kind && (item.path === file.path || item === previous[index]) && !nextTags.has(`${kind}:${entryOf(item).tag || ""}`));
      let id = ids?.find(item => item.path === file.path || item.kind === kind && item.tag && item.tag === entry.tag)?.id || prior?.id;
      if (!id) {
        do { id = !index ? "common" : `object-${++serial}`; } while (existingIds.has(id));
      }
      existingIds.add(id);
      used.add(id);
      if (!originals.has(id)) originals.set(id, file.content);
      return {...file, id, kind, base:file.content, original:originals.get(id)};
    });
    parseError = null;
    return true;
  };
  load(baseline, options.ids);
  if (typeof options.source === "string") { source = options.source; load(source, options.ids); }
  if (!files.length) files = [{id:"common", kind:"common", path:"config.json", content:source, base:source, original:baseline}];
  for (const draft of options.drafts || []) {
    const file = files.find(file => file.id === draft.id && file.path === draft.path);
    if (file && typeof draft.content === "string") file.content = draft.content;
  }
  const descriptor = file => {
    if (file.kind === "common") return {...file, title:segmented ? "公共配置" : "完整源码", detail:segmented ? "路由、DNS 与其他公共项" : "原生配置", dirty:file.content !== file.original};
    const entry = entryOf(file);
    return {...file, title:entry.tag || file.path.split("/").at(-1), detail:[entry.protocol || entry.type, entry.port ?? entry.listen_port].filter(value => value !== undefined).join(" · "), tag:entry.tag, port:Number(entry.port ?? entry.listen_port), system:generated(entry.tag), dirty:file.content !== file.original};
  };
  const merged = () => {
    if (!segmented) return source;
    if (parseError) throw parseError;
    if (files.every(file => file.content === file.base)) return source;
    const root = configJSONMembers(source), common = configJSONMembers(files[0].content);
    const structural = engine === "sing-box" ? ["inbounds", "endpoints", "outbounds"] : ["inbounds", "outbounds"];
    if (structural.some(key => common.has(key))) throw Error("入站与出站请在对应对象或完整源码中编辑。");
    const next = new Map(root);
    for (const key of root.keys()) if (!structural.includes(key) && !common.has(key)) next.delete(key);
    for (const [key, value] of common) next.set(key, value);
    const collections = new Map(structural.map(key => [key, []]));
    for (const file of files.slice(1)) {
      const key = file.path.split("/")[0], fragment = configJSONMembers(file.content);
      if (!fragment.has(key) || [...fragment.keys()].some(field => field !== key && !(key !== "outbounds" && field === "outbounds"))) throw Error(`${file.path} 包含不属于此对象的字段`);
      const entries = configJSONMembers(fragment.get(key), true);
      if (entries.length !== 1) throw Error(`${file.path} 必须包含一个配置对象；批量调整请编辑完整源码`);
      entries.forEach(entry => configJSONMembers(entry));
      collections.get(key).push(...entries);
    }
    for (const file of files.filter(file => file.kind === "inbound")) {
      const fragment = configJSONMembers(file.content);
      if (fragment.has("outbounds")) collections.get("outbounds").push(...configJSONMembers(fragment.get("outbounds"), true));
    }
    for (const [key, entries] of collections) if (root.has(key) || entries.length) next.set(key, patchArray(root.get(key), entries));
    const renames = [];
    for (const file of files.slice(1)) {
      const key = file.path.split("/")[0];
      const previous = JSON.parse(file.base)[key]?.[0]?.tag, renamed = entryOf(file).tag;
      if (!previous || previous === renamed) continue;
      if (!renamed) throw Error("请为对象保留有效标签；移除标签时请在完整源码中同步调整引用。");
      if (files.some(other => other !== file && other.kind === file.kind && entryOf(other).tag === renamed))
        throw Error(`标签“${renamed}”已被其他对象使用，请使用不同标签。`);
      renames.push({kind:file.kind, previous, next:renamed});
    }
    const result = patchConfigObject(source, next);
    return renames.length ? renameReferences(result, engine, renames) : result;
  };
  const has = id => files.some(file => file.id === id) || ["source", "preview"].includes(id) || !segmented && nativeInbounds.some(entry => `native:${entry.tag}:${entry.port}` === id);
  if (!has(selected) || parseError) selected = "source";
  lastSelection = selected === "preview" ? "common" : selected;
  const model = {
    engine, baseline, segmented,
    subscribe(callback) { listeners.add(callback); return () => listeners.delete(callback); },
    objects: () => files.map(descriptor),
    inbounds: () => nativeInbounds,
    updateInbounds(values) {
      nativeInbounds = values || [];
      if (!segmented && selected.startsWith("native:") && !has(selected)) selected = "common";
      notify();
    },
    selected: () => selected,
    current: () => files.find(file => file.id === selected),
    selectedCommon: () => selected === "common",
    selectedInbound() {
      const file = model.current();
      if (file?.kind === "inbound") return descriptor(file);
      return !segmented ? nativeInbounds.find(entry => `native:${entry.tag}:${entry.port}` === selected) || null : null;
    },
    select(id) {
      if (id === selected || !has(id)) return false;
      if (id !== "source" || !parseError) {
        source = merged();
        if (segmented) load(source, files.map(({id, path}) => ({id, path})));
      }
      if (parseError && !["source", "preview"].includes(id)) throw Error(`请先修正完整源码：${parseError.message}`);
      selected = id;
      if (id !== "preview") lastSelection = id;
      notify(); return true;
    },
    selectInbound(tag, port) {
      const file = files.map(descriptor).find(file => file.kind === "inbound" && file.tag === tag && file.port === Number(port));
      return model.select(file?.id || `native:${tag}:${port}`);
    },
    returnFromPreview: () => model.select(lastSelection),
    text() { return ["source", "preview"].includes(selected) || !segmented ? source : (model.current() || files[0]).content; },
    edit(value) {
      if (selected === "preview") return;
      if (selected === "source" || !segmented) {
        source = value;
        load(source);
      } else (model.current() || files[0]).content = value;
      notify();
    },
    replace(value) { source = value; load(source); if (!segmented) nativeInbounds = []; selected = segmented ? "source" : "common"; lastSelection = selected; notify(); },
    dirty() {
      if (source === baseline && files.every(file => file.content === file.base)) return false;
      try { return merged() !== baseline; } catch { return true; }
    },
    currentDirty: () => selected !== "preview" && (["source"].includes(selected) || !segmented ? source !== baseline : (model.current() || files[0]).content !== (model.current() || files[0]).original),
    original: () => ["source", "preview"].includes(selected) || !segmented ? baseline : (model.current() || files[0]).original,
    reset() {
      if (selected === "preview") return;
      if (selected === "source" || !segmented) { source = baseline; load(source); }
      else { const file = model.current() || files[0]; file.content = file.original; }
      notify();
    },
    snapshot() {
      let complete;
      try { complete = merged(); } catch { /* Keep invalid section drafts next to their last complete source. */ }
      return {selection:selected, source:complete ?? source, ids:files.map(file => ({id:file.id, path:file.path, kind:file.kind, tag:entryOf(file).tag})),
        drafts:complete === undefined ? files.filter(file => file.content !== file.base).map(({id, path, content}) => ({id, path, content})) : []};
    },
    content() {
      const result = merged();
      if (new TextEncoder().encode(result).length > 2097152) throw Error("完整配置超过 2 MiB 上限");
      if (engine !== "mihomo") configJSONMembers(result);
      return result;
    },
  };
  return model;
}
