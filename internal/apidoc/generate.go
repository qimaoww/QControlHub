// Package apidoc builds the shipped API contract from route registrations,
// request models and the reviewed API Key policy. No server source is needed
// to serve the resulting document.
package apidoc

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type object = map[string]any
type model struct {
	expr ast.Expr
	pkg  string
}
type function struct {
	decl *ast.FuncDecl
	pkg  string
}
type generator struct {
	models    map[string]model
	funcs     map[string]function
	constants map[string]string
	schemas   object
	files     []*ast.File
}

func Generate(root string) ([]byte, error) {
	g := &generator{models: map[string]model{}, funcs: map[string]function{}, constants: map[string]string{}, schemas: object{}}
	for _, pkg := range []string{"core", "serverconfig", "notify", "certificates", "store", "api"} {
		entries, err := os.ReadDir(filepath.Join(root, "internal", pkg))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "internal", pkg, entry.Name()), nil, 0)
			if err != nil {
				return nil, err
			}
			if pkg == "api" {
				g.files = append(g.files, f)
			}
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					g.funcs[pkg+"."+d.Name.Name] = function{d, pkg}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							g.models[pkg+"."+s.Name.Name] = model{s.Type, pkg}
						case *ast.ValueSpec:
							for i, name := range s.Names {
								if i < len(s.Values) {
									if value, ok := literal(s.Values[i]); ok {
										g.constants[pkg+"."+name.Name] = value
									}
								}
							}
						}
					}
				}
			}
		}
	}
	policies := g.policies()
	paths := object{}
	for _, f := range g.files {
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := c.Fun.(*ast.SelectorExpr)
			if !ok || (fn.Sel.Name != "Handle" && fn.Sel.Name != "HandleFunc") || len(c.Args) < 2 {
				return true
			}
			pattern, ok := literal(c.Args[0])
			if !ok {
				return true
			}
			g.addRoute(paths, pattern, c.Args[1], policies)
			return true
		})
		// Certificate routes share a wrapper and use a literal dispatch table.
		ast.Inspect(f, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			pattern, ok := literal(kv.Key)
			if !ok || !strings.Contains(pattern, " /api/v1/certificate") {
				return true
			}
			if _, ok := kv.Value.(*ast.SelectorExpr); ok {
				g.addRoute(paths, pattern, kv.Value, policies)
			}
			return true
		})
	}
	document := object{"openapi": "3.1.0", "info": object{"title": "QControlHub API", "version": "v1", "description": "正式业务 API。有效范围取 Key 模式、显式权限、创建者当前权限、接口策略和资源授权的交集。"}, "servers": []any{object{"url": "/"}}, "paths": paths, "components": object{"schemas": g.schemas, "responses": commonResponses(), "securitySchemes": object{"apiKey": object{"type": "http", "scheme": "bearer", "bearerFormat": "qch_key_*"}, "environmentToken": object{"type": "http", "scheme": "bearer", "description": "环境凭据；独立于委托 API Key，不能自动替换使用。"}, "session": object{"type": "apiKey", "in": "cookie", "name": "qcontrolhub_api_session_dev", "description": "浏览器会话；写操作还需 X-QControlHub-CSRF，HTTPS Cookie 为 __Host-qcontrolhub_api_session；开发 HTTP 为 qcontrolhub_api_session_dev。"}, "agentSignature": object{"type": "apiKey", "in": "header", "name": "X-QControlHub-Signature", "description": "Agent ID、时间戳、nonce 及 Ed25519 请求签名；Key 无法替代。"}}}}
	b, err := json.MarshalIndent(document, "", "  ")
	return append(b, '\n'), err
}
func literal(e ast.Expr) (string, bool) {
	b, ok := e.(*ast.BasicLit)
	if !ok {
		return "", false
	}
	if b.Kind == token.STRING {
		v, err := strconv.Unquote(b.Value)
		return v, err == nil
	}
	return b.Value, true
}
func source(e ast.Node) string {
	var b bytes.Buffer
	_ = format.Node(&b, token.NewFileSet(), e)
	return b.String()
}
func (g *generator) permission(e ast.Expr) string {
	return g.constants[strings.ReplaceAll(source(e), "core.", "core.")]
}
func (g *generator) policies() map[string]object {
	result := map[string]object{}
	for _, f := range g.files {
		ast.Inspect(f, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "apiKeyPolicies" || len(vs.Values) != 1 {
				return true
			}
			list, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok {
				return false
			}
			for _, elt := range list.Elts {
				kv := elt.(*ast.KeyValueExpr)
				pattern, _ := literal(kv.Key)
				p := object{"write": false, "any": false, "permissions": []string{}}
				for _, field := range kv.Value.(*ast.CompositeLit).Elts {
					field := field.(*ast.KeyValueExpr)
					name := field.Key.(*ast.Ident).Name
					if name == "Permissions" {
						ps := []string{}
						for _, v := range field.Value.(*ast.CompositeLit).Elts {
							ps = append(ps, g.permission(v))
						}
						p["permissions"] = ps
					} else if name == "Write" {
						p["write"] = source(field.Value) == "true"
					} else if name == "Any" {
						p["any"] = source(field.Value) == "true"
					}
				}
				result[pattern] = p
			}
			return false
		})
	}
	return result
}
func (g *generator) addRoute(paths object, pattern string, expr ast.Expr, policies map[string]object) {
	parts := strings.SplitN(pattern, " ", 2)
	if len(parts) != 2 {
		return
	}
	method, path := strings.ToLower(parts[0]), parts[1]
	handler := ""
	required := []string{}
	anyOf := false
	agent := false
	authenticated := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := s.X.(*ast.Ident); ok && x.Name == "s" {
				switch s.Sel.Name {
				case "requirePermission", "requireAllPermissions", "requireAnyPermission", "requireRole":
					authenticated = true
					if s.Sel.Name == "requireAnyPermission" {
						anyOf = true
					}
				case "agent":
					agent = true
				case "subStoreMutation":
				default:
					handler = s.Sel.Name
				}
			}
			if p := g.permission(s); p != "" {
				required = append(required, p)
			}
		}
		return true
	})
	policy, keyAllowed := policies[pattern]
	security := []any{}
	auth := "public"
	if agent {
		auth = "agent_signature"
		security = append(security, object{"agentSignature": []string{}})
	} else if authenticated || strings.Contains(path, "certificate") || strings.Contains(path, "settings/api-keys") {
		auth = "session"
		security = append(security, object{"session": []string{}}, object{"environmentToken": []string{}})
	}
	if keyAllowed {
		auth = "session_or_api_key"
		security = append(security, object{"apiKey": []string{}})
	}
	op := object{"operationId": handler + "_" + strings.ReplaceAll(strings.Trim(path, "/"), "/", "_"), "summary": summarize(handler), "security": security, "x-authentication": auth, "x-api-key": keyAllowed, "x-permissions": object{"any": anyOf, "permissions": required}, "x-resource-scope": "工作区绑定创建者；配置、任务、证书按真实 owner_id 隔离。节点管理不授予他人配置或证书。共享实例须使用真实实例 ID，引用和批量目标逐项校验。", "x-effect": "read", "responses": object{}}
	if keyAllowed {
		op["x-key-policy"] = policy
		if policy["write"] == true {
			op["x-effect"] = "write"
		}
	}
	if !keyAllowed && method != "get" && method != "head" {
		op["x-effect"] = "write"
	}
	params := []any{}
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, "{") {
			params = append(params, object{"name": strings.Trim(segment, "{}"), "in": "path", "required": true, "schema": object{"type": "string", "minLength": 1}, "description": "实际资源标识，按 URL 编码；不能以名称代替 ID。"})
		}
	}
	response := op["responses"].(object)
	addQuery := func(key string) {
		for _, parameter := range params {
			p := parameter.(object)
			if p["in"] == "query" && p["name"] == key {
				return
			}
		}
		params = append(params, object{"name": key, "in": "query", "required": false, "schema": querySchema(path, key), "description": queryDescription(path, key)})
	}
	// Resolve request bodies and direct query reads from the real handler chain.
	seen := map[string]bool{}
	var inspect func(string, int)
	inspect = func(name string, depth int) {
		if depth > 3 || seen[name] {
			return
		}
		seen[name] = true
		fn, ok := g.funcs["api."+name]
		if !ok {
			return
		}
		vars := map[string]model{}
		queryVariables := map[string]bool{}
		if fn.decl.Type.Params != nil {
			for _, parameter := range fn.decl.Type.Params.List {
				if source(parameter.Type) == "url.Values" {
					for _, name := range parameter.Names {
						queryVariables[name.Name] = true
					}
				}
			}
		}
		isQuery := func(value string) bool { return strings.Contains(value, "Query()") || queryVariables[value] }
		ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				for _, name := range n.Names {
					vars[name.Name] = model{n.Type, "api"}
				}
			case *ast.AssignStmt:
				if len(n.Rhs) == 1 && strings.HasSuffix(source(n.Rhs[0]), ".Query()") {
					for _, lhs := range n.Lhs {
						queryVariables[source(lhs)] = true
					}
				}
				if len(n.Rhs) == 1 {
					if call, ok := n.Rhs[0].(*ast.CallExpr); ok {
						name := source(call.Fun)
						pkg := "api"
						if strings.HasPrefix(name, "s.store.") {
							pkg = "store"
						}
						parts := strings.Split(name, ".")
						if fn, ok := g.funcs[pkg+"."+parts[len(parts)-1]]; ok && fn.decl.Type.Results != nil {
							for i, field := range fn.decl.Type.Results.List {
								if i < len(n.Lhs) {
									if id, ok := n.Lhs[i].(*ast.Ident); ok {
										vars[id.Name] = model{field.Type, pkg}
									}
								}
							}
						}
					}
				}
			case *ast.IndexExpr:
				if isQuery(source(n.X)) {
					if key, ok := literal(n.Index); ok {
						addQuery(key)
					}
				}
			case *ast.RangeStmt:
				if n.Key == nil {
					break
				}
				usesQuery := false
				ast.Inspect(n.Body, func(child ast.Node) bool {
					if call, ok := child.(*ast.CallExpr); ok && len(call.Args) == 1 && source(call.Args[0]) == source(n.Key) {
						if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Get" && isQuery(source(selector.X)) {
							usesQuery = true
						}
					}
					return true
				})
				if values, ok := n.X.(*ast.CompositeLit); ok && usesQuery {
					for _, element := range values.Elts {
						if pair, ok := element.(*ast.KeyValueExpr); ok {
							if key, ok := literal(pair.Key); ok {
								addQuery(key)
							}
						}
					}
				}
			case *ast.CallExpr:
				call := source(n.Fun)
				if call == "decodeJSON" && len(n.Args) > 2 {
					if unary, ok := n.Args[2].(*ast.UnaryExpr); ok {
						if id, ok := unary.X.(*ast.Ident); ok && vars[id.Name].expr != nil {
							op["requestBody"] = object{"required": true, "content": object{"application/json": object{"schema": g.schema(vars[id.Name].expr, vars[id.Name].pkg)}}}
						}
					}
				}
				if selector, ok := n.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Get" && len(n.Args) == 1 && isQuery(source(selector.X)) {
					if key, ok := literal(n.Args[0]); ok {
						addQuery(key)
					}
				}
				if call == "writeJSON" && len(n.Args) == 3 {
					status := g.status(n.Args[1])
					if status != "" {
						response[status] = object{"description": "业务结果；异步受理须继续查询任务或资源状态。", "content": object{"application/json": object{"schema": g.infer(n.Args[2], "api", vars)}}}
					}
				}
				if _, ok := g.funcs["api."+call]; ok {
					inspect(call, depth+1)
				}
				if strings.HasPrefix(call, "s.") {
					other := strings.TrimPrefix(call, "s.")
					if other != handler {
						inspect(other, depth+1)
					}
				}
			}
			return true
		})
	}
	inspect(handler, 0)
	g.annotate(op, handler, method, path)
	if len(params) > 0 {
		op["parameters"] = params
	}
	hasSuccess := false
	for status := range response {
		hasSuccess = hasSuccess || strings.HasPrefix(status, "2")
	}
	if !hasSuccess {
		response["200"] = object{"description": "成功；无内容响应为 204。"}
	}
	for _, status := range []string{"400", "401", "403", "404", "409", "429", "500"} {
		response[status] = object{"$ref": "#/components/responses/error" + status}
	}
	for status, raw := range response {
		if strings.HasPrefix(status, "2") {
			r := raw.(object)
			if content, ok := r["content"].(object); ok {
				for _, rawMedia := range content {
					media := rawMedia.(object)
					if schema, ok := media["schema"].(object); ok {
						media["example"] = g.example(schema, 0)
					}
				}
			}
		}
	}
	if body, ok := op["requestBody"].(object); ok {
		media := body["content"].(object)["application/json"].(object)
		media["example"] = g.example(media["schema"].(object), 0)
	}

	if path == "/api/v1/tasks" && method == "post" {
		op["description"] = "创建任务；受理不代表完成。按返回 id 查询 GET /api/v1/tasks/{id}，pending/running 等待，succeeded/failed/canceled 终止；结果不确定先查询，禁止盲目重复提交。动作额外需要权限：host lifecycle / Agent 动作需要 agents.manage；read-config/read-managed-config 需要 agent-config.read；deploy/import-existing 需要 agent-config.write。"
	}
	item, _ := paths[path].(object)
	if item == nil {
		item = object{}
		paths[path] = item
	}
	item[method] = op
}
func (g *generator) status(expr ast.Expr) string {
	v := source(expr)
	if n, err := strconv.Atoi(v); err == nil {
		return strconv.Itoa(n)
	}
	for n := 100; n <= 599; n++ {
		if v == "http.Status"+strings.ReplaceAll(http.StatusText(n), " ", "") {
			return strconv.Itoa(n)
		}
	}
	return ""
}
func summarize(name string) string {
	if value, ok := operationNames[name]; ok {
		return value
	}
	for _, prefix := range []string{"get", "list", "create", "put", "update", "delete", "save", "cancel", "retry", "restore", "preview", "test", "run", "check", "grant", "revoke", "respond", "set", "sync", "replace", "renew", "continue", "export", "apply"} {
		if strings.HasPrefix(name, prefix) {
			return map[string]string{"get": "查询", "list": "列出", "create": "创建", "put": "保存", "update": "更新", "delete": "删除", "save": "保存", "cancel": "取消", "retry": "重试", "restore": "恢复", "preview": "预览", "test": "测试", "run": "执行", "check": "检查", "grant": "授权", "revoke": "撤销", "respond": "回应", "set": "设置", "sync": "同步", "replace": "替换", "renew": "续期", "continue": "继续", "export": "导出", "apply": "应用"}[prefix] + translateResource(strings.TrimPrefix(name, prefix))
		}
	}
	return name
}
func querySchema(path, key string) object {
	s := object{"type": "string"}
	switch key {
	case "limit":
		s = object{"type": "integer", "minimum": 1}
		if path == "/api/v1/tasks" {
			s["default"] = 100
			s["maximum"] = 500
		}
		if path == "/api/v1/audit" {
			s["default"] = 50
			s["maximum"] = 200
		}
		if path == "/api/v1/core-logs" {
			s["default"], s["maximum"] = 1000, 2000
		}
		if path == "/api/v1/client-connections" {
			s["default"], s["maximum"] = 100, 200
		}
		if strings.HasPrefix(path, "/api/v1/metrics/") {
			s["default"], s["maximum"] = 1500, 5000
		}
		if strings.HasSuffix(path, "/revisions") {
			s["default"], s["maximum"] = 20, 100
		}
	case "before":
		s = object{"type": "integer", "minimum": 1}
	case "port":
		s = object{"type": "integer", "minimum": 1, "maximum": 65535}
	case "node_order":
		s = object{"type": "array", "items": object{"type": "string", "minLength": 1, "maxLength": 100}, "maxItems": 500}
	case "scope_only", "sync", "include_non_public", "timeline":
		s = object{"type": "boolean"}
		if key == "scope_only" || key == "sync" {
			s["enum"] = []bool{true}
		}
		if key == "include_non_public" {
			s["default"] = false
		}
		if key == "timeline" {
			s["default"] = true
		}
	case "locations":
		s["enum"] = []string{"cached", "only"}
	case "group_by":
		s["enum"] = []string{"ip"}
	case "bucket":
		s["enum"], s["default"] = []string{"minute", "hour", "day"}, "hour"
	case "transport":
		s["enum"] = []string{"tcp", "udp"}
	case "level":
		s["enum"] = []string{"debug", "info", "warning", "error", "critical"}
	case "status":
		s["enum"] = []string{"pending", "running", "succeeded", "failed", "canceled"}
	case "engine":
		s["enum"] = []string{"mihomo", "xray", "sing-box", "ss-rust"}
	case "since", "until":
		s["format"] = "date-time"
	}
	return s
}
func queryDescription(path, key string) string {
	if path == "/api/v1/client-connections" {
		switch key {
		case "since":
			return "RFC3339；默认最近 24 小时；until 必须晚于 since，窗口最多 32 天。"
		case "until":
			return "RFC3339；默认当前时间；与 since 的窗口最多 32 天。"
		case "node_order":
			return "重复 node_order 参数传入真实节点 ID 数组；仅影响同一授权范围内的排序。"
		case "cursor":
			return "仅 group_by=ip 可用；使用响应 next_cursor 并保留筛选、目标和排序。"
		case "before":
			return "普通记录按正整数 ID 向前分页；group_by=ip 使用 cursor，不能带 before。"
		case "sync":
			return "显式 true 请求现有同步流程；即使 GET 仍按管理行为授权。"
		}
	}
	if key == "instance_id" {
		return "main 或实际 shr_... 实例；不得猜测或混用用户实例。"
	}
	if key == "cursor" || key == "before" {
		return "按响应游标翻页，保持原筛选和资源目标。"
	}
	if key == "limit" {
		return "结果数量；各接口按约束限制，非页码。"
	}
	return "可选筛选；时间使用带时区 RFC3339，+ 须 URL 编码。"
}
func (g *generator) schema(expr ast.Expr, pkg string) object {
	switch e := expr.(type) {
	case *ast.StarExpr:
		s := g.schema(e.X, pkg)
		return object{"anyOf": []any{s, object{"type": "null"}}}
	case *ast.ArrayType:
		return object{"type": "array", "items": g.schema(e.Elt, pkg)}
	case *ast.MapType:
		return object{"type": "object", "additionalProperties": g.schema(e.Value, pkg)}
	case *ast.InterfaceType:
		return object{}
	case *ast.SelectorExpr:
		if source(e) == "time.Time" {
			return object{"type": "string", "format": "date-time"}
		}
		return g.named(source(e))
	case *ast.Ident:
		switch e.Name {
		case "string":
			return object{"type": "string"}
		case "bool":
			return object{"type": "boolean"}
		case "int", "int64", "uint64", "uint16", "int32":
			return object{"type": "integer"}
		case "float32", "float64":
			return object{"type": "number"}
		case "any":
			return object{}
		}
		return g.named(pkg + "." + e.Name)
	case *ast.StructType:
		props := object{}
		for _, f := range e.Fields.List {
			if len(f.Names) == 0 {
				embedded := g.schema(f.Type, pkg)
				if ref, ok := embedded["$ref"].(string); ok {
					if m, ok := g.schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(object); ok {
						for k, v := range m["properties"].(object) {
							props[k] = v
						}
					}
				}
				continue
			}
			if f.Tag == nil {
				continue
			}
			tag, _ := strconv.Unquote(f.Tag.Value)
			name := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			field := g.schema(f.Type, pkg)
			if name == "revision" || name == "version" {
				field["description"] = "并发校验标识；编辑时回传最新读取值；不在界面展示内部编号。"
			}
			if name == "content" {
				field["description"] = "完整内核配置；Key 可原位保留脱敏占位符。省略 content 不修改，显式空字符串提交清空并接受内核校验；并发标识必需。"
			}
			g.annotateField(name, field)
			props[name] = field
		}
		return object{"type": "object", "properties": props, "additionalProperties": false}
	}
	return object{}
}
func (g *generator) named(name string) object {
	key := strings.ReplaceAll(name, ".", "_")
	if _, ok := g.schemas[key]; ok {
		return object{"$ref": "#/components/schemas/" + key}
	}
	m, ok := g.models[name]
	if !ok {
		return object{}
	}
	g.schemas[key] = object{}
	g.schemas[key] = g.schema(m.expr, m.pkg)
	g.annotateModel(name, g.schemas[key].(object))
	if s, ok := g.schemas[key].(object); ok && s["type"] == "string" {
		enums := []string{}
		prefix := strings.Split(name, ".")[0] + "."
		for c, v := range g.constants {
			if strings.HasPrefix(c, prefix) && (name == "core.APIKeyMode" && strings.HasPrefix(c, "core.APIKeyRead")) {
				enums = append(enums, v)
			}
		}
		sort.Strings(enums)
		if name == "core.Engine" {
			enums = []string{"mihomo", "xray", "sing-box", "ss-rust"}
		}
		if name == "core.Action" {
			for c, v := range g.constants {
				if strings.HasPrefix(c, "core.Action") {
					enums = append(enums, v)
				}
			}
			sort.Strings(enums)
		}
		if name == "core.TaskStatus" {
			enums = []string{"pending", "running", "succeeded", "failed", "canceled"}
		}
		if name == "core.Role" {
			enums = []string{"admin", "user"}
		}
		if name == "core.Permission" {
			for c, v := range g.constants {
				if strings.HasPrefix(c, "core.Permission") {
					enums = append(enums, v)
				}
			}
			sort.Strings(enums)
		}
		if len(enums) > 0 {
			s["enum"] = enums
		}
	}
	return object{"$ref": "#/components/schemas/" + key}
}

