export class ConfigFormatError extends Error {
  constructor(message) {
    super(message);
    this.name = "ConfigFormatError";
  }
}

const invalidJson = () =>
  new ConfigFormatError(
    "JSON 语法错误（或包含注释/扩展语法），无法安全格式化。",
  );

function isDigit(char) {
  return char >= "0" && char <= "9";
}

function isWhitespace(char) {
  return char === " " || char === "\t" || char === "\n" || char === "\r";
}

function lexJson(text) {
  const tokens = [];
  const length = text.length;
  let cursor = 0;
  while (cursor < length) {
    const char = text[cursor];
    if (isWhitespace(char)) {
      cursor += 1;
      continue;
    }
    if ("{}[]:,".includes(char)) {
      tokens.push({ type: char, raw: char });
      cursor += 1;
      continue;
    }
    if (char === '"') {
      const start = cursor;
      cursor += 1;
      let closed = false;
      while (cursor < length) {
        const current = text[cursor];
        if (current === "\\") {
          if (cursor + 1 >= length) throw invalidJson();
          const escaped = text[cursor + 1];
          if ('"\\/bfnrt'.includes(escaped)) {
            cursor += 2;
            continue;
          }
          if (escaped === "u") {
            if (cursor + 6 > length) throw invalidJson();
            const hex = text.slice(cursor + 2, cursor + 6);
            if (!/^[0-9a-fA-F]{4}$/.test(hex)) throw invalidJson();
            cursor += 6;
            continue;
          }
          throw invalidJson();
        }
        if (current === '"') {
          cursor += 1;
          closed = true;
          break;
        }
        if (current === "\n" || current === "\r") throw invalidJson();
        cursor += 1;
      }
      if (!closed) throw invalidJson();
      tokens.push({ type: "string", raw: text.slice(start, cursor) });
      continue;
    }
    if (char === "-" || isDigit(char)) {
      const start = cursor;
      if (char === "-") {
        cursor += 1;
        if (cursor >= length || !isDigit(text[cursor])) throw invalidJson();
      }
      if (text[cursor] === "0") {
        cursor += 1;
      } else {
        while (cursor < length && isDigit(text[cursor])) cursor += 1;
      }
      if (text[cursor] === ".") {
        cursor += 1;
        if (cursor >= length || !isDigit(text[cursor])) throw invalidJson();
        while (cursor < length && isDigit(text[cursor])) cursor += 1;
      }
      if (text[cursor] === "e" || text[cursor] === "E") {
        cursor += 1;
        if (text[cursor] === "+" || text[cursor] === "-") cursor += 1;
        if (cursor >= length || !isDigit(text[cursor])) throw invalidJson();
        while (cursor < length && isDigit(text[cursor])) cursor += 1;
      }
      tokens.push({ type: "number", raw: text.slice(start, cursor) });
      continue;
    }
    if (text.startsWith("true", cursor)) {
      tokens.push({ type: "literal", raw: "true" });
      cursor += 4;
      continue;
    }
    if (text.startsWith("false", cursor)) {
      tokens.push({ type: "literal", raw: "false" });
      cursor += 5;
      continue;
    }
    if (text.startsWith("null", cursor)) {
      tokens.push({ type: "literal", raw: "null" });
      cursor += 4;
      continue;
    }
    throw invalidJson();
  }
  return tokens;
}

function parseJson(tokens) {
  let position = 0;
  const peek = () => tokens[position];
  const take = () => {
    const token = tokens[position];
    if (!token) throw invalidJson();
    position += 1;
    return token;
  };
  const readString = () => {
    const token = take();
    if (token.type !== "string") throw invalidJson();
    return token;
  };
  const readValue = () => {
    const token = peek();
    if (!token) throw invalidJson();
    if (token.type === "string" || token.type === "number" || token.type === "literal") {
      position += 1;
      return { kind: "scalar", raw: token.raw };
    }
    if (token.type === "{") return readObject();
    if (token.type === "[") return readArray();
    throw invalidJson();
  };
  const readObject = () => {
    take();
    const entries = [];
    const keys = new Set();
    let token = peek();
    while (token && token.type !== "}") {
      const key = readString();
      const keyValue = JSON.parse(key.raw);
      if (keys.has(keyValue))
        throw new ConfigFormatError(
          `JSON 存在重复字段 "${keyValue}"，无法无损格式化。`,
        );
      keys.add(keyValue);
      const colon = take();
      if (colon.type !== ":") throw invalidJson();
      entries.push({ key: key, value: readValue() });
      token = peek();
      if (token && token.type === ",") {
        take();
        token = peek();
        if (token && token.type === "}") throw invalidJson();
        continue;
      }
      if (token && token.type === "}") break;
      throw invalidJson();
    }
    if (!token || token.type !== "}") throw invalidJson();
    take();
    return { kind: "object", entries };
  };
  const readArray = () => {
    take();
    const items = [];
    let token = peek();
    while (token && token.type !== "]") {
      items.push(readValue());
      token = peek();
      if (token && token.type === ",") {
        take();
        token = peek();
        if (token && token.type === "]") throw invalidJson();
        continue;
      }
      if (token && token.type === "]") break;
      throw invalidJson();
    }
    if (!token || token.type !== "]") throw invalidJson();
    take();
    return { kind: "array", items };
  };
  const root = readValue();
  if (position !== tokens.length) throw invalidJson();
  return root;
}

function emit(node, depth) {
  const indent = "  ".repeat(depth);
  if (node.kind === "scalar") return node.raw;
  if (node.kind === "object") {
    if (node.entries.length === 0) return "{}";
    const childIndent = "  ".repeat(depth + 1);
    const lines = node.entries.map(
      (entry) => `${childIndent}${entry.key.raw}: ${emit(entry.value, depth + 1)}`,
    );
    return `{\n${lines.join(",\n")}\n${indent}}`;
  }
  if (node.kind === "array") {
    if (node.items.length === 0) return "[]";
    const childIndent = "  ".repeat(depth + 1);
    const lines = node.items.map(
      (item) => `${childIndent}${emit(item, depth + 1)}`,
    );
    return `[\n${lines.join(",\n")}\n${indent}]`;
  }
  return "";
}

function formatJson(text) {
  const tokens = lexJson(text);
  const root = parseJson(tokens);
  return `${emit(root, 0)}\n`;
}

function formatYaml() {
  throw new ConfigFormatError(
    "当前环境无法无损格式化 Mihomo YAML：缺少可保留注释、锚点和标签的 YAML 解析器，已保留原文。",
  );
}

export function formatConfigContent(text, language) {
  const normalized = (language || "").toUpperCase();
  if (!text.trim()) throw new ConfigFormatError("内容为空，无法格式化。");
  if (normalized === "JSON") return formatJson(text);
  if (normalized === "YAML") return formatYaml();
  throw new ConfigFormatError(`暂不支持格式化 ${language || "未知"} 格式。`);
}
