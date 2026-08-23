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

func TestNormalizeCoreSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		engine   Engine
		selector string
		source   string
		want     string
		wantErr  bool
	}{
		{name: "omitted defaults to official", engine: EngineMihomo, selector: CoreVersionDevelopment, source: "", want: ""},
		{name: "mihomo development official", engine: EngineMihomo, selector: CoreVersionDevelopment, source: string(CoreSourceOfficial), want: string(CoreSourceOfficial)},
		{name: "mihomo development mirror", engine: EngineMihomo, selector: CoreVersionDevelopment, source: string(CoreSourceMirror), want: string(CoreSourceMirror)},
		{name: "mirror rejected for stable", engine: EngineMihomo, selector: CoreVersionStable, source: string(CoreSourceMirror), wantErr: true},
		{name: "mirror rejected for xray", engine: EngineXray, selector: CoreVersionDevelopment, source: string(CoreSourceMirror), wantErr: true},
		{name: "unknown source rejected", engine: EngineMihomo, selector: CoreVersionDevelopment, source: "private", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeCoreSource(test.engine, test.selector, test.source)
			if test.wantErr {
				if err == nil {
					t.Fatalf("NormalizeCoreSource() = %q, nil; want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("NormalizeCoreSource() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}
