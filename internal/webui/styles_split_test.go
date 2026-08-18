package webui

import (
	"strings"
	"testing"
)

// The stylesheet is split across the styles_*.go consts and reassembled in
// styles_desktop_app.go. These assertions catch a chunk that was dropped or
// reordered (e.g. by a future edit): the byte length is a sentinel for the
// whole sheet, and the chapter markers guard the split point ordering.
func TestDesktopAppStylesSplitIntegrity(t *testing.T) {
	css := desktopAppStyles

	// UTF-8 byte length of the full sheet, incl. multibyte
	// glyphs (✓ 收起 − …). Bump this whenever a styles_*.go const changes, in
	// the same edit.
	const wantLen = 136370
	if len(css) != wantLen {
		t.Fatalf("desktopAppStyles length = %d, want %d; update styles_*.go AND this guard together",
			len(css), wantLen)
	}

	if !strings.HasPrefix(css, ":root,[data-theme=light]{") {
		t.Fatal("desktopAppStyles should start with the light token block")
	}
	if !strings.HasSuffix(css, "@media(prefers-reduced-motion:reduce){.machine-resource-summary progress::-webkit-progress-value,.telemetry-lines progress::-webkit-progress-value{transition:none}.service-card,.context-primary,.context-list>a{transition:none}.page-agents .service-card:hover,.context-primary:hover{transform:none}}\n") {
		t.Fatal("desktopAppStyles should end with the reduced-motion guard")
	}

	for _, marker := range []string{
		"/* Cross-page layout rhythm",
		"/* Corporate Trust v30",
		"/* Control settings v31",
		"/* Mobile settings v32",
		"/* Mobile task batching v33",
		"/* Node workspace v50",
		"/* Node configuration v36",
		"/* Node resources v37",
		"/* Node enrollment v38",
		"/* Wide configuration editor v40",
		"/* Hierarchical configuration workbench v41",
		"/* Automatic client access v43",
		"/* Client access workspace v51",
		"/* Copy reduction v44",
		"/* GPT Image reference v45",
		"/* GPT Image reference v46",
		"/* Polish V47",
	} {
		if !strings.Contains(css, marker) {
			t.Fatalf("missing chapter marker: %s", marker)
		}
	}
}
