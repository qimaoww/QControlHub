package core

import "testing"

func TestNormalizeCoreVersionSelector(t *testing.T) {
	t.Parallel()
	for input, expected := range map[string]string{
		"stable": CoreVersionStable, " DEVELOPMENT ": CoreVersionDevelopment,
		"dev": CoreVersionDevelopment, "v1.19.29": "1.19.29", "1.14.0-beta.3": "1.14.0-beta.3",
	} {
		if actual, err := NormalizeCoreVersionSelector(input); err != nil || actual != expected {
			t.Errorf("NormalizeCoreVersionSelector(%q) = %q, %v; want %q", input, actual, err, expected)
		}
	}
	for _, input := range []string{"", "latest", "https://example.com/core", "1.2", "1.2.3;reboot", "../1.2.3", "Prerelease-Alpha"} {
		if _, err := NormalizeCoreVersionSelector(input); err == nil {
			t.Errorf("NormalizeCoreVersionSelector(%q) unexpectedly succeeded", input)
		}
	}
}
