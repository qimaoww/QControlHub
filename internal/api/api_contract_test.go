package api

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/apidoc"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAPIContractMatchesSourceAndEveryKeyPolicy(t *testing.T) {
	generated, err := apidoc.Generate("../..")
	if err != nil {
		t.Fatal(err)
	}
	shipped, err := os.ReadFile("contract/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, shipped) {
		t.Fatal("API contract drift; run go run scripts/generate-api-spec.go")
	}
	var document struct {
		Paths map[string]map[string]struct {
			Key    bool `json:"x-api-key"`
			Policy struct {
				Write       bool              `json:"write"`
				Any         bool              `json:"any"`
				Permissions []core.Permission `json:"permissions"`
			} `json:"x-key-policy"`
			Parameters []struct {
				Name   string `json:"name"`
				In     string `json:"in"`
				Schema struct {
					Type    string `json:"type"`
					Maximum int    `json:"maximum"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err = json.Unmarshal(shipped, &document); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for path, methods := range document.Paths {
		for method, operation := range methods {
			pattern := strings.ToUpper(method) + " " + path
			policy, exists := apiKeyPolicies[pattern]
			if operation.Key != exists {
				t.Fatalf("route/Key policy mismatch: %s", pattern)
			}
			if !exists {
				continue
			}
			seen[pattern] = true
			raw, _ := json.Marshal(policy.Permissions)
			declared, _ := json.Marshal(operation.Policy.Permissions)
			if policy.Write != operation.Policy.Write || policy.Any != operation.Policy.Any || (len(policy.Permissions) > 0 && !bytes.Equal(raw, declared)) {
				t.Fatalf("capability drift: %s", pattern)
			}
			if policy.Write && policy.allows(core.APIKeyReadOnly, core.APIKeyPermissions()) {
				t.Fatalf("read-only mutation: %s", pattern)
			}
			for _, p := range policy.Permissions {
				if !core.HasPermission(core.APIKeyPermissions(), p) {
					t.Fatalf("unknown/nondelegable policy permission: %s", p)
				}
			}
			if len(policy.Permissions) > 0 && policy.allows(core.APIKeyReadWrite, nil) {
				t.Fatalf("empty permission bypass: %s", pattern)
			}
		}
	}
	for pattern := range apiKeyPolicies {
		if !seen[pattern] {
			t.Fatalf("unregistered or undocumented Key route: %s", pattern)
		}
	}
	// Aliased url.Values, range-based filters and repeated query parameters
	// must survive generation, so clients can call the documented read APIs.
	for path, names := range map[string][]string{
		"/api/v1/core-logs":          {"agent_id", "instance_id", "engine", "level", "q", "before", "limit"},
		"/api/v1/client-connections": {"since", "until", "port", "limit", "node_order", "cursor", "include_non_public"},
	} {
		parameters := document.Paths[path]["get"].Parameters
		for _, name := range names {
			found := false
			for _, parameter := range parameters {
				if parameter.Name == name && parameter.In == "query" {
					found = true
					if name == "limit" && (parameter.Schema.Type != "integer" || parameter.Schema.Maximum == 0) {
						t.Fatalf("unbounded limit in %s", path)
					}
				}
			}
			if !found {
				t.Fatalf("missing query %s in %s", name, path)
			}
		}
	}
	for _, pattern := range []string{"POST /api/v1/users", "DELETE /api/v1/users/{id}", "POST /api/v1/enrollment-tokens", "POST /api/v1/settings/api-keys", "PUT /api/v1/certificate-accounts/{id}/credentials"} {
		if _, ok := apiKeyPolicies[pattern]; ok {
			t.Fatalf("credential boundary opened: %s", pattern)
		}
	}
}
