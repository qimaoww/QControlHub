import { configJSONMembers } from "./config-file-model.js";

const generated = tag => /^qch-(?:trf-|stat-)/.test(tag || "");
const objectText = entries => `{\n${[...entries].map(([key, value]) => `${JSON.stringify(key)}: ${value}`).join(",\n")}\n}`;
const fields = engine => {
  if (engine === "xray") return { routeKey:"routing", matchKey:"inboundTag", targetKey:"outboundTag", actionKey:"type", portKey:"port" };
  if (engine === "sing-box") return { routeKey:"route", matchKey:"inbound", targetKey:"outbound", actionKey:"action", portKey:"listen_port" };
  throw Error("此内核暂不支持出站操作");
};
const exit = (entry, engine) => !["block", "blackhole", "dns"].includes(entry[engine === "xray" ? "protocol" : "type"]);

export function outboundEntries(content) {
  const root = configJSONMembers(content);
  return configJSONMembers(root.get("outbounds") || "[]", true).map((content, index) => {
    configJSONMembers(content);
    return { ...JSON.parse(content), content, index };
  }).filter(entry => !generated(entry.tag));
}

function referencesTo(content, index) {
  const document = JSON.parse(content), previous = document.outbounds[index], references = [];
  document.outbounds.splice(index, 1);
  const referenceKeys = new Set(["outboundTag", "outbound", "outbounds", "detour", "dialerProxy", "final"]);
  const visit = (value, path, reference = false) => {
    if (Array.isArray(value)) value.forEach((entry, i) => visit(entry, `${path}/${i}`, reference));
    else if (value && typeof value === "object") Object.entries(value).forEach(([key, entry]) => {
      visit(entry, `${path}/${key}`, referenceKeys.has(key) || key === "tag" && path.endsWith("/proxySettings"));
    });
    else if (reference && value === previous.tag) references.push(path);
    else if (path.match(/\/balancers\/\d+\/selector\/\d+$/) && typeof value === "string" && previous.tag.startsWith(value)) references.push(path);
  };
  visit(document, "");
  return references;
}

export function mutateOutbound(content, operation, index, fragment, ignoredReferences = []) {
  if (!["add", "modify", "delete"].includes(operation)) throw Error("无效出站操作");
  const root = configJSONMembers(content);
  const entries = configJSONMembers(root.get("outbounds") || "[]", true);
  const previous = operation !== "add" && Number.isInteger(index) && index >= 0 && entries[index] ? JSON.parse(entries[index]) : null;
  if (operation !== "add" && (!previous || generated(previous.tag))) throw Error("请重新选择出站");
  let next;
  if (operation !== "delete") {
    configJSONMembers(fragment);
    next = JSON.parse(fragment);
    if (typeof next.tag !== "string" || !next.tag.trim() || next.tag !== next.tag.trim() || generated(next.tag))
      throw Error("请填写唯一出站标签，不能使用系统统计标签或首尾空格");
    if (entries.some((raw, i) => (operation === "add" || i !== index) && JSON.parse(raw).tag === next.tag))
      throw Error("出站标签已存在");
  }
  if (previous?.tag && (operation === "delete" || next.tag !== previous.tag)) {
    const references = referencesTo(content, index).filter(path => !ignoredReferences.includes(path));
    if (references.length) throw Error(`出站仍被引用：${references.join("、")}。请先调整路由或出站引用。`);
  }
  if (operation === "add") {
    // Keep generated per-inbound exits as an ordered suffix for paired files.
    const firstGenerated = entries.findIndex(raw => generated(JSON.parse(raw).tag));
    entries.splice(firstGenerated < 0 ? entries.length : firstGenerated, 0, fragment);
  } else if (operation === "delete") entries.splice(index, 1);
  else entries[index] = fragment;
  root.set("outbounds", `[${entries.join(",\n")}]`);
  return objectText(root);
}

