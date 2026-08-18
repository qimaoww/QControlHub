package webui

import (
	"strings"
	"testing"
)

func TestConfigLineDiffIdenticalContentIsEmpty(t *testing.T) {
	t.Parallel()
	content := "{\n  \"server\": \"0.0.0.0\",\n  \"server_port\": 8388\n}\n"
	lines := configLineDiff(content, content)
	for _, line := range lines {
		if line.Kind != ' ' {
			t.Fatalf("identical content produced changed line %+v", line)
		}
	}
	if renderConfigDiff(content, content) != "" {
		t.Fatal("renderConfigDiff of identical content should be empty")
	}
}

func TestConfigLineDiffTracksSingleLineChange(t *testing.T) {
	t.Parallel()
	oldText := "{\n  \"server\": \"0.0.0.0\",\n  \"server_port\": 8388\n}\n"
	newText := "{\n  \"server\": \"0.0.0.0\",\n  \"server_port\": 8389\n}\n"
	lines := configLineDiff(oldText, newText)
	var removed, added int
	for _, line := range lines {
		switch line.Kind {
		case '-':
			removed++
			if line.Left != "  \"server_port\": 8388" {
				t.Fatalf("removed line = %q", line.Left)
			}
		case '+':
			added++
			if line.Right != "  \"server_port\": 8389" {
				t.Fatalf("added line = %q", line.Right)
			}
		case ' ':
			if line.Left != line.Right {
				t.Fatalf("context line mismatch: %q vs %q", line.Left, line.Right)
			}
		}
	}
	if removed != 1 || added != 1 {
		t.Fatalf("diff = %d removed, %d added; want 1/1", removed, added)
	}
}

func TestConfigLineDiffHandlesInsertsAndDeletes(t *testing.T) {
	t.Parallel()
	oldText := "a\nb\nc\n"
	newText := "a\nx\nb\nc\n"
	lines := configLineDiff(oldText, newText)
	var sawAdd bool
	for _, line := range lines {
		if line.Kind == '+' && line.Right == "x" {
			sawAdd = true
		}
	}
	if !sawAdd {
		t.Fatalf("inserted line missing from diff: %+v", lines)
	}
}

func TestConfigLineDiffFallbackForHugeInputs(t *testing.T) {
	t.Parallel()
	oldText := strings.Repeat("line-a\n", 3000)
	newText := strings.Repeat("line-b\n", 3000)
	lines := configLineDiff(oldText, newText)
	if len(lines) != 2 {
		t.Fatalf("oversized diff produced %d lines, want single replacement pair", len(lines))
	}
	if lines[0].Kind != '-' || lines[1].Kind != '+' {
		t.Fatalf("oversized diff kinds = %c%c", lines[0].Kind, lines[1].Kind)
	}
}

func TestRenderConfigDiffEscapesHTML(t *testing.T) {
	t.Parallel()
	saved := "mode: rule\nproxies:\n  - name: <script>alert(1)</script>\n"
	deployed := "mode: global\n"
	rendered := renderConfigDiff(saved, deployed)
	if !strings.Contains(rendered, "&lt;script&gt;") {
		t.Fatalf("rendered diff did not escape HTML: %s", rendered)
	}
	if !strings.Contains(rendered, "diff-remove") || !strings.Contains(rendered, "diff-add") {
		t.Fatalf("rendered diff misses +/- rows: %s", rendered)
	}
	if renderConfigDiff(saved, saved) != "" {
		t.Fatal("renderConfigDiff of equal content should be empty")
	}
}
