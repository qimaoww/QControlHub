package agent

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// singBoxResourceKind distinguishes sing-box configuration fields whose value
// is a regular file from fields whose value is a directory. The official sing-box
// service form (-D dir) chdirs before it resolves any of these paths, so after
// migration the QAgent managed unit runs with a different working directory and
// a relative resource would silently resolve elsewhere.
type singBoxResourceKind int

const (
	singBoxFileResource singBoxResourceKind = iota
	singBoxDirectoryResource
)

func (k singBoxResourceKind) String() string {
	if k == singBoxDirectoryResource {
		return "directory"
	}
	return "file"
}

type singBoxResourceField struct {
	key string
	// kind is the kind of filesystem resource the field carries.
	kind singBoxResourceKind
	// allowLogSpecial permits log.output console literals and relative file
	// names. The import-specific validator separately resolves file names into
	// the protected managed sing-box state directory.
	allowLogSpecial bool
}

// singBoxResourceFieldsByParent is the set of sing-box JSON fields that are
// resolved as filesystem resources at runtime, keyed by the enclosing option
// object. It is derived from the sing-box v1.13.19 option package, which is the
// version used by the real deployment target, and covers every field that
// sing-box opens, reads, creates, or persists through its working directory.
// The parent key is used instead of a suffix heuristic so URL fields named path
// (HTTP/transport) are not mistaken for files while directory resources such as
// acme.data_directory, external_ui, and certificate_directory_path are caught.
var singBoxResourceFieldsByParent = map[string][]singBoxResourceField{
	"log": {
		{key: "output", kind: singBoxFileResource, allowLogSpecial: true},
	},
	"certificate": {
		{key: "certificate_path", kind: singBoxFileResource},
		{key: "certificate_directory_path", kind: singBoxDirectoryResource},
	},
	"tls": {
		{key: "certificate_path", kind: singBoxFileResource},
		{key: "key_path", kind: singBoxFileResource},
		{key: "client_certificate_path", kind: singBoxFileResource},
		{key: "client_key_path", kind: singBoxFileResource},
	},
	"acme": {
		{key: "data_directory", kind: singBoxDirectoryResource},
	},
	"ech": {
		{key: "key_path", kind: singBoxFileResource},
		{key: "config_path", kind: singBoxFileResource},
	},
	"geoip": {
		{key: "path", kind: singBoxFileResource},
	},
	"geosite": {
		{key: "path", kind: singBoxFileResource},
	},
	"cache_file": {
		{key: "path", kind: singBoxFileResource},
	},
	"clash_api": {
		{key: "external_ui", kind: singBoxDirectoryResource},
		{key: "cache_file", kind: singBoxFileResource},
	},
	"masquerade": {
		// Hysteria2 masquerade type=file serves a directory.
		{key: "directory", kind: singBoxDirectoryResource},
	},
}

// singBoxTypeResourceFields covers sing-box option objects that are identified
// by their type discriminator rather than by a stable parent key, including
// the local rule set, SSH/Tor outbounds, Tailscale endpoints, and the CCM, OCM,
// DERP, and SSM-API services from the v1.13.19 option package.
func singBoxTypeResourceFields(nodeType string) []singBoxResourceField {
	switch nodeType {
	case "local":
		return []singBoxResourceField{{key: "path", kind: singBoxFileResource}}
	case "ssh":
		return []singBoxResourceField{{key: "private_key_path", kind: singBoxFileResource}}
	case "tor":
		return []singBoxResourceField{
			{key: "executable_path", kind: singBoxFileResource},
			{key: "data_directory", kind: singBoxDirectoryResource},
		}
	case "tailscale":
		// Tailscale endpoint and tailscale DNS server persist state in this
		// directory; the DERP service uses config_path and mesh_psk_file.
		return []singBoxResourceField{
			{key: "state_directory", kind: singBoxDirectoryResource},
			{key: "config_path", kind: singBoxFileResource},
			{key: "mesh_psk_file", kind: singBoxFileResource},
		}
	case "ccm", "ocm":
		return []singBoxResourceField{
			{key: "credential_path", kind: singBoxFileResource},
			{key: "usages_path", kind: singBoxFileResource},
		}
	case "derp":
		return []singBoxResourceField{
			{key: "config_path", kind: singBoxFileResource},
			{key: "mesh_psk_file", kind: singBoxFileResource},
		}
	case "ssm-api":
		return []singBoxResourceField{{key: "cache_path", kind: singBoxFileResource}}
	case "hosts":
		// DNS hosts server reads one or more hosts files.
		return []singBoxResourceField{{key: "path", kind: singBoxFileResource}}
	default:
		return nil
	}
}

