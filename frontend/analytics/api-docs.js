import { bindPreferenceFields } from "./preferences.js";
import { readRegion } from './loading.js';
import { node, stack, section, table, field, code, muted, badge } from './ui.js';

const groups = [
  ['配置', /\/(?:configs|config-catalogs|templates|access-controls)(?:\/|$)/],
  ['节点与共享', /\/(?:agents|agent-access|agent-directory|ip-quality|system-tcp)(?:\/|$)/],
  ['任务与部署', /\/(?:tasks|deployments)(?:\/|$)/],
  ['证书', /\/certificate/],
  ['流量与日志', /\/(?:traffic|core-log|client-connections|metrics|panel-metrics|overview)/],
  ['客户端与订阅', /\/(?:client-access|substore-sync)/],
  ['设置与用户', /\/(?:settings|preferences|users|audit)/],
];
const groupOf = path => groups.find(([, pattern]) => pattern.test(path))?.[0] || '认证与接入';
const authentication = { public: '公开', session: '会话 / 环境令牌', session_or_api_key: '会话 / API Key', agent_signature: 'Agent 签名' };

export function apiDocsPage(ctx) {
  const results = stack();
  const search = field('搜索', 'api-filter', '', { placeholder: '用途、路径或方法' });
  const category = field('业务', 'api-group', '', { options: [['', '全部业务'], ...groups.map(([name]) => [name, name]), ['认证与接入', '认证与接入']] });
  let specification;
  const resolve = schema => schema?.$ref ? schema.$ref.split('/').slice(1).reduce((value, key) => value?.[key], specification) || {} : schema || {};
  const type = schema => {
    const value = resolve(schema);
    if (value.anyOf) return value.anyOf.map(type).join(' / ');
    return value.type === 'array' ? `${type(value.items)}[]` : value.type || 'object';
  };
  function schemaView(schema) {
    const value = resolve(schema);
    if (!value.properties) return code(JSON.stringify(value, null, 2));
    return table(['字段', '类型', '要求', '约束'], Object.entries(value.properties).map(([name, property]) => [
      name, type(property), (value.required || []).includes(name) ? '必填' : '可选',
      stack(property.description || '', resolve(property).enum?.join(' / ') || '',
        property.default !== undefined ? `默认：${property.default}` : '',
        property.maxLength ? `最多 ${property.maxLength} 字符` : ''),
    ]), { label: '请求字段', paginate: 0 });
  }
  const disclosure = (title, content) => node('details', { className: 'a-disclosure' }, node('summary', {}, title), node('div', { className: 'a-disclosure-body a-stack' }, content));
  function operationView({ path, method, operation }, opened) {
    const policy = operation['x-key-policy'];
    const permissions = policy?.permissions || operation['x-permissions']?.permissions || [];
    const body = operation.requestBody?.content?.['application/json'];
    const parameters = operation.parameters || [];
    const key = `${method} ${path}`;
    const parametersView = parameters.length ? table(['参数', '位置', '类型', '要求'], parameters.map(parameter => [
      stack(parameter.name, muted(parameter.description || '')), parameter.in, type(parameter.schema), parameter.required ? '必填' : '可选',
    ]), { label: '路径与查询参数', paginate: 0 }) : null;
    return node('details', { className: 'a-disclosure', 'data-operation': key, open: opened.has(key) },
      node('summary', {}, node('strong', {}, operation.summary), ' ', badge(method.toUpperCase()), node('div', { className: 'a-muted' }, path)),
      node('div', { className: 'a-disclosure-body a-stack' },
        muted(`${authentication[operation['x-authentication']] || operation['x-authentication']} · ${operation['x-effect'] === 'write' ? '写入 / 管理' : '读取'}`),
        permissions.length ? muted(`${policy?.any || operation['x-permissions']?.any ? '任一权限' : '所需权限'}：${permissions.join('、')}`) : null,
        operation.description ? muted(operation.description) : null,
        parametersView,
        body ? schemaView(body.schema) : null,
        disclosure('示例', code(JSON.stringify({ request: body?.example || { method: method.toUpperCase(), path }, responses: Object.fromEntries(Object.entries(operation.responses).filter(([status]) => status.startsWith('2')).map(([status, response]) => [status, resolve(response).content?.['application/json']?.example || resolve(response).description])) }, null, 2))),
        disclosure('响应与错误', table(['状态码', '结果'], Object.entries(operation.responses).map(([status, response]) => [status, resolve(response).description]), { label: '响应状态', paginate: 0 })),
        disclosure('资源范围', muted(operation['x-resource-scope'])),
      ));
  }
  function draw() {
    if (!specification) return;
    const opened = new Set([...results.querySelectorAll('details[open][data-operation]')].map(element => element.dataset.operation));
    const filter = search.querySelector('input').value.trim().toLowerCase();
    const selected = category.querySelector('select').value;
    const operations = Object.entries(specification.paths).flatMap(([path, methods]) => Object.entries(methods).map(([method, operation]) => ({ path, method, operation })))
      .filter(({ path, method, operation }) => `${method} ${path} ${operation.summary}`.toLowerCase().includes(filter)
        && (!selected || selected === groupOf(path)));
    const categories = [...groups.map(([name]) => name), '认证与接入'];
    const sections = categories.flatMap(name => {
      const matches = operations.filter(item => groupOf(item.path) === name);
      return matches.length ? [section(name, node('div', { className: 'a-card-body a-stack' }, matches.map(item => operationView(item, opened))))] : [];
    });
    results.replaceChildren(...(sections.length ? sections : [muted('没有匹配的接口')]));
  }
  for (const control of [search, category]) control.addEventListener(control === search ? 'input' : 'change', draw);
  bindPreferenceFields(ctx, 'api', [search, category], { restore: draw });
  const read = readRegion(ctx, '接口', signal => ctx.api('/openapi.json', { signal }), value => { specification = value; draw(); }, { clear() { specification = null; results.replaceChildren(); } });
  ctx.onRefresh(read.load); void read.load();
  return stack(node('div', { className: 'a-filterbar' }, node('div', { className: 'a-filter-fields' }, search, category)), read.element, results,
    disclosure('调用约定', stack(
      code('Authorization: Bearer <QCH_API_KEY>'),
      muted('先查询 /api-key 核对授权。资源按真实 ID 和归属校验。'),
      muted('编辑时回传当前并发标识；脱敏占位符只能在原位置保留。409 后重新读取。'),
      muted('任务按返回 ID 查询至终态；超时或响应丢失后先核实结果。401、403 停止操作，429 有限等待。'))));
}
