package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestRenderConfigTemplatePlaceholders(t *testing.T) {
	t.Parallel()
	agent := core.Agent{
		ID: "agt_0123456789abcdef", Name: "edge-01",
		Metrics: core.HostMetrics{NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.168.31.205"}}}},
	}
	rendered, err := RenderConfigTemplate(
		"server: {{node_name}}\nid: {{node_id}}\nip: {{lan_ip}}\nport: {{random_port}}\nliteral: {{unknown}}",
		agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"edge-01", "agt_0123456789abcdef", "192.168.31.205", "literal: {{unknown}}"} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("rendered template misses %q: %s", expected, rendered)
		}
	}
	if !strings.Contains(rendered, "port: 2") && !strings.Contains(rendered, "port: 3") && !strings.Contains(rendered, "port: 4") && !strings.Contains(rendered, "port: 5") && !strings.Contains(rendered, "port: 6") {
		t.Fatalf("random port outside 20000-63991: %s", rendered)
	}
	again, err := RenderConfigTemplate("port: {{random_port}}", agent)
	if err != nil {
		t.Fatal(err)
	}
	if again == rendered {
		t.Fatal("two renders produced the same random port")
	}
}

func TestConfigTemplateLifecycleWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	template, err := dataStore.CreateConfigTemplate(ctx, "  edge ss  ", "ss-rust",
		"{\n  \"server\": \"{{lan_ip}}\",\n  \"server_port\": {{random_port}},\n  \"password\": \"change-me\",\n  \"method\": \"chacha20-ietf-poly1305\"\n}")
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if template.Name != "edge ss" || template.Engine != core.EngineShadowsocksRust {
		t.Fatalf("created template = %+v", template)
	}
	templates, err := dataStore.ListConfigTemplates(ctx)
	if err != nil || len(templates) != 1 || templates[0].ID != template.ID {
		t.Fatalf("list templates = %+v, %v", templates, err)
	}
	// The stored row is encrypted when a key is set; here it is plaintext
	// mode, but the round trip must preserve the body.
	if templates[0].Content != template.Content {
		t.Fatalf("template content round trip mismatch")
	}

	if _, err := dataStore.CreateConfigTemplate(ctx, "", "mihomo", "x"); err == nil {
		t.Fatal("empty template name was accepted")
	}
	if _, err := dataStore.CreateConfigTemplate(ctx, "bad", "unknown-engine", "x"); err == nil {
		t.Fatal("unknown template engine was accepted")
	}
	if err := dataStore.DeleteConfigTemplate(ctx, "tpl_missing"); err == nil {
		t.Fatal("deleting a missing template succeeded")
	}
	if err := dataStore.DeleteConfigTemplate(ctx, template.ID); err != nil {
		t.Fatalf("delete template: %v", err)
	}
	remaining, err := dataStore.ListConfigTemplates(ctx)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("templates after delete = %d, %v", len(remaining), err)
	}
}
