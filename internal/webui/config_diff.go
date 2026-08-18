package webui

import (
	"html"
	"strings"
)

// configLineDiff is one rendered row of a line-level configuration diff.
type configDiffLine struct {
	Kind  rune // ' ' unchanged, '-' removed, '+' added
	Left  string
	Right string
}

// maxDiffCells bounds the LCS dynamic-programming table so adversarial
// configurations (up to 2 MiB each) cannot blow up page rendering. Larger
// inputs degrade to a single replacement block.
const maxDiffCells = 4_000_000

// configLineDiff computes a line-level diff between two configurations using
// longest common subsequence dynamic programming. Lines are compared
// byte-for-byte; identical lines are skipped and the remaining segments are
// aligned, which keeps JSON/YAML edits compact.
func configLineDiff(oldText, newText string) []configDiffLine {
	oldLines := splitDiffLines(oldText)
	newLines := splitDiffLines(newText)
	if len(oldLines)*len(newLines) > maxDiffCells {
		return []configDiffLine{
			{Kind: '-', Left: oldText},
			{Kind: '+', Right: newText},
		}
	}

	// dp[i][j] = LCS length of oldLines[:i] and newLines[:j].
	dp := make([]int, (len(oldLines)+1)*(len(newLines)+1))
	width := len(newLines) + 1
	for i := 1; i <= len(oldLines); i++ {
		row := i * width
		previousRow := row - width
		oldLine := oldLines[i-1]
		for j := 1; j <= len(newLines); j++ {
			if oldLine == newLines[j-1] {
				dp[row+j] = dp[previousRow+j-1] + 1
			} else if dp[previousRow+j] >= dp[row+j-1] {
				dp[row+j] = dp[previousRow+j]
			} else {
				dp[row+j] = dp[row+j-1]
			}
		}
	}

	result := make([]configDiffLine, 0, len(oldLines)+len(newLines))
	i, j := len(oldLines), len(newLines)
	for i > 0 && j > 0 {
		if oldLines[i-1] == newLines[j-1] {
			result = append(result, configDiffLine{Kind: ' ', Left: oldLines[i-1], Right: newLines[j-1]})
			i--
			j--
		} else if dp[(i-1)*width+j] >= dp[i*width+j-1] {
			result = append(result, configDiffLine{Kind: '-', Left: oldLines[i-1]})
			i--
		} else {
			result = append(result, configDiffLine{Kind: '+', Right: newLines[j-1]})
			j--
		}
	}
	for ; i > 0; i-- {
		result = append(result, configDiffLine{Kind: '-', Left: oldLines[i-1]})
	}
	for ; j > 0; j-- {
		result = append(result, configDiffLine{Kind: '+', Right: newLines[j-1]})
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func splitDiffLines(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	// A trailing newline produces a trailing empty line; drop it so equal
	// files yield an empty diff.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// renderConfigDiff renders the line diff between the saved and the deployed
// configuration as an HTML fragment (pre-escaped, suitable for the template).
// Identical content yields an empty fragment.
func renderConfigDiff(savedContent, deployedContent string) string {
	lines := configLineDiff(deployedContent, savedContent)
	changed := false
	for _, line := range lines {
		if line.Kind != ' ' {
			changed = true
			break
		}
	}
	if !changed {
		return ""
	}
	var builder strings.Builder
	builder.WriteString(`<pre class="config-diff" aria-label="配置差异">`)
	for _, line := range lines {
		switch line.Kind {
		case ' ':
			builder.WriteString(`<span class="diff-context"></span>`)
			builder.WriteString(html.EscapeString(line.Left))
		case '-':
			builder.WriteString(`<span class="diff-remove">- </span>`)
			builder.WriteString(html.EscapeString(line.Left))
		case '+':
			builder.WriteString(`<span class="diff-add">+ </span>`)
			builder.WriteString(html.EscapeString(line.Right))
		}
		builder.WriteByte('\n')
	}
	builder.WriteString(`</pre>`)
	return builder.String()
}
