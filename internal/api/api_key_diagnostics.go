package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type apiKeyPolicy struct {
	Write       bool
	Any         bool
	Permissions []core.Permission
}

func (p apiKeyPolicy) allows(mode core.APIKeyMode, permissions []core.Permission) bool {
	if p.Write && mode != core.APIKeyReadWrite {
		return false
	}
	allowed := !p.Any
	for _, required := range p.Permissions {
		has := core.HasPermission(permissions, required)
		if p.Any && has {
			return true
		}
		if !p.Any && !has {
			return false
		}
	}
	return allowed
}

type apiKeyResponseWriter struct {
	http.ResponseWriter
	body          bytes.Buffer
	status        int
	secret        string
	revealSecrets bool
}

func (w *apiKeyResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *apiKeyResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(p)
}
func (w *apiKeyResponseWriter) finish() int {
	if w.status == 0 {
		w.status = 200
	}
	data := w.body.Bytes()
	if len(data) > 0 {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err == nil {
			safe := core.RedactDiagnosticValue(value)
			if w.revealSecrets {
				safe = value
			}
			redacted, err := json.Marshal(safe)
			if err == nil {
				data = append(redacted, '\n')
			} else {
				w.status = 500
				data = []byte(`{"error":"诊断输出无法脱敏"}`)
			}
		} else {
			if !w.revealSecrets {
				data = []byte(core.RedactDiagnostic(string(data)))
			}
		}
		// Last line of defense for reflected request text in legacy handlers.
		data = bytes.ReplaceAll(data, []byte(w.secret), []byte("[redacted]"))
	}
	w.Header().Del("Content-Length")
	w.Header().Del("ETag")
	w.Header().Set("X-QControlHub-Auth-Type", "api_key")
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(data)
	// Do not retain credentials in a writer after the request.
	w.secret = ""
	w.body.Reset()
	return w.status
}

func apiKeyRouteList(mode core.APIKeyMode, permissions []core.Permission) []string {
	result := []string{}
	for pattern, policy := range apiKeyPolicies {
		if policy.allows(mode, permissions) {
			result = append(result, strings.TrimSpace(pattern))
		}
	}
	return result
}

// Historical helper keeps diagnostic integrations pinned to read-only semantics.
func diagnosticRouteList(permissions []core.Permission) []string {
	return apiKeyRouteList(core.APIKeyReadOnly, permissions)
}
