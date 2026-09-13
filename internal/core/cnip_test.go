package core

import "testing"

func TestCNIPSourceValidation(t *testing.T) {
	for _, format := range []string{"", "auto", "txt", "dat", "srs", "mmdb"} {
		if err := (CNIPSource{URL: "https://example.com/cn", Format: format}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []CNIPSource{{URL: "ftp://example.com/cn"}, {URL: "https://example.com/cn", Format: "exe"}, {URL: "https://user:pass@example.com/cn"}, {URL: "https://example.com/cn#x"}} {
		if source.Validate() == nil {
			t.Fatal("invalid source accepted")
		}
	}
	if (CNIPSource{}).Validate() != nil {
		t.Fatal("cannot restore defaults")
	}
}