// singBoxGlobalResourceFields are unambiguous filesystem resources wherever
// they appear in a sing-box option object, such as a dialer protect socket.
var singBoxGlobalResourceFields = []singBoxResourceField{
	{key: "protect_path", kind: singBoxFileResource},
}

// singBoxResourceNameSuffixes is a conservative, non-ambiguous fallback for
// sing-box fields whose JSON key ends in a filesystem-resource naming
// convention. It deliberately excludes the bare URL-valued path field on
// HTTP/transport objects and the process_path/process_path_regex rule matching
// patterns, which are not files opened from the configuration. Any future
// sing-box resource field that follows these conventions is still rejected
// when its value is relative, instead of silently drifting with the working
// directory.
var singBoxResourceNameSuffixes = []string{
	"_path",
	"_file",
	"_dir",
	"_directory",
	"_ui",
}

// singBoxResourceNameExceptions are keys that match a resource suffix but are
// not filesystem resources read from the working directory.
var singBoxResourceNameExceptions = map[string]struct{}{
	"process_path":       {},
	"process_path_regex": {},
}

// validateNoRelativeSingBoxResources fails closed for every sing-box option field
// that sing-box resolves against its working directory. By enumerating the
// official option contract by enclosing object and type discriminator, then
// applying a non-ambiguous resource-name fallback, it catches every current
// file and directory resource (including string-array forms) without treating
// URL path fields as files. Every path must be absolute or migration cannot
// preserve the original semantics.
func validateNoRelativeSingBoxResources(content string) error {
	var root any
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return err
	}
	return validateSingBoxResourceNodes(root, "", "")
}

