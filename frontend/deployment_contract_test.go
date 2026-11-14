package frontend

import (
	"os"
	"os/exec"
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
}

func TestStaticAssetsUseBuildGeneratedCacheKeys(t *testing.T) {
	index, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(index)
	for _, required := range []string{
		`/assets/startup.css?v=__QCH_STARTUP_CSS_VERSION__`,
		`/assets/bootstrap.js?v=__QCH_JS_VERSION__`,
		`analytics=__QCH_ANALYTICS_CSS_VERSION__`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("index.html is missing cache key placeholder %q", required)
		}
	}
	dockerfile, err := os.ReadFile("../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, placeholder := range []string{"__QCH_JS_VERSION__", "__QCH_ANALYTICS_CSS_VERSION__", "__QCH_STARTUP_CSS_VERSION__"} {
		if !strings.Contains(string(dockerfile), placeholder) {
			t.Errorf("Dockerfile does not replace %s", placeholder)
		}
	}
	for _, retired := range []string{"COPY frontend/app.js ", "COPY frontend/app.css ", "__QCH_CSS_VERSION__", "/assets/app.js", "/assets/app.css"} {
		if strings.Contains(string(dockerfile), retired) || strings.Contains(content, retired) {
			t.Errorf("web entrypoint/build retains retired classic asset %q", retired)
		}
	}
	// Run the build expressions against both quote conventions and lazy imports.
	output := rewriteModuleCacheImports(t, "import { a } from './a.js';\nimport { b } from \"../b.js\";\nimport('./c.js');\nimport(\"../d.js\");\nimport('https://example.test/e.js');\n")
	for _, path := range []string{"./a.js?v=cache-test", "../b.js?v=cache-test", "./c.js?v=cache-test", "../d.js?v=cache-test", "https://example.test/e.js'"} {
		if !strings.Contains(output, path) {
			t.Errorf("module cache transformation missing %q: %s", path, output)
		}
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

func rewriteModuleCacheImports(t *testing.T, source string) string {
	t.Helper()
	dockerfile := mustReadFrontendFile(t, "../Dockerfile")
	var expressions []string
	for _, line := range strings.Split(string(dockerfile), "\n") {
		if strings.Contains(line, "-e ") && strings.Contains(line, "?v=${js_version}") {
			expressions = append(expressions, strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\\")))
		}
	}
	if len(expressions) != 2 {
		t.Fatalf("expected static and lazy cache expressions, got %d", len(expressions))
	}
	expressions[len(expressions)-1] = strings.TrimSuffix(expressions[len(expressions)-1], "{} +")
	command := exec.Command("sh", "-c", "js_version=cache-test; sed -E "+strings.Join(expressions, " "))
	command.Stdin = strings.NewReader(source)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("module cache expressions failed: %s: %v", output, err)
	}
	return string(output)
}

func TestSPAModulesArePublished(t *testing.T) {
	for _, name := range []string{
		"bootstrap.js", "analytics/app.js", "analytics/operations.js",
		"analytics/dashboard-panel.js", "analytics/dashboard-ranking.js",
		"analytics/dashboard-probes.js", "analytics/dashboard-topology.js", "analytics/dashboard-topo-shared.js",
		"analytics/dashboard-map.js", "analytics/pacific-map-data.js", "analytics/dashboard-telemetry.js",
		"analytics/nodes.js", "analytics/client-access.js", "analytics/substore.js",
		"analytics/configs.js", "analytics/live-config.js", "analytics/config-studio.js",
		"analytics/traffic.js", "analytics/settings.js", "analytics/observability.js",
		"analytics/users.js", "analytics/certificates.js", "analytics/ip-quality.js",
		"analytics/tcp.js", "analytics/themes.js",
		"modules/route-loader.js", "modules/core-log-preferences.js", "module_smoke.mjs",
	} {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("missing SPA module %s: %v", name, err)
		}
	}
}