function bindingState(content, engine, inbound) {
  const keys = fields(engine), root = configJSONMembers(content);
  const sections = engine === "sing-box" ? ["inbounds", "endpoints"] : ["inbounds"];
  const listeners = sections.flatMap(section => configJSONMembers(root.get(section) || "[]", true).map(raw => JSON.parse(raw)));
  if (!inbound?.tag || !Number.isInteger(inbound.port) || inbound.port < 1 || inbound.port > 65535 ||
      listeners.filter(entry => entry.tag === inbound.tag).length !== 1 ||
      !listeners.some(entry => entry.tag === inbound.tag && Number(entry[keys.portKey]) === inbound.port))
    throw Error("入站已变化，请重新选择入站");
  const route = configJSONMembers(root.get(keys.routeKey) || "{}");
  const rules = configJSONMembers(route.get("rules") || "[]", true);
  const plain = rule => Object.keys(rule).every(key => [keys.matchKey, keys.targetKey, keys.actionKey].includes(key)) &&
    (engine === "xray" ? rule.type === "field" : !rule.action || rule.action === "route");
  const matches = value => Array.isArray(value) ? value.length === 1 && value[0] === inbound.tag :
    engine === "sing-box" && value === inbound.tag;
  const indices = rules.flatMap((raw, index) => {
    const rule = JSON.parse(raw);
    return plain(rule) && matches(rule[keys.matchKey]) && typeof rule[keys.targetKey] === "string" &&
      !generated(rule[keys.targetKey]) ? [index] : [];
  });
  if (indices.length > 1) throw Error("当前入站有多条出站绑定，请先在公共配置中整理路由");
  const index = indices[0] ?? -1;
  return { ...keys, root, route, rules, plain, index, tag:index < 0 ? "" : JSON.parse(rules[index])[keys.targetKey] };
}

export function inboundOutbound(content, engine, inbound) {
  return bindingState(content, engine, inbound).tag;
}

export function bindOutbound(content, engine, inbound, tag, remove = false) {
  const { root, route, rules, plain, index, routeKey, matchKey, targetKey } = bindingState(content, engine, inbound);
  if (remove) {
    if (index >= 0) rules.splice(index, 1);
  } else {
    if (!outboundEntries(content).some(entry => entry.tag === tag && exit(entry, engine)))
      throw Error("请选择存在的流量出站，不能绑定系统统计、拦截或 DNS 出站");
    const rule = engine === "xray" ? {type:"field", inboundTag:[inbound.tag], outboundTag:tag} : {inbound:[inbound.tag], outbound:tag};
    if (index >= 0) rules[index] = JSON.stringify(rule);
    else {
      // Conditional policy rules stay ahead of the new fallback. Do not
      // silently override a multi-inbound binding that needs a source review.
      if (rules.some(raw => {
        const value = JSON.parse(raw), tags = value[matchKey];
        return plain(value) && Array.isArray(tags) && tags.length > 1 && tags.includes(inbound.tag);
      })) throw Error("当前入站使用多入站共享路由，请先在公共配置中拆分绑定");
      const fallback = rules.findIndex(raw => {
        const value = JSON.parse(raw);
        return plain(value) && value[matchKey] === undefined && typeof value[targetKey] === "string" && !generated(value[targetKey]);
      });
      if (fallback >= 0 && !outboundEntries(content).some(entry => entry.tag === JSON.parse(rules[fallback])[targetKey] && exit(entry, engine)) ||
          engine === "sing-box" && rules.some(raw => {
            const value = JSON.parse(raw);
            return value.action === "reject" && Object.keys(value).every(key => ["action", "method", "no_drop"].includes(key));
          })) throw Error("已有全局拦截或 DNS 兜底，请先在公共配置中明确调整路由，再绑定出站");
      rules.splice(fallback < 0 ? rules.length : fallback, 0, JSON.stringify(rule));
    }
  }
  route.set("rules", `[${rules.join(",\n")}]`);
  root.set(routeKey, objectText(route));
  return objectText(root);
}

export function changeInboundOutbound(content, engine, inbound, operation, index, fragment) {
  if (operation === "bind") return bindOutbound(content, engine, inbound, fragment);
  const binding = bindingState(content, engine, inbound);
  const ignored = [];
  if (operation !== "add") {
    const entry = outboundEntries(content).find(entry => entry.index === index);
    if (!entry || entry.tag !== binding.tag) throw Error("只能修改或删除当前入站绑定的出站");
    const ownReference = `/${binding.routeKey}/rules/${binding.index}/${binding.targetKey}`;
    const defaultTag = engine === "sing-box" && JSON.parse(content).route?.final || outboundEntries(content)[0]?.tag;
    if (entry.tag === defaultTag || referencesTo(content, index).some(path => path !== ownReference))
      throw Error("此出站是默认或共享出口，不能仅为当前入站修改或删除。请增加专属出站并绑定；共享模板可在公共配置中管理。");
    ignored.push(ownReference);
  }
  const changed = mutateOutbound(content, operation, index, fragment, ignored);
  return bindOutbound(changed, engine, inbound, operation === "delete" ? "" : JSON.parse(fragment).tag, operation === "delete");
}


export { exit as outboundCanBind };
