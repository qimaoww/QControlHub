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
	for _, pattern := range []string{"POST /api/v1/users", "DELETE /api/v1/users/{id}", "POST /api/v1/enrollment-tokens", "POST /api/v1/settings/api-keys", "PUT /api/v1/certificate-accounts/{id}/credentials"} {
		if _, ok := apiKeyPolicies[pattern]; ok {
			t.Fatalf("credential boundary opened: %s", pattern)
		}
	}
}
