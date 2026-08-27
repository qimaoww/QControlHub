package agent

import (
	"os"
	"runtime"
	"testing"
)

func TestOperatingSystemPlatformFromRelease(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "debian",
			content: `PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
VERSION_ID="12"
ID=debian`,
			want: "Debian",
		},
		{
			name: "alpine",
			content: `NAME="Alpine Linux"
ID=alpine
VERSION_ID=3.22.1
PRETTY_NAME="Alpine Linux v3.22"`,
			want: "Alpine",
		},
		{
			name:    "fallback name and version",
			content: "NAME='Minimal Linux'\nVERSION_ID=1.2",
			want:    "Minimal Linux",
		},
		{
			name:    "escaped quote",
			content: "PRETTY_NAME=\"Example \\\"Linux\\\" 1\"",
			want:    `Example "Linux" 1`,
		},
		{
			name:    "missing identity",
			content: "HOME_URL=https://example.test",
			want:    "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := operatingSystemPlatformFromRelease([]byte(test.content)); got != test.want {
				t.Fatalf("operatingSystemPlatformFromRelease() = %q; want %q", got, test.want)
			}
		})
	}
}

func TestSanitizePlatformNameRemovesControlCharacters(t *testing.T) {
	if got := sanitizePlatformName("  Alpine\x00\tLinux   3.22  "); got != "Alpine Linux 3.22" {
		t.Fatalf("sanitizePlatformName() = %q; want %q", got, "Alpine Linux 3.22")
	}
}

func TestOperatingSystemPlatformLengthLimit(t *testing.T) {
	content := []byte(`PRETTY_NAME="abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"`)
	got := operatingSystemPlatformFromRelease(content)
	if len([]rune(got)) != maxPlatformNameRunes {
		t.Fatalf("platform rune count = %d; want %d (%q)", len([]rune(got)), maxPlatformNameRunes, got)
	}
}

func TestOperatingSystemPlatformCurrentHost(t *testing.T) {
	got := operatingSystemPlatform()
	if got == "" {
		t.Fatal("operatingSystemPlatform() returned an empty value")
	}
	t.Logf("detected platform %q", got)
	if runtime.GOOS != "linux" {
		if got != runtime.GOOS {
			t.Fatalf("operatingSystemPlatform() = %q; want %q", got, runtime.GOOS)
		}
		return
	}
	content, err := os.ReadFile("/etc/os-release")
	if err != nil {
		if got != runtime.GOOS {
			t.Fatalf("platform without os-release = %q; want %q", got, runtime.GOOS)
		}
		return
	}
	if want := operatingSystemPlatformFromRelease(content); want != "" && got != want {
		t.Fatalf("operatingSystemPlatform() = %q; want %q", got, want)
	}
}
