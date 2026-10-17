package serverconfig

import (
	"encoding/json"
	"fmt"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
	"strings"
)

type CertificateFileReference struct {
	Path, Field string
	Set         func(string)
}

// Enumerate file-capable fields in their actual configuration context. In
// particular Reality, WireGuard, JLS and other protocol keys are inline data,
// regardless of their length or whether they happen to resemble a pathname.
func CertificateFileReferences(engine core.Engine, content string) (map[string]any, []CertificateFileReference, error) {
	if len(content) > core.MaxConfigBytes {
		return nil, nil, fmt.Errorf("configuration exceeds %d bytes", core.MaxConfigBytes)
	}
	var root map[string]any
	var err error
	if engine == core.EngineMihomo {
		err = yaml.Unmarshal([]byte(content), &root)
	} else {
		err = json.Unmarshal([]byte(content), &root)
	}
	if err != nil {
		return nil, nil, err
	}
	var refs []CertificateFileReference
	add := func(node map[string]any, field string, pemOrFile bool, keys ...string) {
		for _, key := range keys {
			appendValue := func(value any, position string, set func(string)) {
				path, ok := value.(string)
				if !ok || path == "" || (pemOrFile && strings.Contains(path, "-----BEGIN ")) {
					return
				}
				refs = append(refs, CertificateFileReference{Path: path, Field: position, Set: set})
			}
			if list, ok := node[key].([]any); ok {
				for i, value := range list {
					appendValue(value, fmt.Sprintf("%s.%s[%d]", field, key, i), func(path string) { list[i] = path })
				}
			} else {
				appendValue(node[key], field+"."+key, func(path string) { node[key] = path })
			}
		}
	}
	object := func(value any) map[string]any { node, _ := value.(map[string]any); return node }
	each := func(key string, visit func(map[string]any, string)) {
		list, _ := root[key].([]any)
		for i, value := range list {
			if node := object(value); node != nil {
				visit(node, fmt.Sprintf("%s[%d]", key, i))
			}
		}
	}
	switch engine {
	case core.EngineMihomo:
		// These fields accept PEM or a file in Mihomo's ca/ech loaders.
		tlsFiles := func(node map[string]any, field string) {
			add(node, field, true, "certificate", "private-key", "client-auth-cert", "ech-key")
		}
		tlsFiles(object(root["tls"]), "tls")
		tlsFiles(object(root["tuic-server"]), "tuic-server")
		each("listeners", func(node map[string]any, field string) {
			switch node["type"] {
			case "http", "socks", "mixed", "vless", "vmess", "trojan", "hysteria2", "hysteria2-realm", "tuic", "anytls", "trusttunnel":
				tlsFiles(node, field)
				if node["type"] == "hysteria2" {
					add(object(node["realm-opts"]), field+".realm-opts", true, "certificate", "private-key")
				}
			}
		})
		proxyFiles := func(node map[string]any, field string) {
			switch node["type"] {
			case "ssh":
				add(node, field, true, "private-key")
			case "http", "socks5", "vmess", "vless", "trojan", "hysteria", "hysteria2", "tuic", "anytls", "gost-relay", "trusttunnel":
				add(node, field, true, "certificate", "private-key")
				if node["type"] == "vless" {
					add(object(object(node["xhttp-opts"])["download-settings"]), field+".xhttp-opts.download-settings", true, "certificate", "private-key")
				}
				if node["type"] == "hysteria2" {
					add(object(node["realm-opts"]), field+".realm-opts", true, "certificate", "private-key")
				}
			}
		}
		each("proxies", proxyFiles)
		for name, value := range object(root["proxy-providers"]) {
			payload, _ := object(value)["payload"].([]any)
			for i, proxy := range payload {
				proxyFiles(object(proxy), fmt.Sprintf("proxy-providers[%q].payload[%d]", name, i))
			}
		}
	case core.EngineXray:
		tlsFiles := func(node map[string]any, field string) {
			list, _ := node["certificates"].([]any)
			for i, cert := range list {
				// certificate/key are inline string arrays; only *File is a path.
				add(object(cert), fmt.Sprintf("%s.certificates[%d]", field, i), false, "certificateFile", "keyFile")
			}
		}
		var streamFiles func(map[string]any, string)
		streamFiles = func(node map[string]any, field string) {
			for _, key := range []string{"tlsSettings", "xtlsSettings"} {
				tlsFiles(object(node[key]), field+"."+key)
			}
			for _, transport := range []string{"xhttpSettings", "splithttpSettings"} {
				options := object(node[transport])
				streamFilesIfPresent := func(value any, position string) {
					if stream := object(value); stream != nil {
						streamFiles(stream, position)
					}
				}
				streamFilesIfPresent(options["downloadSettings"], field+"."+transport+".downloadSettings")
				streamFilesIfPresent(object(options["extra"])["downloadSettings"], field+"."+transport+".extra.downloadSettings")
			}
		}
		for _, key := range []string{"inbounds", "outbounds"} {
			each(key, func(node map[string]any, field string) {
				streamFiles(object(node["streamSettings"]), field+".streamSettings")
			})
		}
		streamFiles(object(root["transport"]), "transport")
	case core.EngineSingBox:
		// TLS occurs in inbound/outbound, endpoint and DNS server options,
		// including nested dialers. File-only fields never contain inline PEM.
		var visit func(any, string)
		visit = func(value any, field string) {
			switch node := value.(type) {
			case map[string]any:
				if tls := object(node["tls"]); tls != nil {
					add(tls, field+".tls", false, "certificate_path", "key_path", "client_certificate_path", "client_key_path")
					add(object(tls["ech"]), field+".tls.ech", false, "key_path", "config_path")
				}
				for key, child := range node {
					visit(child, field+"."+key)
				}
			case []any:
				for i, child := range node {
					visit(child, fmt.Sprintf("%s[%d]", field, i))
				}
			}
		}
		visit(root, "")
		add(object(root["certificate"]), "certificate", false, "certificate_path")
	}
	for i := range refs {
		refs[i].Field = strings.TrimPrefix(refs[i].Field, ".")
	}
	return root, refs, nil
}

// MapCertificateFiles visits only actual file-capable certificate fields.
// Reality, WireGuard and other inline protocol keys are never interpreted.
func MapCertificateFiles(engine core.Engine, content string, change func(string) (string, error)) (string, error) {
	root, refs, err := CertificateFileReferences(engine, content)
	if err != nil {
		return "", err
	}
	changed := false
	for _, ref := range refs {
		value, err := change(ref.Path)
		if err != nil {
			return "", err
		}
		if value != ref.Path {
			ref.Set(value)
			changed = true
		}
	}
	if !changed {
		return content, nil
	}
	var value []byte
	if engine == core.EngineMihomo {
		value, err = yaml.Marshal(root)
	} else {
		value, err = json.MarshalIndent(root, "", "  ")
	}
	return string(value), err
}
