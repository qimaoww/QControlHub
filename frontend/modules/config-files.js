// Keep JSON tokens verbatim: parsing and stringifying an entire configuration
// through JS numbers would corrupt integers larger than Number.MAX_SAFE_INTEGER.
function members(text, array = false) {
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
const objectText = (map) => `{\n${[...map].map(([key,value]) => `  ${JSON.stringify(key)}: ${value}`).join(",\n")}\n}\n`;

export function splitConfigFiles(engine, content) {
  if (!["xray", "sing-box"].includes(engine)) return [{path: engine === "mihomo" ? "config.yaml" : "config.json", content}];
  const root = members(content);
  const files = [{path:"00-common.json", content:""}];
  for (const key of ["inbounds", "outbounds"]) {
    if (!root.has(key)) continue;
    const entries = members(root.get(key), true);
    entries.forEach((entry, i) => {
      members(entry);
      files.push({path:`${key}/${String(i).padStart(4,"0")}.json`,content:objectText(new Map([[key,`[${entry}]`]]))});
    });
    if (entries.length) root.delete(key);
  }
  files[0].content = objectText(root);
  return files;
}

export function mergeConfigFiles(files) {
  if (!files.length || files.length > 1025 || files[0].path !== "00-common.json") throw new Error("无效配置文件列表");
  const root = members(files[0].content), lists = new Map();
  for (const file of files.slice(1)) {
    const key = file.path.split("/")[0], entries = lists.get(key) || [];
    if (!["inbounds","outbounds"].includes(key) || file.path !== `${key}/${String(entries.length).padStart(4,"0")}.json`)
      throw new Error("无效配置文件路径或顺序");
    const fragment = members(file.content);
    if (fragment.size !== 1 || !fragment.has(key)) throw new Error(`${file.path} 只能包含 ${key}`);
    const values = members(fragment.get(key), true);
    if (values.length !== 1) throw new Error(`${file.path} 必须包含一个入站或出站`);
    members(values[0]);
    entries.push(values[0]); lists.set(key,entries);
  }
  for (const [key, entries] of lists) {
    if (root.has(key)) throw new Error(`${key} 同时存在于公共文件和独立文件中`);
    root.set(key,`[\n${entries.join(",\n")}\n]`);
  }
  const content = objectText(root);
  if (new TextEncoder().encode(content).length > 2097152) throw new Error("合并配置超过 2 MiB 上限");
  return content;
}

export function bindConfigFiles(form, engine, notify) {
  if (!form || !["xray","sing-box"].includes(engine)) return null;
  const editor = form.querySelector("[data-code-editor]"), input = form.querySelector("[data-code-input]");
  if (!editor || !input) return null;
  let files;
  try { files = splitConfigFiles(engine,input.value); } catch { return null; }
  const originals = files.map(file => file.content), readOnly = input.readOnly;
  let selected = 0;
  const select = document.createElement("select");
  select.setAttribute("aria-label","选择入站、出站或公共配置文件");
  for (const [i,file] of files.entries()) {
    const option = document.createElement("option"); option.value = String(i); option.textContent = file.path; select.append(option);
  }
  const preview = document.createElement("option"); preview.value = "preview"; preview.textContent = "合并预览（只读）"; select.append(preview);
  editor.querySelector(".code-file-meta").append(select);
  const save = () => { if (selected !== "preview" && !readOnly) files[selected].content = input.value; };
  const controller = {
    content() { save(); return mergeConfigFiles(files); },
    original() { return selected === "preview" ? input.value : originals[selected]; },
    dirty() { save(); return files.some((file,i) => file.content !== originals[i]); },
    reset() { if (selected !== "preview" && !readOnly) input.value = files[selected].content = originals[selected]; },
  };
  select.addEventListener("change", () => {
    save();
    try {
      const next = select.value === "preview" ? "preview" : Number(select.value);
      const content = next === "preview" ? mergeConfigFiles(files) : files[next].content;
      selected = next; input.value = content; input.readOnly = readOnly || next === "preview";
      input.dispatchEvent(new Event("input", {bubbles:true}));
    } catch (error) { select.value = String(selected); notify(error.message,"error"); }
  });
  input.value = files[0].content;
  editor.configFileController = controller;
  return controller;
}
