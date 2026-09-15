package frontend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentWebSocketProxyForwardsSourceChain(t *testing.T) {
	nginx, err := os.ReadFile("nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	const agentProxy = `location /agent/ { proxy_http_version 1.1; proxy_buffering off; proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection "upgrade"; proxy_set_header Host $http_host; proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for; proxy_pass $control_plane; }`
	if !strings.Contains(string(nginx), agentProxy) {
		t.Error("Agent WebSocket proxy must forward the existing trusted source chain")
	}
}

func TestOfficialDeploymentsTrustTheExactTwoHopProxyChain(t *testing.T) {
	compose, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	quickStart, err := os.ReadFile("../deploy/quick-start.sh")
	if err != nil {
		t.Fatal(err)
	}
	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	production, err := os.ReadFile("../docs/production.md")
	if err != nil {
		t.Fatal(err)
	}

	for _, source := range []struct {
		name    string
		content string
	}{
		{name: "bundled compose", content: string(compose)},
		{name: "external quick-start compose", content: string(quickStart)},
	} {
		for _, required := range []string{
			`QCH_TRUSTED_PROXY_CIDRS: ${QCH_TRUSTED_PROXY_CIDRS:-172.30.254.2/32,172.30.254.1/32}`,
			`QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS:-}`,
			`ipv4_address: ${QCH_WEB_PROXY_ADDRESS:-172.30.254.2}`,
			`ipv4_address: ${QCH_CONTROL_PLANE_PROXY_ADDRESS:-172.30.254.3}`,
			`subnet: ${QCH_CONTROL_PROXY_SUBNET:-172.30.254.0/24}`,
			`gateway: ${QCH_CONTROL_PROXY_GATEWAY:-172.30.254.1}`,
		} {
			if !strings.Contains(source.content, required) {
				t.Errorf("%s does not close the exact proxy chain with %q", source.name, required)
			}
		}
	}
	for _, required := range []string{
		`QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS`,
		`PREVIOUS_CONFIG_KEYS_FILE`,
		`prepend_unique_csv "$PREVIOUS_CONFIG_KEYS" "$CONFIG_KEY"`,
	} {
		if !strings.Contains(string(quickStart), required) {
			t.Errorf("quick-start key rotation contract is missing %q", required)
		}
	}

	qcontrolWeb := strings.SplitN(string(compose), "\n  qcontrol-web:", 2)
	if len(qcontrolWeb) != 2 {
		t.Fatal("bundled compose is missing qcontrol-web")
	}
	qcontrolWebBlock := strings.SplitN(qcontrolWeb[1], "\nvolumes:", 2)[0]
	if strings.Contains(qcontrolWebBlock, "\n      - backend") || strings.Contains(qcontrolWebBlock, "\n      backend:") {
		t.Error("qcontrol-web must reach control-plane only through its fixed proxy-chain address")
	}

	for _, required := range []string{
		`'QCH_WEB_PROXY_ADDRESS=172.30.254.2'`,
		`'QCH_CONTROL_PLANE_PROXY_ADDRESS=172.30.254.3'`,
		`'QCH_TRUSTED_PROXY_CIDRS=172.30.254.2/32,172.30.254.1/32'`,
	} {
		if !strings.Contains(string(makefile), required) {
			t.Errorf("make init-env is missing %q", required)
		}
	}
	for _, required := range []string{
		`trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$web_proxy_address/32")"`,
		`trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$proxy_gateway/32")"`,
		`"QCH_TRUSTED_PROXY_CIDRS=$trusted_proxy_cidrs"`,
	} {
		if strings.Count(string(quickStart), required) != 2 {
			t.Errorf("bundled and external env preparation must both preserve %q", required)
		}
	}
	for _, required := range []string{"宿主 Nginx 与 `qcontrol-web` 两跳代理", "两个精确 `/32` 端点", "禁止改成整个私网"} {
		if !strings.Contains(string(production), required) {
			t.Errorf("production proxy documentation is missing %q", required)
		}
	}
}

func TestStaticAssetsUseBuildGeneratedCacheKeys(t *testing.T) {
	index, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(index)
	for _, required := range []string{
		`/assets/app.css?v=__QCH_CSS_VERSION__`,
		`/assets/app.js?v=__QCH_JS_VERSION__`,
		`rel="modulepreload" href="/assets/modules/dashboard.js?v=__QCH_JS_VERSION__"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("index.html is missing cache key placeholder %q", required)
		}
	}
	dockerfile, err := os.ReadFile("../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, placeholder := range []string{"__QCH_CSS_VERSION__", "__QCH_JS_VERSION__"} {
		if !strings.Contains(string(dockerfile), placeholder) {
			t.Errorf("Dockerfile does not replace %s", placeholder)
		}
	}
	if !strings.Contains(string(dockerfile), `modules/[^\"]+\\.js`) || !strings.Contains(string(dockerfile), `?v=${js_version}`) {
		t.Error("Dockerfile does not add the aggregate JavaScript cache key to module imports")
	}
	if !strings.Contains(string(dockerfile), `import\\(\"\\./modules/`) {
		t.Error("Dockerfile does not add the aggregate JavaScript cache key to lazy module imports")
	}
	if !strings.Contains(string(dockerfile), `js_content_version`) || !strings.Contains(string(dockerfile), `${VERSION}`) {
		t.Error("Dockerfile JavaScript cache key must include both content and release version")
	}
	nginx := string(mustReadFrontendFile(t, "nginx.conf"))
	for _, required := range []string{
		`map "$uri:$arg_v" $qcontrol_cache_control`,
		`public, max-age=31536000, immutable`,
		`gzip on`,
		`gzip_static on`,
		`gzip_types application/javascript application/json image/svg+xml text/css`,
	} {
		if !strings.Contains(nginx, required) {
			t.Errorf("static asset delivery is missing %q", required)
		}
	}
	if !strings.Contains(string(dockerfile), `-exec gzip -9 -k {} +`) {
		t.Error("web image does not precompress static JavaScript and CSS")
	}
}

func TestSPAModulesArePublished(t *testing.T) {
	for _, name := range []string{
		"dashboard.js",
		"agents.js",
		"client-access.js",
		"substore-sync.js",
		"configs.js",
		"tasks.js",
		"traffic.js",
		"settings.js",
		"route-loader.js",
		"core-log-preferences.js",
		"../module_smoke.mjs",
	} {
		if _, err := os.Stat(filepath.Join("modules", name)); err != nil {
			t.Errorf("missing SPA module %s: %v", name, err)
		}
	}
}
