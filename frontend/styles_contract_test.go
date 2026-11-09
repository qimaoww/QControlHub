package frontend

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestMotionSystemCoversWorkspaceInteractions(t *testing.T) {
	styles := string(mustReadFrontendFile(t, "analytics/theme.css"))
	for _, required := range []string{
		".a-dialog::backdrop", "@keyframes analytics-scrim", ".a-button[aria-busy=\"true\"]",
		"@media (prefers-reduced-motion: reduce)", "animation: none !important; transition: none !important; scroll-behavior: auto !important",
		":root[data-ui-theme=\"analytics\"][data-theme-motion=\"off\"]",
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("native motion styles missing %q", required)
		}
	}
	motion := frontendSources(t, "analytics/motion.js", "modules/theme-lifecycle.js")
	for _, required := range []string{
		"!scope.enabled()", "scope.onSuspend(cancel)", "observer.disconnect(); cancel()",
		"seenMessages", "messages.get(element)", "groups.slice(0, 2)",
		"beforetoggle", "validationMessage", "active.get(element)",
	} {
		if !strings.Contains(motion, required) {
			t.Errorf("native motion lifecycle missing %q", required)
		}
	}
	live := frontendSources(t, "analytics/live-dom.js", "analytics/config-studio.js")
	for _, required := range []string{
		"if (current.data !== fresh.data)", "if (control) return current;", "if (current.childNodes[index] !== child)",
		"current.reconcileTable?.(fresh)", "const retained = new Set(children)",
	} {
		if !strings.Contains(live, required) {
			t.Errorf("stable native updates missing %q", required)
		}
	}
	// Retain real animation/refresh behavior coverage in the existing browser
	// suites; retired CSS keyframe names cannot prove the shipped runtime works.
	suites := frontendSources(t, "agents_browser_runtime.mjs", "browser/motion.mjs", "browser/native-motion.mjs")
	for _, required := range []string{"import { testMotionRuntime }", "testMotionRuntime", "analytics"} {
		if !strings.Contains(suites, required) {
			t.Errorf("native motion regressions missing %q", required)
		}
	}
	for _, forbidden := range []string{".workspace-main.page-enter", ".qch-reconcile-enter", "@keyframes qch-nav-active"} {
		if strings.Contains(styles, forbidden) {
			t.Errorf("retired refresh entrance remains: %s", forbidden)
		}
	}
}

func TestNativeConsoleStylesRemainExact(t *testing.T) {
	styles := mustReadFrontendFile(t, "analytics/theme.css")
	// Preserve the reviewed native surface, including the requested compact
	// card-selection header, node network column, settings section selection
	// proportional raster logo containment in the existing brand mark, and
	// unavailable public-IP placeholders aligned with normal address rows, and
	// readable connection country/node columns within their scroll container.
	const expected = "4b6b3fee517d7814f9f0b59da5b78164f8e318e31eb45f82bfceb2fad3515d01"
	if actual := fmt.Sprintf("%x", sha256.Sum256(styles)); actual != expected {
		t.Fatalf("native theme hash = %s, want approved surface %s", actual, expected)
	}
	for _, required := range []string{".a-dialog", ".a-dialog::backdrop", ".a-code", ".a-button", ".a-form-grid", ".a-dashboard-periods", ".a-dashboard-live", "min-height: 44px", "overflow-wrap: anywhere"} {
		if !strings.Contains(string(styles), required) {
			t.Errorf("native console styles missing %q", required)
		}
	}
}
