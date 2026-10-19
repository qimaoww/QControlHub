// Keep JSON tokens verbatim: parsing and stringifying an entire configuration
// through JS numbers would corrupt integers larger than Number.MAX_SAFE_INTEGER.
export function configJSONMembers(text, array = false) {
  const parsed = JSON.parse(text);
  if (array ? !Array.isArray(parsed) : !parsed || Array.isArray(parsed) || typeof parsed !== "object")
    throw new Error(array ? "入站/出站必须是数组" : "配置文件必须是 JSON 对象");
  const parts = [];
  let depth = 0, quoted = false, escaped = false, start = text.indexOf(array ? "[" : "{") + 1;
  for (let i = start; i < text.length; i++) {
    const c = text[i];
    if (quoted) {
      if (escaped) escaped = false;
      else if (c === "\\") escaped = true;
      else if (c === '"') quoted = false;
      continue;
    }
    if (c === '"') { quoted = true; continue; }
    if (c === "{" || c === "[") depth++;
    else if (c === "}" || c === "]") {
      if (!depth) { if (text.slice(start, i).trim()) parts.push(text.slice(start, i).trim()); break; }
      depth--;
    } else if (c === "," && !depth) { parts.push(text.slice(start, i).trim()); start = i + 1; }
  }
  if (array) return parts;
  const result = new Map();
  for (const part of parts) {
    const match = part.match(/^("(?:[^"\\]|\\.)*")\s*:\s*([\s\S]*)$/);
    if (!match) throw new Error("无效 JSON 字段");
    const key = JSON.parse(match[1]);
    if (result.has(key)) throw new Error(`重复 JSON 字段：${key}`);
    result.set(key, match[2]);
  }
  return result;
}
const members = configJSONMembers;
const objectText = (map) => `{\n${[...map].map(([key,value]) => `  ${JSON.stringify(key)}: ${value}`).join(",\n")}\n}\n`;

// Compare JSON snapshots without rounding large numeric tokens or depending
// on object-key order. Arrays and scalar spellings retain their exact meaning.
export function canonicalConfigJSON(content) {
  const text = String(content).trim();
  JSON.parse(text);
  if (text.startsWith("{"))
    return `{${[...members(text)].sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => `${JSON.stringify(key)}:${canonicalConfigJSON(value)}`).join(",")}}`;
  if (text.startsWith("["))
    return `[${members(text, true).map(canonicalConfigJSON).join(",")}]`;
  return text;
}

function entryFilename(tag, kind, index, used) {
  let base = typeof tag === "string" ? tag.replace(/\.json$/, "") : "";
  base = Array.from(base.replace(/[^\p{L}\p{N}_.-]/gu, "_").replace(/^\.+|\.+$/g, "")).slice(0,48).join("");
  if (!base) base = `${kind.replace(/s$/, "")}-${index + 1}`;
  let name = `${base}.json`;
  for (let suffix = 2; used.has(name); suffix++) name = `${base}-${suffix}.json`;
  used.add(name);
  return name;
}

export function splitConfigFiles(engine, content) {
  if (!["xray", "sing-box"].includes(engine)) return [{path: engine === "mihomo" ? "config.yaml" : "config.json", content}];
  const root = members(content);
  const lists = new Map();
  const ingressKeys = engine === "sing-box" ? ["inbounds", "endpoints"] : ["inbounds"];
  for (const key of [...ingressKeys, "outbounds"]) {
    if (!root.has(key)) continue;
    const entries = members(root.get(key), true);
    entries.forEach(entry => members(entry));
    lists.set(key, entries);
  }
  const listeners = ingressKeys.flatMap(key => (lists.get(key) || []).map((entry, index) => ({key, entry, index})));
  const outbounds = lists.get("outbounds") || [];
  const owners = new Map(), portKey = engine === "xray" ? "port" : "listen_port";
  listeners.forEach(({entry}, i) => {
    const raw = JSON.parse(entry)[portKey];
    const port = typeof raw === "number" || typeof raw === "string" && /^[+]?[0-9]+$/.test(raw.trim()) ? Number(raw) : 0;
    if (Number.isInteger(port) && port > 0 && port <= 65535) owners.set(port, owners.has(port) ? -1 : i);
  });
  const ownerOf = raw => {
    const tag = JSON.parse(raw).tag;
    const match = typeof tag === "string" && tag.match(/^qch-trf-([1-9][0-9]{0,4})-[a-f0-9]{12}$/);
    return match ? owners.get(Number(match[1])) : undefined;
  };
  // Shared/default exits remain first. Pair only an ordered dedicated suffix,
  // so imported routing and default-outbound priority are never changed.
  let cut = outbounds.length, next = listeners.length;
  while (cut > 0) {
    const owner = ownerOf(outbounds[cut - 1]);
    if (owner === undefined || owner < 0 || owner > next) break;
    next = owner; cut--;
  }
  const paired = listeners.map(() => []);
  for (const raw of outbounds.slice(cut)) paired[ownerOf(raw)].push(raw);
  if (cut < outbounds.length) {
    if (cut) root.set("outbounds", `[${outbounds.slice(0, cut).join(",\n")}]`);
    else root.delete("outbounds");
  }
  const files = [{path:"common.json", content:""}], used = new Set();
  listeners.forEach(({key, entry, index}, i) => {
    const fragment = new Map([[key, `[${entry}]`]]);
    if (paired[i].length) fragment.set("outbounds", `[${paired[i].join(",\n")}]`);
    files.push({path:`${key}/${entryFilename(JSON.parse(entry).tag, key, index, used)}`, content:objectText(fragment)});
  });
  for (const key of ingressKeys) if (lists.get(key)?.length) root.delete(key);
  files[0].content = objectText(root);
  return files;
}

