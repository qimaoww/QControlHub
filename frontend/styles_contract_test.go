package frontend

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMotionSystemCoversWorkspaceInteractions(t *testing.T) {
	styles, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	content := string(styles)
	for _, required := range []string{
		"--motion-fast:120ms",
		"--motion-base:320ms",
		"--motion-slow:480ms",
		"--motion-feedback:280ms",
		"--motion-distance:24px",
		"@keyframes qch-dialog-enter",
		"@keyframes qch-fade-enter",
		"@keyframes qch-route-progress",
		"@keyframes qch-boot-orbit",
		".boot-content",
		".boot-mark::before",
		".workspace-main.is-route-pending::before",
		"dialog[open]::backdrop",
		".modal-backdrop::before",
		".modal-backdrop>[role=dialog]",
		"@media(prefers-reduced-motion:reduce)",
		"animation:none!important;transition:none!important;scroll-behavior:auto!important",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("motion system is missing %q", required)
		}
	}
	refresh, err := os.ReadFile("modules/refresh.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(refresh), "!existingNodes.has(child)") || !strings.Contains(string(refresh), "enterSurface(child") {
		t.Error("reconciled dynamic content must animate only when inserted")
	}
	for _, forbidden := range []string{".workspace-main.page-enter", ".qch-reconcile-enter", "@keyframes qch-nav-active"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("motion must not retain refresh-replayed entrances: %s", forbidden)
		}
	}
	// Behavior is verified by the module and real browser suites, rather than
	// requiring the retired CSS classes/keyframe names as the implementation.
	for _, suite := range []struct{ path, marker string }{
		{"module_smoke.mjs", `import("./smoke/motion.mjs")`},
		{"agents_browser_runtime.mjs", `import { testMotionRuntime }`},
	} {
		source, err := os.ReadFile(suite.path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), suite.marker) {
			t.Errorf("motion regressions must run from %s", suite.path)
		}
	}
	configs, err := os.ReadFile("modules/live-config-view.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"const liveConfigPhase = current",
		"data-live-config-phase=\"${esc(liveConfigPhase)}\"",
		"data-refresh-key=\"live-config-content-${esc(agent.id)}-${esc(engine)}-${esc(sourceMode)}-${esc(liveConfigPhase)}\"",
	} {
		if !strings.Contains(string(configs), required) {
			t.Errorf("live configuration transition is missing %q", required)
		}
	}
}

func TestInitialConsoleStylesRemainExactOutsideApprovedExtensions(t *testing.T) {
	styles, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	const extensionMarker = "/* Deployment command dialog v48: a scoped extension to the initial console surface. */"
	parts := strings.SplitN(string(styles), extensionMarker, 2)
	if len(parts) != 2 {
		t.Fatalf("app.css is missing approved deployment dialog extension marker")
	}
	// Base hash covers the initial release plus the approved v56 revision that
	// reserved the compact media queries for coarse-pointer devices so desktop
	// zoom keeps one fixed layout and the shared typography scale revision.
	const expected = "bbf685608d6a619a5db5cdbee9e6a287fd9aa698f71070038c312f14976d73cc"
	if actual := fmt.Sprintf("%x", sha256.Sum256([]byte(parts[0]))); actual != expected {
		t.Fatalf("base app.css hash = %s, want initial release hash %s", actual, expected)
	}
	for _, required := range []string{".deploy-command-modal", ".deploy-command-input", ".deploy-command-copy"} {
		if !strings.Contains(parts[1], required) {
			t.Errorf("deployment dialog extension is missing %q", required)
		}
	}
}
