package architecture_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/qimaoww/qcontrolhub"

type dependencyRule struct {
	ImporterPrefix  string
	ForbiddenPrefix []string
	Description     string
}

var dependencyRules = []dependencyRule{
	{
		ImporterPrefix:  modulePath + "/internal/core",
		ForbiddenPrefix: []string{modulePath},
		Description:     "core must not depend on application packages or adapters",
	},
	{
		ImporterPrefix: modulePath + "/internal/store",
		ForbiddenPrefix: []string{
			modulePath + "/internal/api",
			modulePath + "/internal/agent",
			modulePath + "/cmd/",
		},
		Description: "store must not depend on HTTP, agent, or command packages",
	},
	{
		ImporterPrefix: modulePath + "/internal/api",
		ForbiddenPrefix: []string{
			modulePath + "/internal/agent",
			modulePath + "/cmd/",
		},
		Description: "api must not depend on agent or command packages",
	},
	{
		ImporterPrefix: modulePath + "/internal/hostmetrics",
		ForbiddenPrefix: []string{
			modulePath + "/internal/agent",
			modulePath + "/internal/api",
			modulePath + "/internal/store",
		},
		Description: "hostmetrics must not depend on agent, API, or store packages",
	},
}

func TestProductionPackagesRespectDependencyDirection(t *testing.T) {
	packages := sourcePackages(t)
	for _, expected := range []string{
		modulePath + "/internal/core",
		modulePath + "/internal/store",
		modulePath + "/internal/api",
	} {
		if _, ok := packages[expected]; !ok {
			t.Fatalf("sourcePackages() did not scan expected production package %s", expected)
		}
	}
	violations := validateDependencies(packages)
	if len(violations) != 0 {
		t.Fatalf("architecture dependency violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestDependencyPolicyRejectsRepresentativeViolations(t *testing.T) {
	packages := map[string][]string{
		modulePath + "/internal/core":        {modulePath, modulePath + "/internal/store"},
		modulePath + "/internal/store":       {modulePath + "/internal/api"},
		modulePath + "/internal/api":         {modulePath + "/internal/agent"},
		modulePath + "/internal/hostmetrics": {modulePath + "/internal/api"},
		modulePath + "/internal/netpolicy":   {modulePath + "/cmd/control-plane"},
	}
	violations := validateDependencies(packages)
	for _, want := range []string{
		"internal/core imports github.com/qimaoww/qcontrolhub:",
		"internal/core imports github.com/qimaoww/qcontrolhub/internal/store",
		"internal/store imports github.com/qimaoww/qcontrolhub/internal/api",
		"internal/api imports github.com/qimaoww/qcontrolhub/internal/agent",
		"internal/hostmetrics imports github.com/qimaoww/qcontrolhub/internal/api",
		"internal/netpolicy imports github.com/qimaoww/qcontrolhub/cmd/control-plane",
	} {
		if !contains(violations, want) {
			t.Fatalf("validateDependencies() = %v, want violation containing %q", violations, want)
		}
	}
}

func TestDependencyPolicyKeepsPackagePrefixesExact(t *testing.T) {
	packages := map[string][]string{
		modulePath + "/internal/corex":          {modulePath + "/internal/store"},
		modulePath + "/internal/storex":         {modulePath + "/internal/api"},
		modulePath + "/internal/api":            {modulePath + "/internal/agentx"},
		modulePath + "/internal/hostmetrics":    {modulePath + "/internal/storex"},
		modulePath + "/internal/netpolicy":      {modulePath + "/cmdline"},
		modulePath + "/cmd/control-plane-extra": {modulePath + "/internal/core"},
	}
	if violations := validateDependencies(packages); len(violations) != 0 {
		t.Fatalf("validateDependencies() = %v, want no prefix-boundary false positives", violations)
	}
}

func sourcePackages(t *testing.T) map[string][]string {
	t.Helper()
	packages := make(map[string][]string)
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test")
	}
	root, absoluteErr := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if absoluteErr != nil {
		t.Fatalf("resolve repository root: %v", absoluteErr)
	}
	root = filepath.Clean(root)
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// filepath.WalkDir calls the callback for root too. Its base name can
			// begin with a dot in a checkout path, but it is not an ignored tree.
			if filePath == root {
				return nil
			}
			switch entry.Name() {
			case ".git", "bin", "vendor", "testdata", "node_modules":
				return filepath.SkipDir
			}
			if strings.HasPrefix(entry.Name(), ".") || strings.HasPrefix(entry.Name(), "_") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") || strings.HasPrefix(entry.Name(), "_") ||
			!strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), filePath, nil, parser.ImportsOnly)
		if parseErr != nil {
			return fmt.Errorf("parse imports from %s: %w", filePath, parseErr)
		}
		relativeDir, relativeErr := filepath.Rel(root, filepath.Dir(filePath))
		if relativeErr != nil {
			return relativeErr
		}
		importer := modulePath
		if relativeDir != "." {
			importer += "/" + path.Clean(filepath.ToSlash(relativeDir))
		}
		for _, spec := range file.Imports {
			imported, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				return fmt.Errorf("unquote import in %s: %w", filePath, unquoteErr)
			}
			packages[importer] = append(packages[importer], imported)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return packages
}

func validateDependencies(packages map[string][]string) []string {
	violations := make([]string, 0)
	for importer, imports := range packages {
		for _, imported := range imports {
			for _, rule := range dependencyRules {
				if !hasPackagePrefix(importer, rule.ImporterPrefix) {
					continue
				}
				for _, forbidden := range rule.ForbiddenPrefix {
					if hasPackagePrefix(imported, forbidden) {
						violations = append(violations, fmt.Sprintf("%s imports %s: %s", importer, imported, rule.Description))
					}
				}
			}
			if !hasPackagePrefix(importer, modulePath+"/cmd/") && hasPackagePrefix(imported, modulePath+"/cmd/") {
				violations = append(violations, fmt.Sprintf("%s imports %s: command packages are application entrypoints and must not be imported", importer, imported))
			}
		}
	}
	sort.Strings(violations)
	return violations
}

func hasPackagePrefix(path, prefix string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