export function mergeConfigFiles(files) {
  if (!files.length || files.length > 1025 || !["common.json","00-common.json"].includes(files[0].path)) throw new Error("无效配置文件列表");
  const root = members(files[0].content), lists = new Map();
  const seen = new Set();
  let pairedExits = false, standaloneExits = false;
  for (const file of files.slice(1)) {
    const [key, name, extra] = file.path.split("/"), entries = lists.get(key) || [];
    const validName = files[0].path === "00-common.json"
      ? file.path === `${key}/${String(entries.length).padStart(4,"0")}.json`
      : extra === undefined && typeof name === "string" && new TextEncoder().encode(name).length <= 220 && /^[\p{L}\p{N}_-][\p{L}\p{N}_.-]*\.json$/u.test(name);
    if (!["inbounds","endpoints","outbounds"].includes(key) || !validName || seen.has(file.path))
      throw new Error("无效配置文件路径或顺序");
    seen.add(file.path);
    const fragment = members(file.content);
    if (!fragment.has(key) || [...fragment.keys()].some(field => field !== key && !(key !== "outbounds" && field === "outbounds" && files[0].path === "common.json")))
      throw new Error(`${file.path} 只能包含 ${key}${key !== "outbounds" ? " 和配套 outbounds" : ""}`);
    const values = members(fragment.get(key), true);
    if (values.length !== 1) throw new Error(`${file.path} 必须包含一个入站或出站`);
    members(values[0]);
    entries.push(values[0]); lists.set(key,entries);
    if (key === "outbounds") standaloneExits = true;
    else if (fragment.has("outbounds")) {
      const exits = members(fragment.get("outbounds"), true);
      exits.forEach(exit => members(exit));
      pairedExits = true;
      lists.set("outbounds", [...(lists.get("outbounds") || []), ...exits]);
    }
  }
  if (pairedExits && standaloneExits) throw new Error("不能混用成套入站出口和旧版独立出站文件");
  for (let [key, entries] of lists) {
    if (root.has(key)) {
      if (key !== "outbounds" || !pairedExits) throw new Error(`${key} 同时存在于公共文件和独立文件中`);
      entries = [...members(root.get(key), true), ...entries];
    }
    root.set(key,`[\n${entries.join(",\n")}\n]`);
  }
  const content = objectText(root);
  for (const key of ["inbounds", "endpoints", "outbounds"]) if (root.has(key)) members(root.get(key), true).forEach(entry => members(entry));
  if (new TextEncoder().encode(content).length > 2097152) throw new Error("合并配置超过 2 MiB 上限");
  return content;
}

export function configFileDisplayName(path, content) {
  if (["common.json","00-common.json"].includes(path)) return "公共配置.json";
  const name = path.split("/").at(-1);
  try {
    const key = path.split("/")[0], entry = JSON.parse(content)[key]?.[0];
    const protocol = entry?.type || entry?.protocol;
    if (typeof protocol === "string" && protocol) return `${name} · ${protocol}`;
  } catch { /* Invalid drafts retain their file identity. */ }
  return name;
}
