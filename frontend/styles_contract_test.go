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
		"@keyframes qch-page-enter",
		"@keyframes qch-card-enter",
		"@keyframes qch-dialog-enter",
		"@keyframes qch-route-progress",
		"@keyframes qch-boot-enter",
		"@keyframes qch-first-screen-enter",
		"@keyframes qch-context-enter",
		"@keyframes qch-task-result-enter",
		"@keyframes qch-reconcile-enter",
		"@keyframes qch-config-read-enter",
		".boot-content",
		".boot-mark::before",
		".workspace-main.first-screen.page-enter>",
		".workspace-main.first-screen.page-enter> *:nth-child(n){animation-delay:0ms}",
		".workspace-main.page-enter>",
		".workspace-main.is-route-pending::before",
		".context-sidebar.context-enter>",
		".page-tasks [data-task-result][open]>.task-result-block",
		".page-live-config .live-config-workspace[data-live-config-phase]",
		".client-access-toolbar>nav a.active",
		".substore-target-bar>nav>button.active",
		".core-log-filter-group button[aria-pressed=true]",
		".qch-reconcile-enter",
		"dialog[open]::backdrop",
		".modal-backdrop>[role=dialog]",
		"@media(prefers-reduced-motion:reduce)",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("motion system is missing %q", required)
		}
	}
	refresh, err := os.ReadFile("modules/refresh.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(refresh), "markInsertedMotion(freshChild.cloneNode(true))") {
		t.Error("reconciled dynamic content must animate only when inserted")
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