func validateSingBoxResourceNodes(value any, parentKey, nodeType string) error {
	switch node := value.(type) {
	case map[string]any:
		if nodeType == "" {
			if resourceType, ok := node["type"].(string); ok {
				nodeType = resourceType
			}
		}
		if nodeType == "inbounds" || nodeType == "outbounds" || nodeType == "rule_set" ||
			nodeType == "endpoints" || nodeType == "http_clients" ||
			nodeType == "certificate_providers" || nodeType == "services" {
			// A map that is an array element of a named option collection must
			// derive its discriminator from its own type field, not from the
			// collection key passed down from its parent.
			nodeType = ""
			if resourceType, ok := node["type"].(string); ok {
				nodeType = resourceType
			}
		}
		for _, field := range singBoxResourceFieldsByParent[parentKey] {
			if raw, ok := node[field.key]; ok {
				if err := checkSingBoxResourceField(field, raw); err != nil {
					return err
				}
			}
		}
		for _, field := range singBoxTypeResourceFields(nodeType) {
			if raw, ok := node[field.key]; ok {
				if err := checkSingBoxResourceField(field, raw); err != nil {
					return err
				}
			}
		}
		for _, field := range singBoxGlobalResourceFields {
			if raw, ok := node[field.key]; ok {
				if err := checkSingBoxResourceField(field, raw); err != nil {
					return err
				}
			}
		}
		for key, raw := range node {
			field, ok := singBoxResourceFieldForName(key)
			if !ok {
				continue
			}
			alreadyChecked := false
			for _, explicit := range append(append([]singBoxResourceField{},
				singBoxResourceFieldsByParent[parentKey]...),
				append(singBoxTypeResourceFields(nodeType), singBoxGlobalResourceFields...)...) {
				if explicit.key == key {
					alreadyChecked = true
					break
				}
			}
			if alreadyChecked {
				continue
			}
			if err := checkSingBoxResourceField(field, raw); err != nil {
				return err
			}
		}
		for key, child := range node {
			if err := validateSingBoxResourceNodes(child, key, ""); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range node {
			if err := validateSingBoxResourceNodes(item, parentKey, ""); err != nil {
				return err
			}
		}
	case string:
		// The Hysteria2 masquerade option is polymorphic: a JSON string is a
		// URL that sing-box parses into a file, proxy, or string masquerade.
		if parentKey != "masquerade" {
			return nil
		}
		return validateSingBoxMasqueradeURL(node)
	}
	return nil
}

// validateSingBoxMasqueradeURL applies the sing-box v1.13.19 URL semantics for
// the Hysteria2 masquerade string form. A file: URL is served by http.Dir on
// its path, so a relative path would resolve against the process working
// directory and drift after migration; it is rejected unless the directory is
// absolute. http/https URLs are network proxies and are not filesystem
// resources. Unknown or malformed schemes are left to the real core to reject
// strictly.
func validateSingBoxMasqueradeURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	switch parsed.Scheme {
	case "file":
		if parsed.Path == "" || !filepath.IsAbs(parsed.Path) {
			return fmt.Errorf("relative sing-box directory resource path in masquerade (%q) cannot be migrated safely", raw)
		}
	case "http", "https":
		// Network proxy URL, not a filesystem resource.
	default:
		// Unknown scheme: let the real core reject it.
	}
	return nil
}

// singBoxResourceFieldForName maps a key to a filesystem-resource field when it
// follows a resource naming convention and is not a process-path or URL field.
func singBoxResourceFieldForName(key string) (singBoxResourceField, bool) {
	if _, exception := singBoxResourceNameExceptions[key]; exception {
		return singBoxResourceField{}, false
	}
	if key == "path" {
		// Bare path is URL-valued on HTTP/transport objects and only a resource
		// in the explicit parent/type contract (geoip, geosite, cache_file,
		// hosts, local rule set).
		return singBoxResourceField{}, false
	}
	for _, suffix := range singBoxResourceNameSuffixes {
		if strings.HasSuffix(key, suffix) {
			kind := singBoxFileResource
			if strings.HasSuffix(key, "_directory") || strings.HasSuffix(key, "_dir") ||
				strings.HasSuffix(key, "data_directory") || strings.HasSuffix(key, "state_directory") ||
				strings.HasSuffix(key, "_ui") || key == "certificate_directory_path" {
				kind = singBoxDirectoryResource
			}
			return singBoxResourceField{key: key, kind: kind}, true
		}
	}
	return singBoxResourceField{}, false
}

func checkSingBoxResourceField(field singBoxResourceField, value any) error {
	check := func(path string) error {
		if path == "" {
			return nil
		}
		if field.allowLogSpecial {
			return nil
		}
		if filepath.IsAbs(path) {
			return nil
		}
		return fmt.Errorf("relative sing-box %s resource path in %s (%q) cannot be migrated safely", field.kind, field.key, path)
	}
	switch raw := value.(type) {
	case string:
		return check(raw)
	case []any:
		for _, item := range raw {
			path, ok := item.(string)
			if !ok {
				return fmt.Errorf("sing-box resource field %q contains a non-string element", field.key)
			}
			if err := check(path); err != nil {
				return err
			}
		}
		return nil
	default:
		// A non-string value is a type error caught by sing-box's own strict
		// option unmarshal; it is not a path that can drift with the working
		// directory, so it is left for the real core validation to reject.
		return nil
	}
}
