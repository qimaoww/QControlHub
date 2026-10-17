package agent

import (
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"gopkg.in/yaml.v3"
)

type coreCertificateReference struct {
	path, field string
	set         func(string)
}

func coreCertificateReferences(engine core.Engine, content string) (map[string]any, []coreCertificateReference, error) {
	root, refs, err := serverconfig.CertificateFileReferences(engine, content)
	out := make([]coreCertificateReference, 0, len(refs))
	for _, ref := range refs {
		out = append(out, coreCertificateReference{path: ref.Path, field: ref.Field, set: ref.Set})
	}
	return root, out, err
}

func coreCertificatePaths(engine core.Engine, content string) ([]string, error) {
	_, refs, err := coreCertificateReferences(engine, content)
	paths := make([]string, 0, len(refs))
	for _, ref := range refs {
		paths = append(paths, ref.path)
	}
	return paths, err
}

// Validation uses a fresh Mihomo home to avoid touching live state. Resolve
// relative file references against the service's real home before writing the
// temporary snapshot, so -t and the deployed service read the same files.
func mihomoValidationCertificatePaths(content, state string) (string, error) {
	root, refs, err := coreCertificateReferences(core.EngineMihomo, content)
	if err != nil {
		return "", err
	}
	changed := false
	for _, ref := range refs {
		if !filepath.IsAbs(ref.path) {
			ref.set(filepath.Join(state, ref.path))
			changed = true
		}
	}
	if !changed {
		return content, nil
	}
	value, err := yaml.Marshal(root)
	return string(value), err
}
