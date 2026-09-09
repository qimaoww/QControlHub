package api

import (
	"crypto/sha256"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func hasChinese(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool { return unicode.Is(unicode.Han, r) })
}

func TestChineseErrorMessages(t *testing.T) {
	for original, want := range errorMessages {
		t.Run(original, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeError(response, http.StatusBadRequest, original)
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusBadRequest || body["error"] != want || !hasChinese(want) {
				t.Fatalf("unexpected error response: %d %v", response.Code, body)
			}
			if response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatal("error response must remain JSON with UTF-8")
			}
		})
	}
	for _, value := range []string{"invalid input: username is required", "conflict: configuration changed; reload before saving", "invalid JSON body: unexpected EOF", "invalid mihomo YAML: unexpected token", "invalid xray JSON: trailing data", `unsupported engine "unknown"`, "configuration exceeds 2097152 bytes", "ss-rust configuration must be a JSON object", `duplicate capability "xray"`} {
		if got := chineseErrorMessage(http.StatusBadRequest, value); !hasChinese(got) || got == value {
			t.Errorf("not translated: %q -> %q", value, got)
		}
	}
	for _, status := range []int{400, 401, 403, 404, 409, 422, 429, 500, 502, 503, 504, 599} {
		if got := chineseErrorMessage(status, "unknown internal detail /private/path"); !hasChinese(got) || strings.Contains(got, "/private/path") {
			t.Errorf("unsafe fallback for %d: %q", status, got)
		}
	}
	if got := chineseErrorMessage(400, "conflict: 请刷新配置后重试"); got != "请刷新配置后重试" {
		t.Fatalf("Chinese cause lost: %q", got)
	}
	for original, want := range map[string]string{
		"conflict: sing-box core tasks are disabled because an existing service could not be mapped safely: unsupported executable wrapper": "现有 sing-box 服务无法安全接管，已禁用该内核的任务。请检查现有服务布局后重新发现。原始原因（供排查）：unsupported executable wrapper",
		"net.ipv4.tcp_rmem requires 3 integers":                   "参数 net.ipv4.tcp_rmem 需要填写 3 个整数。",
		"net.ipv4.tcp_rmem requires ordered integers in [1, 100]": "参数 net.ipv4.tcp_rmem 需要按从小到大的顺序填写整数，范围为 1 到 100。",
		`invalid JSON body: json: unknown field "typo"`:           "请求包含不支持的字段 typo，请检查字段名称或刷新页面后重试。",
		"conflict: invalid or duplicate engine \"xray\"":          "内核 xray 无效或重复，请重新选择。",
		"预设配置无法建立独立出口归属，未保存或部署：no explicit default outbound":      "预设配置无法建立独立出口归属，未保存或部署：未配置明确的默认出口，请先设置默认出口再启用统计。",
	} {
		if got := chineseErrorMessage(400, original); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// Every literal API rejection needs a specific translation, not just a generic
// status fallback. New English writeError calls must extend the catalog.
func TestAPIErrorLiteralCoverage(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 3 {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok || name.Name != "writeError" {
				return true
			}
			literal, ok := call.Args[2].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			message, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			if _, translated := errorMessages[message]; !translated && !hasChinese(message) {
				t.Errorf("%s: missing specific Chinese error for %q", path, message)
			}
			return true
		})
	}
}

func TestLoginChineseErrorsPreserveSecurity(t *testing.T) {
	tokenValue := strings.Repeat("a", 32)
	for _, test := range []struct {
		name, origin, token, want string
		secure                    bool
		status                    int
	}{
		{"HTTP against HTTPS panel", "http://panel.example", tokenValue, "HTTPS", true, 403},
		{"foreign origin", "https://other.example", tokenValue, "协议、域名或端口", true, 403},
		{"invalid credentials", "https://panel.example", "wrong", "用户名、密码或管理员令牌", true, 401},
		{"valid HTTPS login", "https://panel.example", tokenValue, "", true, 200},
		{"explicit HTTP development login", "http://panel.example", tokenValue, "", false, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := New(nil, Config{AdminTokenDigest: sha256.Sum256([]byte(tokenValue)), SecureTransport: test.secure})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"token":"`+test.token+`"}`))
			request.Host = "panel.example"
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("got %d %s, want %d containing %q", response.Code, response.Body, test.status, test.want)
			}
			if test.status != 200 && response.Header().Get("Set-Cookie") != "" {
				t.Fatal("rejected login must not issue a session cookie")
			}
		})
	}
}
