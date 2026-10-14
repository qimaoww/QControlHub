package githubfetch

import (
	"os/exec"
	"strings"
	"testing"
)

func TestGoShellURLContract(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/org/repo/releases/download/v1/file%2Fname.zip?x=a%2Fb&empty=&x=2",
		"https://api.github.com/repos/o/r/releases?per_page=5&page=2",
		"https://raw.githubusercontent.com/o/r/refs/heads/main/f%20name",
		"https://GITHUB.COM/o/r", "https://github.com?x=y",
		Prefix + "https://github.com/o/r/f?x=%2520",
		Prefix + Prefix + "https://github.com/o/r/f",
		"http://github.com/o/r", "https://user:secret@github.com/o/r",
		"https://github.com:443/o/r", "https://github.com.evil.example/o/r",
		"https://control.example/api/v1/agent-binary", "https://github.com/o/r#fragment",
		Prefix + "https://control.example/file", "https://github.com/a b", "https://github.com/a\\b",
	} {
		t.Run(raw, func(t *testing.T) {
			original, err := Original(raw)
			cmd := exec.Command("sh", "-c", `. "$1"; qch_github_original "$2"`, "sh", "../../deploy/modules/shared/github-download.sh", raw)
			output, shellErr := cmd.Output()
			if (shellErr == nil) != (err == nil) {
				t.Fatalf("Go error=%v Shell error=%v", err, shellErr)
			}
			if err != nil {
				return
			}
			if strings.TrimSuffix(string(output), "\n") != original {
				t.Fatalf("Go=%s Shell=%s", original, output)
			}
			first, err := Accelerated(raw)
			if err != nil {
				t.Fatal(err)
			}
			second, err := Accelerated(first)
			if err != nil || first != second || first != Prefix+original {
				t.Fatalf("not idempotent: %s %s %v", first, second, err)
			}
		})
	}
}