func commonResponses() object {
	result := object{}
	for status, description := range map[string]string{"400": "参数或业务校验失败", "401": "凭据无效、停用、到期、撤销或创建者失效", "403": "模式、权限、来源或资源授权拒绝", "404": "资源不存在或不可见", "409": "并发变更或状态冲突；重新读取并保留草稿", "429": "限流；按 Retry-After 有限等待", "500": "内部错误；记录 X-Request-ID 或 request_id"} {
		result["error"+status] = object{"description": description, "content": object{"application/json": object{"schema": object{"type": "object", "required": []string{"error"}, "properties": object{"error": object{"type": "string"}, "request_id": object{"type": "string"}, "code": object{"type": "string"}, "stage": object{"type": "string"}, "failure_retention": object{"type": "string", "enum": []string{"not_required", "saved", "failed"}}}}, "example": object{"error": description, "request_id": "req_example"}}}}
	}
	return result
}
func (g *generator) infer(expr ast.Expr, pkg string, vars map[string]model) object {
	switch e := expr.(type) {
	case *ast.Ident:
		if m, ok := vars[e.Name]; ok {
			return g.schema(m.expr, m.pkg)
		}
		if e.Name == "true" || e.Name == "false" {
			return object{"type": "boolean"}
		}
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			return object{"type": "string"}
		}
		return object{"type": "number"}
	case *ast.CompositeLit:
		if _, ok := e.Type.(*ast.MapType); ok {
			props := object{}
			for _, elt := range e.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if key, ok := literal(kv.Key); ok {
						props[key] = g.infer(kv.Value, pkg, vars)
					}
				}
			}
			return object{"type": "object", "properties": props}
		}
		return g.schema(e.Type, pkg)
	case *ast.CallExpr:
		name := source(e.Fun)
		parts := strings.Split(name, ".")
		for _, p := range []string{"store", "api", "core"} {
			if fn, ok := g.funcs[p+"."+parts[len(parts)-1]]; ok && fn.decl.Type.Results != nil && len(fn.decl.Type.Results.List) > 0 {
				return g.schema(fn.decl.Type.Results.List[0].Type, p)
			}
		}
	}
	return object{}
}
func (g *generator) example(schema object, depth int) any {
	if depth > 6 {
		return nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		if resolved, ok := g.schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(object); ok {
			return g.example(resolved, depth+1)
		}
	}
	if choices, ok := schema["anyOf"].([]any); ok && len(choices) > 0 {
		return g.example(choices[0].(object), depth+1)
	}
	switch schema["type"] {
	case "object":
		result := object{}
		if properties, ok := schema["properties"].(object); ok {
			for name, property := range properties {
				value := g.example(property.(object), depth+1)
				if name == "id" {
					value = "resource_example"
				}
				if name == "content" {
					value = "mixed-port: 21001"
				}
				if name == "name" {
					value = "example"
				}
				result[name] = value
			}
		}
		return result
	case "array":
		if items, ok := schema["items"].(object); ok {
			return []any{g.example(items, depth+1)}
		}
		return []any{}
	case "integer", "number":
		return 1
	case "boolean":
		return false
	case "string":
		if schema["format"] == "date-time" {
			return "2026-11-10T12:00:00Z"
		}
		if values, ok := schema["enum"].([]string); ok && len(values) > 0 {
			return values[0]
		}
		return "example"
	}
	return nil
}
